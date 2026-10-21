// Package container defines the runtime's container configuration and
// coordinates the host-side setup and isolated child process.
package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

// Config describes one container launch. RootFS must be an absolute path to a
// prepared root filesystem. Environment entries use KEY=value form.
type Config struct {
	ID        string   `json:"id"`
	RootFS    string   `json:"rootfs"`
	Command   []string `json:"command"`
	Env       []string `json:"env,omitempty"`
	Hostname  string   `json:"hostname,omitempty"`
	MemoryMax uint64   `json:"memory_max,omitempty"`
	CPUQuota  uint64   `json:"cpu_quota,omitempty"`
	CPUPeriod uint64   `json:"cpu_period,omitempty"`
	Network   bool     `json:"network,omitempty"`
	NetworkIP string   `json:"network_ip,omitempty"`
	Attach    bool     `json:"attach,omitempty"`
	TTY       bool     `json:"tty,omitempty"`
}

// Validate rejects incomplete or unsafe-to-interpret launch descriptions.
func (c Config) Validate() error {
	if c.ID == "" {
		return errors.New("container id is required")
	}
	for _, char := range c.ID {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.') {
			return errors.New("container id may contain only letters, digits, dot, underscore, and hyphen")
		}
	}
	if c.ID == "." || c.ID == ".." {
		return errors.New("container id cannot be a dot path")
	}
	if len(c.ID) > 64 {
		return errors.New("container id cannot exceed the Linux hostname limit of 64 bytes")
	}
	if len(c.Hostname) > 64 || strings.ContainsRune(c.Hostname, '\x00') {
		return errors.New("hostname cannot exceed 64 bytes or contain NUL")
	}
	if c.RootFS == "" || c.RootFS[0] != '/' || c.RootFS == "/" {
		return errors.New("rootfs must be an absolute non-root path")
	}
	if len(c.Command) == 0 || c.Command[0] == "" {
		return errors.New("command must include an executable")
	}
	for _, arg := range c.Command {
		if strings.ContainsRune(arg, '\x00') {
			return errors.New("command arguments cannot contain NUL")
		}
	}
	for _, item := range c.Env {
		key, _, ok := strings.Cut(item, "=")
		if !ok || key == "" || strings.ContainsRune(key, '\x00') || strings.ContainsRune(item, '\x00') {
			return fmt.Errorf("invalid environment entry %q: expected KEY=value", item)
		}
	}
	if (c.CPUQuota == 0) != (c.CPUPeriod == 0) {
		return errors.New("cpu quota and period must be specified together")
	}
	if c.NetworkIP != "" {
		if !c.Network {
			return errors.New("network_ip requires networking to be enabled")
		}
		ip := net.ParseIP(c.NetworkIP).To4()
		if ip == nil || ip[0] != 10 || ip[1] != 0 || ip[2] != 0 || ip[3] < 2 || ip[3] > 254 {
			return errors.New("network_ip must be an available address in 10.0.0.2/24 through 10.0.0.254/24")
		}
	}
	if c.TTY && !c.Attach {
		return errors.New("tty requires attach mode")
	}
	return nil
}

// Child is a namespaced child held before executing its workload. Keeping it
// paused lets the host attach cgroups and configure networking first.
type Child interface {
	PID() int
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Resize(columns, rows uint16) error
	Exec(context.Context, Config) error
	Wait() error
	Close() error
}

// StartedProcess exposes the process identity and optional attached streams
// once the child is ready to exec its workload.
type StartedProcess struct {
	PID    int
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser
	Resize func(columns, rows uint16) error
}

// Backend isolates Linux-specific mechanisms from lifecycle coordination.
// Implementations should make each operation safe to retry during cleanup.
type Backend interface {
	StartPaused(context.Context, Config) (Child, error)
	AttachCgroup(context.Context, Config, int) (cleanup func() error, err error)
	ConfigureNetwork(context.Context, Config, int) (cleanup func() error, err error)
}

// Run prepares and executes a container, cleaning up acquired host resources
// in reverse order on setup failure and after the workload exits.
func Run(ctx context.Context, cfg Config, backend Backend) (runErr error) {
	return RunStarted(ctx, cfg, backend, nil)
}

// RunStarted runs a container and calls started after kernel setup has
// completed and the workload has been released. This lets the daemon return
// the host PID before waiting for the container's eventual exit.
func RunStarted(ctx context.Context, cfg Config, backend Backend, started func(StartedProcess)) (runErr error) {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if backend == nil {
		return errors.New("runtime backend is required")
	}
	child, err := backend.StartPaused(ctx, cfg)
	if err != nil {
		return fmt.Errorf("start isolated child: %w", err)
	}
	cleanups := []func() error{child.Close}
	defer func() { runErr = errors.Join(runErr, cleanupAll(cleanups)) }()

	if child.PID() <= 0 {
		return errors.New("backend returned an invalid child pid")
	}
	cgroupCleanup, err := backend.AttachCgroup(ctx, cfg, child.PID())
	if err != nil {
		return fmt.Errorf("attach child to cgroup: %w", err)
	}
	if cgroupCleanup != nil {
		cleanups = append([]func() error{cgroupCleanup}, cleanups...)
	}
	networkCleanup, err := backend.ConfigureNetwork(ctx, cfg, child.PID())
	if err != nil {
		return fmt.Errorf("configure container network: %w", err)
	}
	if networkCleanup != nil {
		cleanups = append([]func() error{networkCleanup}, cleanups...)
	}
	if err := child.Exec(ctx, cfg); err != nil {
		return fmt.Errorf("execute container workload: %w", err)
	}
	if started != nil {
		started(StartedProcess{PID: child.PID(), Stdin: child.Stdin(), Stdout: child.Stdout(), Stderr: child.Stderr(), Resize: child.Resize})
	}
	if err := child.Wait(); err != nil {
		return fmt.Errorf("wait for container workload: %w", err)
	}
	return nil
}

func cleanupAll(cleanups []func() error) error {
	var result error
	for _, cleanup := range cleanups {
		if cleanup != nil {
			result = errors.Join(result, cleanup())
		}
	}
	return result
}
