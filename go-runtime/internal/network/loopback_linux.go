//go:build linux

package network

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// BringLoopbackUp enables lo inside the calling process's network namespace.
func BringLoopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open network control socket: %w", err)
	}
	defer unix.Close(fd)
	request, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, request); err != nil {
		return fmt.Errorf("read loopback flags: %w", err)
	}
	flags := request.Uint16()
	if flags&unix.IFF_UP != 0 {
		return nil
	}
	request.SetUint16(flags | unix.IFF_UP)
	if err := unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, request); err != nil {
		return fmt.Errorf("bring loopback up: %w", err)
	}
	return nil
}
