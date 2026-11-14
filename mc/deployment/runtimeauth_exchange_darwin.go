//go:build darwin

package deployment

import "golang.org/x/sys/unix"

func atomicExchangeDirectories(left, right string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, left, unix.AT_FDCWD, right, unix.RENAME_SWAP)
}
