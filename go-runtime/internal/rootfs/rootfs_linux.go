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
	if err := syscall.Mount("", root, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_NOSUID|syscall.MS_NODEV, ""); err != nil {
		return fmt.Errorf("restrict rootfs mount flags: %w", err)
	}
	if err := setupDevices(root); err != nil {
		return fmt.Errorf("set up container devices: %w", err)
	}
	procPath := filepath.Join(root, "proc")
	procInfo, err := os.Lstat(procPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(procPath, 0555); err != nil {
			return fmt.Errorf("create proc mount point: %w", err)
		}
		procInfo, err = os.Lstat(procPath)
	}
	if err != nil || !procInfo.IsDir() || procInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("proc mount point %q must be a real directory", procPath)
	}
	if err := syscall.Mount("proc", procPath, "proc", syscall.MS_NOSUID|syscall.MS_NOEXEC|syscall.MS_NODEV|syscall.MS_RDONLY, ""); err != nil {
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

func setupDevices(root string) error {
	dev := filepath.Join(root, "dev")
	info, err := os.Lstat(dev)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(dev, 0755); err != nil {
			return err
		}
		info, err = os.Lstat(dev)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("/dev must be a real directory")
	}
	if err := syscall.Mount("tmpfs", dev, "tmpfs", syscall.MS_NOSUID|syscall.MS_NOEXEC, "mode=755,size=1m"); err != nil {
		return fmt.Errorf("mount /dev tmpfs: %w", err)
	}
	devices := []struct{ name string }{{"null"}, {"zero"}, {"full"}, {"random"}, {"urandom"}, {"tty"}}
	for _, entry := range devices {
		placeholder := filepath.Join(dev, entry.name)
		file, err := os.OpenFile(placeholder, os.O_CREATE|os.O_RDWR, 0666)
		if err != nil {
			return fmt.Errorf("create /dev/%s mount point: %w", entry.name, err)
		}
		if err := file.Close(); err != nil {
			return err
		}
		device := filepath.Join("/dev", entry.name)
		deviceInfo, err := os.Stat(device)
		if err != nil || deviceInfo.Mode()&os.ModeCharDevice == 0 {
			return fmt.Errorf("host device %s is unavailable", device)
		}
		if err := syscall.Mount(device, placeholder, "", syscall.MS_BIND, ""); err != nil {
			return fmt.Errorf("bind mount %s: %w", device, err)
		}
	}
	links := []struct{ name, target string }{
		{"fd", "/proc/self/fd"}, {"stdin", "/proc/self/fd/0"},
		{"stdout", "/proc/self/fd/1"}, {"stderr", "/proc/self/fd/2"},
	}
	for _, link := range links {
		if err := os.Symlink(link.target, filepath.Join(dev, link.name)); err != nil {
			return fmt.Errorf("create /dev/%s link: %w", link.name, err)
		}
	}
	pts := filepath.Join(dev, "pts")
	if err := os.Mkdir(pts, 0755); err != nil {
		return fmt.Errorf("create /dev/pts: %w", err)
	}
	if err := syscall.Mount("devpts", pts, "devpts", syscall.MS_NOSUID|syscall.MS_NOEXEC, "newinstance,ptmxmode=0666,mode=0620"); err != nil {
		return fmt.Errorf("mount devpts: %w", err)
	}
	if err := os.Symlink("pts/ptmx", filepath.Join(dev, "ptmx")); err != nil {
		return fmt.Errorf("create /dev/ptmx link: %w", err)
	}
	shm := filepath.Join(dev, "shm")
	if err := os.Mkdir(shm, 01777); err != nil {
		return fmt.Errorf("create /dev/shm: %w", err)
	}
	if err := syscall.Mount("tmpfs", shm, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, "mode=1777,size=64m"); err != nil {
		return fmt.Errorf("mount /dev/shm: %w", err)
	}
	return nil
}
