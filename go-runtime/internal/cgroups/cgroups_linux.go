//go:build linux

package cgroups

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Limits struct {
	MemoryMax uint64
	CPUQuota  uint64
	CPUPeriod uint64
}

// Attach creates one cgroup v2 group, writes its limits, and moves pid into it.
// The caller must invoke the returned cleanup after the process exits.
func Attach(root, id string, limits Limits, pid int) (func() error, error) {
	if pid <= 0 {
		return nil, errors.New("cgroup process id must be positive")
	}
	if limits.CPUQuota == 0 != (limits.CPUPeriod == 0) {
		return nil, errors.New("cpu quota and period must be specified together")
	}
	if limits.MemoryMax == 0 && limits.CPUQuota == 0 {
		return func() error { return nil }, nil
	}
	group, cleanup, err := CreateGroup(root, id)
	if err != nil {
		return nil, err
	}
	if err := WriteLimits(group, limits, pid); err != nil {
		_ = cleanup()
		return nil, err
	}
	return cleanup, nil
}

// CreateGroup creates a unique cgroup directory and returns its path and
// cleanup function. The kernel populates control files in that directory.
func CreateGroup(root, id string) (string, func() error, error) {
	cleanRoot := filepath.Clean(root)
	if !filepath.IsAbs(root) || cleanRoot == "/" || cleanRoot == "/sys/fs/cgroup" {
		return "", nil, errors.New("cgroup root must be an absolute dedicated subtree, not the cgroup hierarchy root")
	}
	validID := id != "" && id != "." && id != ".."
	for _, char := range id {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.') {
			validID = false
			break
		}
	}
	if !validID {
		return "", nil, errors.New("cgroup id must be a non-empty path-safe value")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", nil, fmt.Errorf("create cgroup root: %w", err)
	}
	group := filepath.Join(root, id)
	if err := os.Mkdir(group, 0755); err != nil {
		return "", nil, fmt.Errorf("create cgroup %q: %w", id, err)
	}
	cleanup := func() error {
		if err := os.Remove(group); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove cgroup %q: %w", id, err)
		}
		return nil
	}
	return group, cleanup, nil
}

// RemoveEmptyGroups removes only direct child cgroups that contain no
// processes. It is intended for startup recovery after a forced daemon exit:
// populated groups may still belong to running containers and are left alone.
func RemoveEmptyGroups(root string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
		return errors.New("cgroup recovery root must be an absolute dedicated subtree")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve cgroup recovery root: %w", err)
	}
	if filepath.Clean(resolvedRoot) == "/sys/fs/cgroup" {
		return errors.New("cgroup recovery root must not be the cgroup hierarchy root")
	}
	entries, err := os.ReadDir(resolvedRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read cgroup recovery root: %w", err)
	}
	var result error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		group := filepath.Join(root, entry.Name())
		procs, readErr := os.ReadFile(filepath.Join(group, "cgroup.procs"))
		if errors.Is(readErr, os.ErrNotExist) {
			continue // Not a cgroup directory.
		}
		if readErr != nil {
			result = errors.Join(result, fmt.Errorf("inspect cgroup %q: %w", entry.Name(), readErr))
			continue
		}
		if len(strings.TrimSpace(string(procs))) != 0 {
			continue
		}
		if removeErr := os.Remove(group); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			result = errors.Join(result, fmt.Errorf("remove empty cgroup %q: %w", entry.Name(), removeErr))
		}
	}
	return result
}

// WriteLimits writes into an existing cgroup v2 control directory. It is also
// useful for checking limit formatting against a fake cgroup filesystem.
func WriteLimits(group string, limits Limits, pid int) error {
	if pid <= 0 {
		return errors.New("cgroup process id must be positive")
	}
	if (limits.CPUQuota == 0) != (limits.CPUPeriod == 0) {
		return errors.New("cpu quota and period must be specified together")
	}
	if limits.MemoryMax > 0 {
		if err := writeControl(filepath.Join(group, "memory.max"), fmt.Sprint(limits.MemoryMax)); err != nil {
			return err
		}
	}
	if limits.CPUQuota > 0 {
		if limits.CPUPeriod == 0 {
			return errors.New("cpu period must be positive when cpu quota is set")
		}
		value := fmt.Sprintf("%d %d", limits.CPUQuota, limits.CPUPeriod)
		if err := writeControl(filepath.Join(group, "cpu.max"), value); err != nil {
			return err
		}
	}
	return writeControl(filepath.Join(group, "cgroup.procs"), fmt.Sprint(pid))
}

func writeControl(path, value string) error {
	if err := os.WriteFile(path, []byte(value), 0200); err != nil {
		return fmt.Errorf("write cgroup control %s: %w", filepath.Base(path), err)
	}
	return nil
}
