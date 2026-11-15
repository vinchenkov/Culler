package main

import (
	"path/filepath"

	"mc/repository"
	"mc/verbs"
)

// repositoryFileCommand returns one file of a Project repository at an exact
// commit. The dashboard reads the Project repository on the host, so the
// command runs on the host when other verbs delegate to the helper.
func repositoryFileCommand(args []string) (any, error) {
	if len(args) != 3 || !filepath.IsAbs(args[0]) || !repository.ValidCommit(args[1]) {
		return nil, verbs.Usagef("usage: mc __repository-file <workspace-root> <commit> <path>")
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "mc __repository-file"); err != nil {
		return nil, err
	}
	return verbs.RepositoryFile(args[0], args[1], args[2])
}
