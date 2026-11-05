//go:build darwin && !test_fake_routing

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func hiddenInputIsTerminal(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA)
	return err == nil
}

func readHiddenTerminalLine(file *os.File) (body []byte, retErr error) {
	fd := int(file.Fd())
	original, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, err
	}
	hidden := *original
	hidden.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &hidden); err != nil {
		return nil, err
	}
	defer func() {
		if restoreErr := unix.IoctlSetTermios(fd, unix.TIOCSETA, original); restoreErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("restore terminal echo: %w", restoreErr))
		}
	}()
	return bufio.NewReader(file).ReadBytes('\n')
}
