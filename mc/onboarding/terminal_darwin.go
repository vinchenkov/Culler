//go:build darwin

package onboarding

import "golang.org/x/sys/unix"

func getTerminalState(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TIOCGETA)
}

func setTerminalState(fd int, state *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TIOCSETA, state)
}

// macOS cannot poll /dev/tty. Select supports the release launcher's terminal input.
func waitTerminalInput(fd, timeout int) (int, error) {
	var readable unix.FdSet
	if fd < 0 || fd >= len(readable.Bits)*32 {
		return 0, unix.EBADF
	}
	readable.Set(fd)
	deadline := unix.NsecToTimeval(int64(timeout) * 1000000)
	return unix.Select(fd+1, &readable, nil, nil, &deadline)
}
