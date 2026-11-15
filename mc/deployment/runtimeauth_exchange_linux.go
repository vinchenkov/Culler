//go:build linux

package deployment

import "golang.org/x/sys/unix"

func atomicExchangeDirectories(left, right string) error {
	return unix.Renameat2(unix.AT_FDCWD, left, unix.AT_FDCWD, right, unix.RENAME_EXCHANGE)
}
