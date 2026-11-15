//go:build test_fake_routing

package main

import "io"

// The test build resolves the helper from the environment (MC_HELPER) instead of
// deriving production names from the canonical home, and otherwise takes the SAME
// crossing production does — see brokerBackupVia.
//
// It previously took a bare `delegate()` here, which ran the whole `mc backup`
// verb inside the helper and wrote the snapshot under the helper's MC_HOME. That
// cannot work: `requireSecureBackupDir` wants the backups dir owned by
// `os.Getuid()`, and in-helper `mc` is setuid, so the real and effective uids
// differ and no mount arrangement satisfies it. Nothing surfaced it until the
// resident's startup snapshot became fatal, and then every docker_e2e test that
// starts a resident timed out.
func brokerBackup(args []string, stdout, stderr io.Writer) int {
	return brokerBackupVia(helperContainerName(), args, stdout, stderr)
}

func brokerReset(args []string, stdout, stderr io.Writer) int {
	return delegate(args, nil, stdout, stderr)
}
