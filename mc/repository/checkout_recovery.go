package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type treeFile struct{ mode, object string }

func treeFiles(ctx context.Context, g Git, commit string) (map[string]treeFile, error) {
	body, err := g.command(ctx, nil, "ls-tree", "-r", "-z", commit)
	if err != nil {
		return nil, err
	}
	files := map[string]treeFile{}
	for _, entry := range strings.Split(string(body), "\x00") {
		header, path, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("the commit tree entry is invalid")
		}
		files[path] = treeFile{fields[0], fields[2]}
	}
	return files, nil
}

// indexFiles reads the entries of the index of g.
func indexFiles(ctx context.Context, g Git) (map[string]treeFile, error) {
	body, err := g.command(ctx, nil, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	files := map[string]treeFile{}
	for _, entry := range strings.Split(string(body), "\x00") {
		header, path, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("the index entry is invalid")
		}
		files[path] = treeFile{fields[0], fields[1]}
	}
	return files, nil
}

// worktreeFile reads the mode and object of one path in the operator
// checkout. A missing path returns the zero value.
func worktreeFile(ctx context.Context, g Git, path string) (treeFile, error) {
	info, err := os.Lstat(filepath.Join(g.Root, path))
	if os.IsNotExist(err) {
		return treeFile{}, nil
	}
	if err != nil {
		return treeFile{}, err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		link, err := os.Readlink(filepath.Join(g.Root, path))
		if err != nil {
			return treeFile{}, err
		}
		body, err := g.command(ctx, []byte(link), "hash-object", "--stdin")
		if err != nil {
			return treeFile{}, err
		}
		return treeFile{"120000", strings.TrimSpace(string(body))}, nil
	case info.Mode().IsRegular():
		object, err := g.output(ctx, "hash-object", "--no-filters", "--", path)
		if err != nil {
			return treeFile{}, err
		}
		mode := "100644"
		if info.Mode()&0111 != 0 {
			mode = "100755"
		}
		return treeFile{mode, object}, nil
	}
	return treeFile{"directory", ""}, nil
}

// resumeCheckout prepares the index of g to finish an interrupted update.
// The interrupted update left each changed path at its destination version or
// at its candidate version. Each path at its candidate version takes the
// candidate entry, so the two-way merge updates only the remaining paths.
func resumeCheckout(ctx context.Context, g Git, c Candidate) error {
	prior, err := treeFiles(ctx, g, c.DestinationCommit)
	if err != nil {
		return err
	}
	next, err := treeFiles(ctx, g, c.Commit)
	if err != nil {
		return err
	}
	staged, err := indexFiles(ctx, g)
	if err != nil {
		return err
	}
	changed, err := g.ChangedPaths(ctx, c.DestinationCommit, c.Commit)
	if err != nil {
		return err
	}
	var edited []string
	for _, path := range changed {
		file, err := worktreeFile(ctx, g, path)
		if err != nil {
			return err
		}
		switch {
		case staged[path] != prior[path] && staged[path] != next[path]:
			edited = append(edited, path)
		case file == next[path]:
			if err := takeEntry(ctx, g, next, path); err != nil {
				return err
			}
		case file != prior[path]:
			edited = append(edited, path)
		}
	}
	if len(edited) > 0 {
		return refuse("An interrupted merge into %s found local changes: %s. Discard each of these changes.",
			c.DestinationBranch, strings.Join(edited, ", "))
	}
	_, err = g.output(ctx, "update-index", "-q", "--refresh")
	return err
}
