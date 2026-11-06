//go:build darwin || linux

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

func terminalFile(file *os.File) bool {
	if file == nil {
		return false
	}
	_, err := getTerminalState(int(file.Fd()))
	return err == nil
}

func terminalWidth(file *os.File) int {
	if file == nil {
		return 0
	}
	size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0
	}
	return int(size.Col)
}

func (t *Terminal) openKeys() (*keyReader, error) {
	keys := &keyReader{terminal: t}
	if t.input == nil {
		return keys, nil
	}
	fd := int(t.input.Fd())
	original, err := getTerminalState(fd)
	if err != nil {
		return nil, fmt.Errorf("read the terminal controls: %w", err)
	}
	changed := *original
	changed.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	changed.Cflag &^= unix.CSIZE | unix.PARENB
	changed.Cflag |= unix.CS8
	changed.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.IEXTEN | unix.ISIG
	changed.Cc[unix.VMIN] = 1
	changed.Cc[unix.VTIME] = 0
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	if err := setTerminalState(fd, &changed); err != nil {
		signal.Stop(signals)
		return nil, fmt.Errorf("set the terminal controls: %w", err)
	}
	keys.signals = signals
	keys.stop = func() { signal.Stop(signals) }
	keys.restore = func() error {
		if err := setTerminalState(fd, original); err != nil {
			return fmt.Errorf("restore the terminal controls: %w", err)
		}
		return nil
	}
	return keys, nil
}

func (k *keyReader) readByte(escape bool) (byte, error) {
	for {
		select {
		case <-k.signals:
			return 0, errors.Join(ErrExit, context.Canceled)
		default:
		}
		if !escape && k.refresh != nil {
			k.refresh()
		}
		if k.terminal.input == nil || k.terminal.in.Buffered() > 0 {
			return k.terminal.in.ReadByte()
		}
		timeout := 100
		if escape {
			timeout = 40
		}
		count, err := waitTerminalInput(int(k.terminal.input.Fd()), timeout)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("read the terminal input: %w", err)
		}
		if count == 0 {
			if escape {
				return 0, io.EOF
			}
			continue
		}
		return k.terminal.in.ReadByte()
	}
}
