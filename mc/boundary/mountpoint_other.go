//go:build !darwin

package boundary

// mountPoint is the alias-route mechanism, and it is macOS-specific by nature:
// the thing it exists to defeat is the APFS firmlink at /Users, which presents a
// second live ancestor chain that filepath.Dir never walks.
//
// Elsewhere the rule degrades to a no-op, which is the correct answer rather
// than a missing feature: without firmlinks, a volume's mount point is already a
// member of the path's own Dir chain, so the union of the chain and its alias
// routes is just the chain. Callers treat the error as "no additional routes",
// never as "fail closed" — on this platform there is nothing to fail closed
// about.
func mountPoint(string) (string, error) { return "", errNoMountPoint }
