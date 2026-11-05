package repository

import (
	"context"
	"fmt"
	"strings"
)

// TreeID returns the tree object at one path of a commit, or an empty string
// when the commit has no tree at that path.
func (g Git) TreeID(ctx context.Context, commit, name string) (string, error) {
	if !ValidCommit(commit) || !validRepositoryPath(name) || name == ".." {
		return "", fmt.Errorf("the tree read requires an exact commit and a relative path")
	}
	body, err := g.command(ctx, nil, "ls-tree", "-z", commit, "--", name)
	if err != nil {
		return "", err
	}
	for _, record := range strings.Split(string(body), "\x00") {
		if record == "" {
			continue
		}
		header, entry, ok := strings.Cut(record, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 {
			return "", fmt.Errorf("the repository tree contains an invalid entry")
		}
		if entry != name {
			continue
		}
		if fields[1] != "tree" {
			return "", nil
		}
		if !ValidCommit(fields[2]) {
			return "", fmt.Errorf("the repository tree contains an invalid object")
		}
		return fields[2], nil
	}
	return "", nil
}
