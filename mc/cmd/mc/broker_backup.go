package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"mc/verbs"
)

// brokerBackupVia is the ONE spine-snapshot crossing, shared by every build.
//
// The direction is the whole point and it is easy to get backwards: the helper
// only STREAMS bytes out of the lock domain through `mc __backup-spine`, and the
// HOST writes the snapshot into its own MC_HOME/backups. It must be that way
// round — `requireSecureBackupDir` (verbs/spine_transfer.go) requires the
// backups directory to be owned by `os.Getuid()`, and inside the helper `mc`
// runs setuid (uid 10001) while its real uid is the container user, so an
// in-helper write can never satisfy that check at mode 0700.
//
// This function exists because the build tags previously chose between two
// DIFFERENT crossings: production streamed and wrote host-side, while
// `test_fake_routing` substituted a bare `delegate()` that ran the whole verb
// inside the helper. A routing tag silently changing where a spine snapshot is
// written is not a difference any caller could see, and it took down every
// docker_e2e test that starts a resident the moment the resident's startup
// snapshot became fatal — the lane was red for two weeks
// . The two builds now differ
// only in how they RESOLVE the helper name, which is genuinely environmental.
func brokerBackupVia(helper string, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc backup"))
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(id, "mc backup"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	req, home, err := verbs.PrepareSpineTransfer(releaseBuildID, gatewayControlVersion, configSchemaVersion)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	frame, err := json.Marshal(req)
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("encode private backup frame: %v", err))
	}
	cmd := exec.Command("docker", "exec", "-i", helper, "mc", "__backup-spine")
	cmd.Stdin = bytes.NewReader(frame)
	privateErr := boundedDoctorBuffer{limit: privateDoctorOutputLimit}
	cmd.Stderr = &privateErr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("open private backup stream: %v", err))
	}
	if err := cmd.Start(); err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("start private backup crossing: %v", err))
	}
	snapshot, size, receiveErr := verbs.PublishSpineBackup(home, req, pipe, time.Now())
	if receiveErr != nil {
		_, _ = io.Copy(io.Discard, pipe)
	}
	waitErr := cmd.Wait()
	if privateErr.Len() > 0 {
		_, _ = stderr.Write(privateErr.Bytes())
	}
	if privateErr.overflow {
		return writeVerbError(stdout, stderr, verbs.Usagef("private backup diagnostics exceeded their bound"))
	}
	if waitErr != nil {
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			return writeVerbError(stdout, stderr, verbs.Domainf("private backup refused with exit %d", exit.ExitCode()))
		}
		return writeVerbError(stdout, stderr, verbs.Usagef("private backup crossing failed: %v", waitErr))
	}
	if receiveErr != nil {
		return writeVerbError(stdout, stderr, receiveErr)
	}
	if err := writeJSON(stdout, map[string]any{"snapshot": snapshot, "bytes": size}); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}
