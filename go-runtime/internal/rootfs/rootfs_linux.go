//go:build linux

package rootfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Enter bind-mounts root as the new root, detaches the previous root, and
// mounts proc for the container's PID namespace.
func Enter(root string) error {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || root == "/" {
		return fmt.Errorf("rootfs must be an absolute directory other than /, got %q", root)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect rootfs: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("rootfs must be a real directory, not a symlink")
	}
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make mount propagation private: %w", err)
	}
	if err := syscall.Mount(root, root, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("bind mount rootfs: %w", err)
	}
	procPath := filepath.Join(root, "proc")
	if err := os.MkdirAll(procPath, 0555); err != nil {
		return fmt.Errorf("create proc mount point: %w", err)
	}
	procInfo, err := os.Lstat(procPath)
	if err != nil {
		return fmt.Errorf("inspect proc mount point: %w", err)
	}
	if !procInfo.IsDir() || procInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("proc mount point %q must be a real directory", procPath)
	}
	if err := syscall.Mount("proc", procPath, "proc", syscall.MS_NOSUID|syscall.MS_NOEXEC|syscall.MS_NODEV, ""); err != nil {
		return fmt.Errorf("mount procfs at %s (namespace setup requires mount permission): %w", procPath, err)
	}
	oldRoot, err := os.MkdirTemp(root, ".minidock-oldroot-")
	if err != nil {
		return fmt.Errorf("create old-root mount point: %w", err)
	}
	oldRootName := "/" + filepath.Base(oldRoot)
	if err := syscall.PivotRoot(root, oldRoot); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}
	if err := os.Chdir("/"); err != nil {
		return fmt.Errorf("change to container root: %w", err)
	}
	if err := syscall.Unmount(oldRootName, syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount old root: %w", err)
	}
	if err := os.Remove(oldRootName); err != nil {
		return fmt.Errorf("remove old-root mount point: %w", err)
	}
	return nil
}
