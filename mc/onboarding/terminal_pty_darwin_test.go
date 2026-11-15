//go:build darwin

package onboarding

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func testTerminalPair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	for _, request := range []uint{unix.TIOCPTYGRANT, unix.TIOCPTYUNLK} {
		if _, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), uintptr(request), 0); errno != 0 {
			t.Fatal(errno)
		}
	}
	var name [128]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		t.Fatal(errno)
	}
	slave, err := os.OpenFile(unix.ByteSliceToString(name[:]), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

func TestReleaseTerminalInput(t *testing.T) {
	if os.Getenv("CULLER_TEST_RELEASE_TERMINAL") == "1" {
		terminal := NewTerminal(os.Stdin, os.Stdout)
		if !terminal.HasTerminal() {
			t.Fatal("the release input requires a terminal")
		}
		value, err := terminal.Choose("Release input", []Choice{{ID: "first", Label: "First"}, {ID: "second", Label: "Second"}}, 0)
		if err != nil || value != "second" {
			t.Fatalf("the release input returned %q, %v", value, err)
		}
		return
	}
	master, slave := testTerminalPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The shell opens /dev/tty, as the public release launcher does.
	cmd := exec.CommandContext(ctx, "sh", "-c", `exec "$1" -test.run '^TestReleaseTerminalInput$' </dev/tty`, "sh", os.Args[0])
	cmd.Env = append(os.Environ(), "CULLER_TEST_RELEASE_TERMINAL=1", "TERM=xterm")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	readTerminalUntil(t, master, "q exit")
	if _, err := master.Write([]byte("\x1b[B\r")); err != nil {
		t.Fatal(err)
	}
	readTerminalUntil(t, master, "PASS")
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the release terminal failed: %v", err)
	}
}
