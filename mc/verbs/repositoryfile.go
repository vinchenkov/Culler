package verbs

import (
	"context"
	"time"
	"unicode/utf8"

	"mc/repository"
)

// RepositoryFileLimit is the largest file that RepositoryFile returns.
const RepositoryFileLimit = 1 << 20

// RepositoryFile returns the text of the regular file at a path of an exact
// commit of the repository at root. The Files changed view reads the two
// versions of a changed file to show its unmodified lines.
func RepositoryFile(root, commit, name string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body, err := repository.Git{Root: root}.ReadFile(ctx, commit, name, RepositoryFileLimit)
	if err != nil {
		return nil, Domainf("%v", err)
	}
	if !utf8.Valid(body) {
		return nil, Domainf("the repository file is not UTF-8 text: %s", name)
	}
	return map[string]any{"commit": commit, "path": name, "size": len(body), "content": string(body)}, nil
}
