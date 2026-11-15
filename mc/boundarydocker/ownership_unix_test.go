//go:build docker_boundary

package boundarydocker

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"testing"
)

// assertOwnedByInvoker checks that a file written from inside a container by
// uid 10002 arrives on the host owned by the OPERATOR who ran the test.
//
// This is the surprising half of the final-uid canary, and the reason it is
// asserted rather than assumed: on Docker Desktop's VirtioFS share the host
// inode is created with the sharing user's ownership, NOT the container uid.
// That is what lets an apply merge into an operator repository and leave
// operator-owned bytes behind with nothing chowning anything.
//
// If this ever flips to 10002, the operator ends up with unowned files in their
// own repository, and the design's no-permission-widening posture stops being
// sufficient on its own.
func assertOwnedByInvoker(t *testing.T, fi fs.FileInfo) error {
	t.Helper()
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("no stat information for %s", fi.Name())
	}
	if want := os.Getuid(); int(st.Uid) != want {
		return fmt.Errorf("file is owned by uid %d, want the operator's %d — "+
			"the container uid reached the host inode, so an apply would leave "+
			"files the operator does not own inside their own repository", st.Uid, want)
	}
	return nil
}
