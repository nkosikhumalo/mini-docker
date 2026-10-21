//go:build linux

package isolation

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestPTYCreationAndResize(t *testing.T) {
	pty, err := openPTY()
	if err != nil {
		t.Skipf("PTY device is unavailable: %v", err)
	}
	defer pty.Close()
	if err := pty.Resize(100, 40); err != nil {
		t.Fatal(err)
	}
	size, err := unix.IoctlGetWinsize(int(pty.master.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		t.Fatal(err)
	}
	if size.Col != 100 || size.Row != 40 {
		t.Fatalf("PTY size is %dx%d, want 100x40", size.Col, size.Row)
	}
}
