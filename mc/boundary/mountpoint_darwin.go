//go:build darwin

package boundary

import (
	"syscall"
)

// mountPoint returns the mount point of the volume hosting path — the kernel's
// own answer, via statfs(2)'s f_mntonname.
//
// This is the mechanism for the alias routes that a filepath.Dir walk cannot
// reach. /Users is an APFS FIRMLINK, not a symlink: lstat reports a plain
// directory, ModeSymlink is clear, and filepath.EvalSymlinks neither sees through
// it nor normalizes it. So HOME has a second, fully live ancestor chain that no
// Dir walk produces, and os.SameFile catches only the aliases of chain members —
// not the volume mount point itself, which is an alias of nothing in the chain.
//
// Measured on the development machine: statfs("/Users").f_mntonname is
// "/System/Volumes/Data", the root that a Dir-walk broad_root misses and through
// which the whole of HOME (including ~/.ssh) is readable.
//
// statfs is used rather than parsing /usr/share/firmlinks or hardcoding
// "/System/Volumes/Data": it is the kernel's own mount table, it needs no
// macOS-specific literal, and it stays correct for a HOME on an external volume
// or a relocated non-/Users HOME.
func mountPoint(path string) (string, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "", err
	}
	b := make([]byte, 0, len(st.Mntonname))
	for _, c := range st.Mntonname {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b), nil
}
