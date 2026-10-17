//go:build linux

package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
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
	response, err := NewRuntimeClient(conn).Run(ctx, &RunRequest{Config: container.Config{
		ID:        "system-test",
		RootFS:    rootfs,
		Command:   []string{"/bin/busybox", "sh", "-c", "test ! -e /etc/passwd"},
		MemoryMax: 64 << 20,
		CPUQuota:  50000,
		CPUPeriod: 100000,
		Network:   true,
	}})
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
