//go:build linux

package onboarding

import "golang.org/x/sys/unix"

func getTerminalState(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TCGETS)
}

func setTerminalState(fd int, state *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TCSETS, state)
}

func waitTerminalInput(fd, timeout int) (int, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	count, err := unix.Poll(fds, timeout)
	if err == nil && fds[0].Revents&unix.POLLNVAL != 0 {
		return 0, unix.EBADF
	}
	return count, err
}
