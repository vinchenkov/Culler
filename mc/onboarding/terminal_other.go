//go:build !darwin && !linux

package onboarding

import "os"

func terminalFile(*os.File) bool { return false }
func terminalWidth(*os.File) int { return 0 }

func (t *Terminal) openKeys() (*keyReader, error) {
	return &keyReader{terminal: t}, nil
}

func (k *keyReader) readByte(bool) (byte, error) {
	return k.terminal.in.ReadByte()
}
