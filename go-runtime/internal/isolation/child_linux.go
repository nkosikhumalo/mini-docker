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
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"mini-docker/go-runtime/internal/cgroups"
	"mini-docker/go-runtime/internal/container"
	"mini-docker/go-runtime/internal/network"
	"mini-docker/go-runtime/internal/rootfs"
)

const childEnv = "MINIDOCK_INTERNAL_CHILD"

var namespaceTypes = []string{"user", "pid", "mnt", "net", "uts", "ipc"}

// ChildStarter re-executes the current binary with a fresh set of Linux
// namespaces. The child remains blocked until Release is called by Exec.
type ChildStarter struct {
	CgroupRoot string
	LogRoot    string
	Network    *network.BridgeManager
}

// StartPaused creates the child in fresh PID, mount, network, UTS, and IPC
// namespaces. It does not require a privileged command; Linux permissions
// determine whether the kernel permits these namespaces for this caller.
func (starter ChildStarter) StartPaused(ctx context.Context, config container.Config) (container.Child, error) {
	hostUID, err := mappedHostID("MINIDOCK_HOST_UID", "SUDO_UID", os.Getuid())
	if err != nil {
		return nil, err
	}
	hostGID, err := mappedHostID("MINIDOCK_HOST_GID", "SUDO_GID", os.Getgid())
	if err != nil {
		return nil, err
	}
	// Keep an open handle to the running runtime instead of re-executing its
	// pathname. Namespace UID mappings can make a root daemon's child unable
	// to traverse a private home directory such as /home/nkosi (mode 0750).
	executable, err := os.Open("/proc/self/exe")
	if err != nil {
		return nil, fmt.Errorf("open runtime executable for namespace child: %w", err)
	}
	defer executable.Close()

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
	cmd := exec.CommandContext(ctx, "/proc/self/fd/5")
	child := &Child{cmd: cmd}
	var inherited []*os.File
	var logFile *os.File
	if config.TTY {
		child.pty, err = openPTY()
		if err != nil {
			_ = r.Close()
			_ = w.Close()
			_ = readyR.Close()
			_ = readyW.Close()
			return nil, fmt.Errorf("create container pty: %w", err)
		}
		child.stdin, child.stdout = child.pty.input, child.pty.output
		cmd.Stdin, cmd.Stdout, cmd.Stderr = child.pty.slave, child.pty.slave, child.pty.slave
	} else if config.Attach {
		stdinRead, stdinWrite, pipeErr := os.Pipe()
		if pipeErr != nil {
			err = pipeErr
		}
		if err == nil {
			child.stdin, cmd.Stdin = stdinWrite, stdinRead
			inherited = append(inherited, stdinRead)
		}
		if err != nil {
			_ = r.Close()
			_ = w.Close()
			_ = readyR.Close()
			_ = readyW.Close()
			return nil, fmt.Errorf("create container stdin pipe: %w", err)
		}
		stdoutRead, stdoutWrite, pipeErr := os.Pipe()
		if pipeErr != nil {
			err = pipeErr
		}
		if err == nil {
			child.stdout, cmd.Stdout = stdoutRead, stdoutWrite
			inherited = append(inherited, stdoutWrite)
		}
		if err != nil {
			_ = r.Close()
			_ = w.Close()
			_ = readyR.Close()
			_ = readyW.Close()
			_ = child.stdin.Close()
			for _, file := range inherited {
				_ = file.Close()
			}
			return nil, fmt.Errorf("create container stdout pipe: %w", err)
		}
		stderrRead, stderrWrite, pipeErr := os.Pipe()
		if pipeErr != nil {
			err = pipeErr
		}
		if err == nil {
			child.stderr, cmd.Stderr = stderrRead, stderrWrite
			inherited = append(inherited, stderrWrite)
		}
		if err != nil {
			_ = child.stdin.Close()
			_ = child.stdout.Close()
			for _, file := range inherited {
				_ = file.Close()
			}
			_ = r.Close()
			_ = w.Close()
			_ = readyR.Close()
			_ = readyW.Close()
			return nil, fmt.Errorf("create container stderr pipe: %w", err)
		}
	} else {
		cmd.Stdin = os.Stdin
		if config.LogPath != "" {
			root := starter.LogRoot
			if root == "" {
				root = defaultContainerRoot()
			}
			want, pathErr := filepath.Abs(filepath.Join(root, config.ID, "container.log"))
			got, gotErr := filepath.Abs(config.LogPath)
			if pathErr != nil || gotErr != nil || filepath.Clean(got) != filepath.Clean(want) {
				_ = r.Close()
				_ = w.Close()
				_ = readyR.Close()
				_ = readyW.Close()
				return nil, errors.New("log path must be the container's configured container.log")
			}
			logDir := filepath.Dir(want)
			logDirInfo, dirErr := os.Lstat(logDir)
			if dirErr != nil || !logDirInfo.IsDir() || logDirInfo.Mode()&os.ModeSymlink != 0 {
				_ = r.Close()
				_ = w.Close()
				_ = readyR.Close()
				_ = readyW.Close()
				return nil, errors.New("container log directory must be a real directory")
			}
			logFD, openErr := unix.Open(want, unix.O_WRONLY|unix.O_CREAT|unix.O_APPEND|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
			if openErr != nil {
				_ = r.Close()
				_ = w.Close()
				_ = readyR.Close()
				_ = readyW.Close()
				return nil, fmt.Errorf("open container log: %w", openErr)
			}
			logFile = os.NewFile(uintptr(logFD), "minidock-container-log")
			child.logFile = logFile
			logWriter := &cappedLogWriter{file: logFile}
			cmd.Stdout, cmd.Stderr = logWriter, logWriter
		} else {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}
	}
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.ExtraFiles = []*os.File{r, readyW, executable}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC,
		Pdeathsig:                  syscall.SIGKILL,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: hostUID, Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: hostGID, Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
	if config.TTY {
		cmd.SysProcAttr.Setsid, cmd.SysProcAttr.Setctty, cmd.SysProcAttr.Ctty = true, true, 0
	}
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		for _, file := range inherited {
			_ = file.Close()
		}
		if child.stdin != nil {
			_ = child.stdin.Close()
		}
		if child.stdout != nil {
			_ = child.stdout.Close()
		}
		if child.stderr != nil {
			_ = child.stderr.Close()
		}
		if child.pty != nil {
			_ = child.pty.Close()
		}
		_ = r.Close()
		_ = w.Close()
		_ = readyR.Close()
		_ = readyW.Close()
		return nil, fmt.Errorf("start namespaced child: %w", err)
	}
	for _, file := range inherited {
		_ = file.Close()
	}
	if child.pty != nil && child.pty.slave != nil {
		_ = child.pty.slave.Close()
		child.pty.slave = nil
	}
	_ = r.Close()
	_ = readyW.Close()
	child.release = w
	child.ready = readyR
	if err := child.verifyNamespaces(); err != nil {
		_ = child.Close()
		return nil, err
	}
	return child, nil
}

func mappedHostID(configKey, sudoKey string, fallback int) (int, error) {
	value := os.Getenv(configKey)
	if value == "" && os.Geteuid() == 0 {
		value = os.Getenv(sudoKey)
	}
	if value == "" {
		return fallback, nil
	}
	id, err := strconv.Atoi(value)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("invalid %s value %q", configKey, value)
	}
	return id, nil
}

func defaultContainerRoot() string {
	if configured := os.Getenv("MINIDOCK_CONTAINERS_DIR"); configured != "" {
		return filepath.Clean(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/var/lib/minidock/containers"
	}
	return filepath.Join(home, ".local", "share", "minidock", "containers")
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
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   io.ReadCloser
	logFile  *os.File
	pty      *PseudoTerminal
	release  *os.File
	ready    *os.File
	released bool
	waited   bool
}

func (c *Child) PID() int                          { return c.cmd.Process.Pid }
func (c *Child) Stdin() io.WriteCloser             { return c.stdin }
func (c *Child) Stdout() io.ReadCloser             { return c.stdout }
func (c *Child) Stderr() io.ReadCloser             { return c.stderr }
func (c *Child) Resize(columns, rows uint16) error { return c.pty.Resize(columns, rows) }

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
	return errors.Join(c.cmd.Wait(), c.closeLogFile())
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
	if c.pty != nil {
		_ = c.pty.Close()
		c.pty = nil
	}
	var result error
	if c.cmd.ProcessState == nil && c.cmd.Process != nil && !c.waited {
		err := c.cmd.Process.Kill()
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			result = errors.Join(result, err)
		}
		c.waited = true
		waitErr := c.cmd.Wait()
		var exitError *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitError) {
			result = errors.Join(result, waitErr)
		}
	}
	return errors.Join(result, c.closeLogFile())
}

func (c *Child) closeLogFile() error {
	if c.logFile == nil {
		return nil
	}
	err := c.logFile.Close()
	c.logFile = nil
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
// ChildStarter. It reads config from fd 3, closes the inherited runtime handle
// at fd 5, then replaces itself with the requested executable. The caller
// should exit with the returned status.
func RunChild() (int, error) {
	runtime.LockOSThread()
	if os.Getenv(childEnv) != "1" {
		return 0, errors.New("not a minidock namespace child")
	}
	executable := os.NewFile(5, "minidock-runtime-executable")
	if executable == nil {
		return 1, errors.New("missing inherited runtime executable")
	}
	_ = executable.Close()
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
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return 1, fmt.Errorf("set no_new_privs: %w", err)
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
