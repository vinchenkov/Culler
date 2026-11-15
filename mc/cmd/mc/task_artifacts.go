package main

import (
	"path/filepath"
	"strconv"

	"mc/verbs"
)

// taskArtifactsCommand resolves the read-only bind of the artifact files of
// one task for the Collie container of a packet conversation. It runs on the
// host, because the helper container sees neither the mount allowlist nor the
// artifact files.
func taskArtifactsCommand(args []string) (any, error) {
	usage := verbs.Usagef("usage: mc __task-artifacts <workspace-root> <task>")
	if len(args) != 2 || !filepath.IsAbs(args[0]) {
		return nil, usage
	}
	task, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || task < 1 {
		return nil, usage
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "mc __task-artifacts"); err != nil {
		return nil, err
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return nil, verbs.Usagef("%v", err)
	}
	return verbs.TaskArtifactMount(home, args[0], task)
}
