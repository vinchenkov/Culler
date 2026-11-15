//go:build !darwin

package boundary

import "io/fs"

// The native ACL leg is macOS-specific. Non-Darwin builds retain the owner and
// group/other mode predicate.
func trustedNoGrantingACL(string, fs.FileInfo, int) error { return nil }
