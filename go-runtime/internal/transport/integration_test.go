//go:build linux && integration

package transport

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"mini-docker/go-runtime/internal/container"
	"mini-docker/go-runtime/internal/isolation"
	"mini-docker/go-runtime/internal/network"
)

// TestPrivilegedRuntimeIntegration exercises real namespaces, mounts, cgroup
// controls, a bridge/veth pair, and firewall setup. Run only via the explicit
// privileged e2e target on a disposable Linux host.
func TestPrivilegedRuntimeIntegration(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("privileged runtime integration requires root")
	}
	cgroupRoot := os.Getenv("MINIDOCK_CGROUP_ROOT")
	if !filepath.IsAbs(cgroupRoot) || filepath.Clean(cgroupRoot) == "/sys/fs/cgroup" {
		t.Fatal("MINIDOCK_CGROUP_ROOT must name a dedicated writable delegated cgroup v2 subtree")
	}
	if _, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers")); err != nil {
		t.Fatalf("inspect delegated cgroup controllers: %v", err)
	}

	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatal("busybox is required for the privileged runtime integration test")
	}
	rootfs := filepath.Join(t.TempDir(), "rootfs")
	if err := os.MkdirAll(filepath.Join(rootfs, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(busybox, filepath.Join(rootfs, "bin", "busybox")); err != nil {
		t.Fatal(err)
	}

	manager := network.NewBridgeManager()
	defer func() {
		if err := manager.Close(); err != nil {
			t.Errorf("clean up real bridge/firewall state: %v", err)
		}
	}()

	id := fmt.Sprintf("e2e-%d", os.Getpid())
	backend := isolation.ChildStarter{CgroupRoot: cgroupRoot, Network: manager}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = container.Run(ctx, container.Config{
		ID:        id,
		RootFS:    rootfs,
		Command:   []string{"/bin/busybox", "sh", "-c", "test -c /dev/null && /bin/busybox grep -q '^NoNewPrivs:[[:space:]]*1$' /proc/self/status && /bin/busybox grep -q eth0 /proc/net/route"},
		MemoryMax: 64 << 20,
		CPUQuota:  50000,
		CPUPeriod: 100000,
		Network:   true,
	}, backend)
	if err != nil {
		t.Fatalf("run isolated workload with real host resources: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cgroupRoot, id)); !os.IsNotExist(err) {
		t.Fatalf("runtime left cgroup %q behind: %v", id, err)
	}
}
