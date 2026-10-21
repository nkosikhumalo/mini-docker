//go:build linux

package isolation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"mini-docker/go-runtime/internal/cgroups"
	"mini-docker/go-runtime/internal/container"
	"mini-docker/go-runtime/internal/network"
	"mini-docker/go-runtime/internal/rootfs"
)

const childEnv = "MINIDOCK_INTERNAL_CHILD"

var namespaceTypes = []string{"pid", "mnt", "net", "uts", "ipc"}

// ChildStarter re-executes the current binary with a fresh set of Linux
// namespaces. The child remains blocked until Release is called by Exec.
type ChildStarter struct {
	CgroupRoot string
	Network    *network.BridgeManager
}

// StartPaused creates the child in fresh PID, mount, network, UTS, and IPC
// namespaces. It does not require a privileged command; Linux permissions
// determine whether the kernel permits these namespaces for this caller.
func (ChildStarter) StartPaused(ctx context.Context, _ container.Config) (container.Child, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create child release pipe: %w", err)
	}
	readyR, readyW, err := os.Pipe()
	if err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, fmt.Errorf("create child setup acknowledgement pipe: %w", err)
	}
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.ExtraFiles = []*os.File{r, readyW}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
	if err := cmd.Start(); err != nil {
		_ = r.Close()
		_ = w.Close()
		_ = readyR.Close()
		_ = readyW.Close()
		return nil, fmt.Errorf("start namespaced child: %w", err)
	}
	_ = r.Close()
	_ = readyW.Close()
	child := &Child{cmd: cmd, release: w, ready: readyR}
	if err := child.verifyNamespaces(); err != nil {
		_ = child.Close()
		_ = cmd.Wait()
		return nil, err
	}
	return child, nil
}

// AttachCgroup applies requested resource limits in cgroups v2. With no
// limits, it avoids creating an unnecessary group.
func (starter ChildStarter) AttachCgroup(_ context.Context, cfg container.Config, pid int) (func() error, error) {
	if cfg.MemoryMax != 0 || cfg.CPUQuota != 0 {
		root := starter.CgroupRoot
		if root == "" {
			root = os.Getenv("MINIDOCK_CGROUP_ROOT")
		}
		if root == "" {
			root = "/sys/fs/cgroup/minidock"
		}
		return cgroups.Attach(root, cfg.ID, cgroups.Limits{MemoryMax: cfg.MemoryMax, CPUQuota: cfg.CPUQuota, CPUPeriod: cfg.CPUPeriod}, pid)
	}
	return nil, nil
}

// ConfigureNetwork optionally attaches a host bridge and veth to the paused
// child before releasing it into its workload.
func (starter ChildStarter) ConfigureNetwork(ctx context.Context, cfg container.Config, pid int) (func() error, error) {
	if !cfg.Network {
		return nil, nil
	}
	if starter.Network == nil {
		return nil, errors.New("container networking was requested but no bridge manager is configured")
	}
	return starter.Network.ConfigureWithIP(ctx, cfg.ID, pid, cfg.NetworkIP)
}

// Child is the host-side handle for a process blocked in the namespace child
// entry point.
type Child struct {
	cmd      *exec.Cmd
	release  *os.File
	ready    *os.File
	released bool
	waited   bool
}

func (c *Child) PID() int { return c.cmd.Process.Pid }

// Exec sends the workload configuration and releases the child. In the child
// process this method name corresponds to entering the workload via execve.
func (c *Child) Exec(_ context.Context, config container.Config) error {
	if c.released {
		return errors.New("namespaced child was already released")
	}
	err := json.NewEncoder(c.release).Encode(config)
	closeErr := c.release.Close()
	c.released = true
	if err != nil {
		return fmt.Errorf("send child configuration: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("release child: %w", closeErr)
	}
	var acknowledgment [1]byte
	if _, err := io.ReadFull(c.ready, acknowledgment[:]); err != nil {
		return fmt.Errorf("container setup failed before workload exec: %w", err)
	}
	_ = c.ready.Close()
	c.ready = nil
	return nil
}

func (c *Child) Wait() error {
	if c.waited {
		return errors.New("namespaced child was already waited")
	}
	c.waited = true
	return c.cmd.Wait()
}

func (c *Child) Close() error {
	if c.release != nil {
		_ = c.release.Close()
		c.release = nil
	}
	if c.ready != nil {
		_ = c.ready.Close()
		c.ready = nil
	}
	if c.cmd.ProcessState != nil || c.cmd.Process == nil {
		return nil
	}
	err := c.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (c *Child) verifyNamespaces() error {
	for _, ns := range namespaceTypes {
		host, err := os.Readlink(filepath.Join("/proc/self/ns", ns))
		if err != nil {
			return fmt.Errorf("read host %s namespace: %w", ns, err)
		}
		isolated, err := os.Readlink(filepath.Join("/proc", fmt.Sprint(c.PID()), "ns", ns))
		if err != nil {
			return fmt.Errorf("read child %s namespace: %w", ns, err)
		}
		if host == isolated {
			return fmt.Errorf("child did not enter a new %s namespace", ns)
		}
	}
	return nil
}

// RunChild is called at the start of main when the process was re-executed by
// ChildStarter. It reads the config from fd 3, then replaces itself with the
// requested executable. The caller should exit with the returned status.
func RunChild() (int, error) {
	if os.Getenv(childEnv) != "1" {
		return 0, errors.New("not a minidock namespace child")
	}
	fd := os.NewFile(3, "minidock-child-config")
	if fd == nil {
		return 0, errors.New("missing child configuration pipe")
	}
	defer fd.Close()
	ready := os.NewFile(4, "minidock-child-ready")
	if ready == nil {
		return 1, errors.New("missing child setup acknowledgement pipe")
	}
	defer ready.Close()
	var config container.Config
	if err := json.NewDecoder(fd).Decode(&config); err != nil {
		return 0, fmt.Errorf("read child configuration: %w", err)
	}
	if len(config.Command) == 0 || config.Command[0] == "" {
		return 0, errors.New("child configuration has no executable")
	}
	hostname := config.Hostname
	if hostname == "" {
		hostname = config.ID
	}
	if hostname != "" {
		if err := syscall.Sethostname([]byte(hostname)); err != nil {
			return 1, fmt.Errorf("set container hostname: %w", err)
		}
	}
	if err := network.BringLoopbackUp(); err != nil {
		return 1, fmt.Errorf("configure loopback in network namespace: %w", err)
	}
	if config.RootFS != "" {
		if err := rootfs.Enter(config.RootFS); err != nil {
			return 1, fmt.Errorf("enter container rootfs: %w", err)
		}
	}
	path, err := exec.LookPath(config.Command[0])
	if err != nil {
		return 0, fmt.Errorf("resolve workload executable: %w", err)
	}
	env := config.Env
	if env == nil {
		env = make([]string, 0, len(os.Environ()))
		for _, item := range os.Environ() {
			if !strings.HasPrefix(item, childEnv+"=") {
				env = append(env, item)
			}
		}
	}
	syscall.CloseOnExec(int(ready.Fd()))
	if _, err := ready.Write([]byte{1}); err != nil {
		return 1, fmt.Errorf("acknowledge container setup: %w", err)
	}
	if err := syscall.Exec(path, config.Command, env); err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return 127, nil
		}
		if errors.Is(err, syscall.EACCES) {
			return 126, nil
		}
		return 1, fmt.Errorf("exec workload: %w", err)
	}
	return 1, errors.New("exec returned unexpectedly")
}

// IsChild reports whether this process was launched as the hidden re-exec
// child entry point.
func IsChild() bool { return os.Getenv(childEnv) == "1" }
