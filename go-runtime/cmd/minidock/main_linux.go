//go:build linux

// minidock contains the small terminal attach client used by bin/minidock.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtimev1 "mini-docker/go-runtime/gen/runtime/v1"
	"mini-docker/go-runtime/internal/transport"
)

func main() {
	if len(os.Args) != 3 || os.Args[1] != "attach" {
		fmt.Fprintln(os.Stderr, "Usage: minidock attach CONTAINER_ID")
		os.Exit(2)
	}
	code, err := attach(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "minidock attach:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func attach(id string) (int, error) {
	socket := os.Getenv("MINIDOCK_SOCKET")
	if socket == "" {
		socket = "/tmp/minidock-runtime.sock"
	}
	if !filepath.IsAbs(socket) {
		return 1, errors.New("MINIDOCK_SOCKET must be an absolute path")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := grpc.DialContext(ctx, "unix://"+socket,
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return 1, fmt.Errorf("connect to runtime socket: %w", err)
	}
	defer conn.Close()
	stream, err := transport.NewRuntimeClient(conn).Attach(ctx)
	if err != nil {
		return 1, fmt.Errorf("open terminal stream: %w", err)
	}
	defer stream.CloseSend()

	restore, tty := rawTerminal(int(os.Stdin.Fd()))
	if restore != nil {
		defer restore()
	}
	rows, columns := terminalSize(int(os.Stdin.Fd()))
	if err := stream.SendMsg(&runtimev1.AttachInput{ContainerId: id, Rows: rows, Columns: columns}); err != nil {
		return 1, fmt.Errorf("identify container session: %w", err)
	}

	input := make(chan *runtimev1.AttachInput, 16)
	go readTerminal(input, tty)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			r, c := terminalSize(int(os.Stdin.Fd()))
			select {
			case input <- &runtimev1.AttachInput{Rows: r, Columns: c}:
			default:
			}
		}
	}()

	sendDone := make(chan error, 1)
	go func() {
		for message := range input {
			if err := stream.SendMsg(message); err != nil {
				sendDone <- err
				return
			}
			if message.GetStdinEof() {
				_ = stream.CloseSend()
				sendDone <- nil
				return
			}
		}
	}()

	for {
		output := new(runtimev1.AttachOutput)
		if err := stream.RecvMsg(output); err != nil {
			if errors.Is(err, io.EOF) {
				return 0, nil
			}
			return 1, fmt.Errorf("read container output: %w", err)
		}
		if output.GetExited() {
			return int(output.GetExitCode()), nil
		}
		writer := io.Writer(os.Stdout)
		if output.GetStream() == 2 {
			writer = os.Stderr
		}
		if _, err := writer.Write(output.GetData()); err != nil {
			return 1, fmt.Errorf("write terminal output: %w", err)
		}
	}
}

func readTerminal(messages chan<- *runtimev1.AttachInput, isTTY bool) {
	defer close(messages)
	buffer := make([]byte, 32*1024)
	for {
		count, err := os.Stdin.Read(buffer)
		if count > 0 {
			data := append([]byte(nil), buffer[:count]...)
			messages <- &runtimev1.AttachInput{Stdin: data}
		}
		if err != nil {
			messages <- &runtimev1.AttachInput{StdinEof: true}
			return
		}
		if count == 0 && !isTTY {
			time.Sleep(time.Millisecond)
		}
	}
}

func rawTerminal(fd int) (func(), bool) {
	previous, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, false
	}
	raw := *previous
	raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Cflag |= unix.CS8
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, false
	}
	return func() { _ = unix.IoctlSetTermios(fd, unix.TCSETS, previous) }, true
}

func terminalSize(fd int) (uint32, uint32) {
	size, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return 24, 80
	}
	rows, columns := size.Row, size.Col
	if rows == 0 {
		rows = 24
	}
	if columns == 0 {
		columns = 80
	}
	return uint32(rows), uint32(columns)
}
