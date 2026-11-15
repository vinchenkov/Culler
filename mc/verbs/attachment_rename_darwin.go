//go:build darwin

package verbs

import "golang.org/x/sys/unix"

func attachmentRenameNoReplace(oldDir int, oldName string, newDir int, newName string) error {
	return unix.RenameatxNp(oldDir, oldName, newDir, newName, unix.RENAME_EXCL)
}
