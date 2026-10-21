//go:build linux

package isolation

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

type PseudoTerminal struct {
	master *os.File
	slave  *os.File
	input  *os.File
	output *os.File
}

func openPTY() (*PseudoTerminal, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	pty := &PseudoTerminal{master: master}
	cleanup := func() { _ = pty.Close() }
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		cleanup()
		return nil, fmt.Errorf("unlock pty: %w", err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("get pty number: %w", err)
	}
	pty.slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("open pty slave: %w", err)
	}
	inputFD, err := unix.Dup(int(master.Fd()))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("duplicate pty input: %w", err)
	}
	pty.input = os.NewFile(uintptr(inputFD), "minidock-pty-input")
	outputFD, err := unix.Dup(int(master.Fd()))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("duplicate pty output: %w", err)
	}
	pty.output = os.NewFile(uintptr(outputFD), "minidock-pty-output")
	return pty, nil
}

func (p *PseudoTerminal) Close() error {
	var first error
	for _, file := range []*os.File{p.slave, p.input, p.output, p.master} {
		if file != nil {
			if err := file.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

func (p *PseudoTerminal) Resize(columns, rows uint16) error {
	if p == nil || p.master == nil {
		return fmt.Errorf("container has no pty")
	}
	return unix.IoctlSetWinsize(int(p.master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: columns, Row: rows})
}
