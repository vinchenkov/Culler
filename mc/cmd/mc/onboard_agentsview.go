package main

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"mc/deployment"
	"mc/verbs"
)

const onboardAgentsViewTimeout = 20 * time.Minute

// runOnboardAgentsView installs the pinned AgentsView executable at
// MC_HOME/bin/agentsview. It runs on the host because the download and the
// executable stay outside the helper.
func runOnboardAgentsView(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("mc __onboard agentsview")
	executable := fs.String("executable", "", "verified AgentsView executable to install")
	if err := parse(fs, args); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if *executable != "" && !filepath.IsAbs(*executable) {
		return writeVerbError(stdout, stderr, verbs.Usagef("mc __onboard agentsview --executable requires an absolute path"))
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(id, "mc __onboard agentsview"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	pin, err := onboardAgentsViewPin()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("%v", err))
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("%v", err))
	}
	var status, path string
	if *executable != "" {
		status, path, err = deployment.InstallAgentsViewExecutable(home, *executable, pin)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), onboardAgentsViewTimeout)
		defer cancel()
		status, path, err = deployment.EnsureAgentsView(ctx, home, pin, deployment.AgentsViewDownload{})
	}
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("%v", err))
	}
	if err := writeJSON(stdout, map[string]any{"status": status, "version": pin.Version, "path": path}); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}
