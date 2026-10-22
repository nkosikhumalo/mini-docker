//go:build linux

package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	runtimev1 "mini-docker/go-runtime/gen/runtime/v1"
	"mini-docker/go-runtime/internal/cgroups"
	"mini-docker/go-runtime/internal/container"
	"mini-docker/go-runtime/internal/isolation"
	"mini-docker/go-runtime/internal/network"
)

func TestMain(m *testing.M) {
	if isolation.IsChild() {
		status, err := isolation.RunChild()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(status)
	}
	os.Exit(m.Run())
}

// TestGoRuntimeEndToEnd crosses the RPC boundary and exercises config
// validation, cgroup limit writes, namespace creation, loopback setup,
// pivot_root, workload exec, and exit-code delivery in one flow.
func TestGoRuntimeEndToEnd(t *testing.T) {
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Skip("busybox is required for the full runtime test")
	}
	temp := t.TempDir()
	rootfs := filepath.Join(temp, "rootfs")
	if err := os.MkdirAll(filepath.Join(rootfs, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(busybox, filepath.Join(rootfs, "bin", "busybox")); err != nil {
		t.Fatal(err)
	}

	cgroupRoot := filepath.Join(temp, "fake-cgroup")
	limitsWritten := new(cgroupSnapshot)
	networkCommands := new(fakeNetworkRunner)
	backend := systemBackend{
		ChildStarter: isolation.ChildStarter{Network: network.NewBridgeManagerWithRunner(networkCommands)},
		cgroupRoot:   cgroupRoot,
		snapshot:     limitsWritten,
	}
	server := grpc.NewServer()
	RegisterRuntime(server, func(ctx context.Context, config container.Config) error {
		return container.Run(ctx, config, backend)
	})
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = listener.Close() })
	t.Cleanup(server.Stop)
	conn, err := grpc.DialContext(context.Background(), "runtime-test",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	response, err := NewRuntimeClient(conn).Run(ctx, &runtimev1.RunRequest{
		Id:        "system-test",
		Rootfs:    rootfs,
		Command:   []string{"/bin/busybox", "sh", "-c", "test ! -e /etc/passwd && test -c /dev/null && /bin/busybox grep -q '^NoNewPrivs:[[:space:]]*1$' /proc/self/status"},
		MemoryMax: 64 << 20,
		CpuQuota:  50000,
		CpuPeriod: 100000,
		Network:   true,
	})
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("kernel policy blocks required namespaces or mounts: %v", err)
		}
		t.Fatal(err)
	}
	if response.ExitCode != 0 {
		t.Fatalf("container command exited with code %d", response.ExitCode)
	}

	if limitsWritten.memoryMax != "67108864" || limitsWritten.cpuMax != "50000 100000" || limitsWritten.pid == "" {
		t.Fatalf("unexpected cgroup controls: %#v", limitsWritten)
	}
	if !strings.Contains(strings.Join(networkCommands.commands, "\n"), "ip route replace default via 10.0.0.1 dev eth0") {
		t.Fatalf("network setup did not configure a default route: %v", networkCommands.commands)
	}
	if !strings.Contains(strings.Join(networkCommands.commands, "\n"), "iptables -t nat -A POSTROUTING -s 10.0.0.0/24 ! -o minidock0 -j MASQUERADE -m comment --comment minidock-managed") {
		t.Fatalf("network setup did not configure masquerading: %v", networkCommands.commands)
	}
	if err := backend.Network.Close(); err != nil {
		t.Fatalf("close network manager: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cgroupRoot, "system-test")); !os.IsNotExist(err) {
		t.Fatalf("temporary cgroup was not cleaned up: %v", err)
	}
	if matches, err := filepath.Glob(filepath.Join(rootfs, ".minidock-oldroot-*")); err != nil || len(matches) != 0 {
		t.Fatalf("old root mount point leaked into supplied rootfs: %v, %v", matches, err)
	}
}

// TestRuntimeSocketPermissions checks the filesystem permission mode used by
// the daemon. Some restricted environments prohibit creating Unix sockets; in
// those environments the end-to-end RPC test still runs through bufconn.
func TestRuntimeSocketPermissions(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "runtime.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skipf("environment prohibits Unix sockets: %v", err)
		}
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0660); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0660 {
		t.Fatalf("runtime socket permissions are %o, want 660", info.Mode().Perm())
	}
}

func TestManagedStartWaitStopLifecycle(t *testing.T) {
	server := grpc.NewServer()
	workloadDone := make(chan struct{})
	RegisterManagedRuntime(server, func(ctx context.Context, _ container.Config, started func(container.StartedProcess)) error {
		started(container.StartedProcess{PID: 4242})
		select {
		case <-ctx.Done():
			return nil
		case <-workloadDone:
			return nil
		}
	})
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = listener.Close(); server.Stop() })
	conn, err := grpc.DialContext(context.Background(), "managed-test",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := NewRuntimeClient(conn)
	request := &runtimev1.RunRequest{Id: "managed-test", Rootfs: "/tmp/minidock-test-root", Command: []string{"/bin/true"}}
	started, err := client.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if started.GetPid() != 4242 {
		t.Fatalf("start returned pid %d, want 4242", started.GetPid())
	}
	waited := make(chan error, 1)
	go func() {
		_, err := client.Wait(context.Background(), &runtimev1.WaitRequest{Id: request.GetId()})
		waited <- err
	}()
	if _, err := client.Stop(context.Background(), &runtimev1.StopRequest{Id: request.GetId()}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("wait after stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not finish after stop")
	}
}

func TestAttachRelaysInputAndOutput(t *testing.T) {
	server := grpc.NewServer()
	RegisterManagedRuntime(server, func(ctx context.Context, _ container.Config, started func(container.StartedProcess)) error {
		stdinReader, stdinWriter := io.Pipe()
		stdoutReader, stdoutWriter := io.Pipe()
		started(container.StartedProcess{PID: 5151, Stdin: stdinWriter, Stdout: stdoutReader})
		finished := make(chan error, 1)
		go func() {
			_, err := io.Copy(stdoutWriter, stdinReader)
			_ = stdoutWriter.Close()
			_ = stdinReader.Close()
			finished <- err
		}()
		select {
		case err := <-finished:
			return err
		case <-ctx.Done():
			_ = stdinReader.Close()
			_ = stdoutWriter.Close()
			return ctx.Err()
		}
	})
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = listener.Close(); server.Stop() })
	conn, err := grpc.DialContext(context.Background(), "attach-test",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := NewRuntimeClient(conn)
	request := &runtimev1.RunRequest{Id: "attach-test", Rootfs: "/tmp/minidock-test-root", Command: []string{"/bin/sh"}, Attach: true}
	started, err := client.Start(context.Background(), request)
	if err != nil || started.GetPid() != 5151 {
		t.Fatalf("start returned %#v, %v", started, err)
	}
	stream, err := client.Attach(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.SendMsg(&runtimev1.AttachInput{ContainerId: request.GetId()}); err != nil {
		t.Fatal(err)
	}
	if err := stream.SendMsg(&runtimev1.AttachInput{Stdin: []byte("interactive input\n")}); err != nil {
		t.Fatal(err)
	}
	if err := stream.SendMsg(&runtimev1.AttachInput{StdinEof: true}); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	gotExit := false
	for {
		message := new(runtimev1.AttachOutput)
		err := stream.RecvMsg(message)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if message.GetExited() {
			gotExit = true
			if message.GetExitCode() != 0 {
				t.Fatalf("attach exit code = %d", message.GetExitCode())
			}
			continue
		}
		output.Write(message.GetData())
	}
	if output.String() != "interactive input\n" {
		t.Fatalf("attach output = %q", output.String())
	}
	if !gotExit {
		t.Fatal("attach stream did not return an exit event")
	}
	if _, err := client.Wait(context.Background(), &runtimev1.WaitRequest{Id: request.GetId()}); err != nil {
		t.Fatal(err)
	}
}

func TestListActiveContainersReportsLiveSessions(t *testing.T) {
	item := &session{id: "container-a", pid: os.Getpid(), networkIP: "10.0.0.2", done: make(chan struct{})}
	server := &runtimeServer{sessions: map[string]*session{item.id: item}}
	response, err := server.ListActiveContainers(context.Background(), &runtimev1.ListActiveContainersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetContainers()) != 1 || response.GetContainers()[0].GetId() != item.id || int(response.GetContainers()[0].GetPid()) != item.pid {
		t.Fatalf("unexpected active session response: %#v", response.GetContainers())
	}
}

func TestGetContainerLogsSupportsBoundedOffsets(t *testing.T) {
	root := t.TempDir()
	containerDir := filepath.Join(root, "container-a")
	if err := os.Mkdir(containerDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(containerDir, "container.log"), []byte("abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	server := &runtimeServer{logRoot: root}
	response, err := server.GetContainerLogs(context.Background(), &runtimev1.GetContainerLogsRequest{Id: "container-a", Offset: 2, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response.GetData(), []byte("cde")) || response.GetSize() != 6 {
		t.Fatalf("unexpected log slice %q of size %d", response.GetData(), response.GetSize())
	}
	if _, err := server.GetContainerLogs(context.Background(), &runtimev1.GetContainerLogsRequest{Id: "../outside"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("path traversal id should be rejected, got %v", err)
	}
}

func TestRelayOutputUnblocksWhenSessionStops(t *testing.T) {
	reader := io.NopCloser(strings.NewReader(strings.Repeat("x", 96*1024)))
	output := make(chan *runtimev1.AttachOutput, 1)
	stop := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(1)
	go relayOutput(reader, 1, output, stop, &wait)
	time.Sleep(10 * time.Millisecond)
	close(stop)
	done := make(chan struct{})
	go func() { wait.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("output relay remained blocked after its session stopped")
	}
}

type systemBackend struct {
	isolation.ChildStarter
	cgroupRoot string
	snapshot   *cgroupSnapshot
}

func (b systemBackend) AttachCgroup(_ context.Context, config container.Config, pid int) (func() error, error) {
	group, cleanup, err := cgroups.CreateGroup(b.cgroupRoot, config.ID)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"memory.max", "cpu.max", "cgroup.procs"} {
		if err := os.WriteFile(filepath.Join(group, name), nil, 0600); err != nil {
			_ = cleanup()
			return nil, err
		}
	}
	err = cgroups.WriteLimits(group, cgroups.Limits{
		MemoryMax: config.MemoryMax,
		CPUQuota:  config.CPUQuota,
		CPUPeriod: config.CPUPeriod,
	}, pid)
	if err != nil {
		_ = cleanup()
		return nil, err
	}
	return func() error {
		b.snapshot.memoryMax = readFile(filepath.Join(group, "memory.max"))
		b.snapshot.cpuMax = readFile(filepath.Join(group, "cpu.max"))
		b.snapshot.pid = readFile(filepath.Join(group, "cgroup.procs"))
		if err := cleanup(); err != nil {
			// A normal cgroup mount exposes virtual control files that vanish on
			// rmdir. This test uses regular files to simulate those controls.
			return os.RemoveAll(group)
		}
		return nil
	}, nil
}

type cgroupSnapshot struct{ memoryMax, cpuMax, pid string }

type fakeNetworkRunner struct {
	commands     []string
	bridgeExists bool
}

func (r *fakeNetworkRunner) Run(_ context.Context, name string, args ...string) error {
	command := strings.Join(append([]string{name}, args...), " ")
	r.commands = append(r.commands, command)
	if name == "iptables" && len(args) > 2 && args[2] == "-C" {
		return errors.New("rule does not exist")
	}
	if command == "ip link show dev minidock0" && !r.bridgeExists {
		return errors.New("bridge does not exist")
	}
	if command == "ip link add name minidock0 type bridge" {
		r.bridgeExists = true
	}
	return nil
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func readFile(path string) string {
	value, _ := os.ReadFile(path)
	return string(value)
}
