package repository

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
)

type TreeFile struct {
	Path   string
	Mode   string
	Object string
	Size   int64
}

// ErrTreeEntry reports a tree entry whose path holds a backslash or a line break.
var ErrTreeEntry = errors.New("the repository tree contains an invalid entry")

// Tree reads immutable Git entries without a checkout or a content filter.
func (g Git) Tree(ctx context.Context, commit, prefix string) ([]TreeFile, error) {
	if !ValidCommit(commit) || (prefix != "" && !validRepositoryPath(prefix)) {
		return nil, fmt.Errorf("the repository read requires an exact commit and a relative path")
	}
	args := []string{"ls-tree", "-r", "-l", "-z", commit, "--"}
	if prefix != "" {
		args = append(args, prefix)
	}
	body, err := g.command(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	var files []TreeFile
	for _, record := range strings.Split(string(body), "\x00") {
		if record == "" {
			continue
		}
		header, name, ok := strings.Cut(record, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 4 || !validRepositoryPath(name) {
			return nil, ErrTreeEntry
		}
		size := int64(0)
		if fields[3] != "-" {
			size, err = strconv.ParseInt(fields[3], 10, 64)
			if err != nil || size < 0 {
				return nil, fmt.Errorf("the repository tree contains an invalid size")
			}
		}
		files = append(files, TreeFile{Path: name, Mode: fields[0], Object: fields[2], Size: size})
	}
	return files, nil
}

func (g Git) ReadTreeFile(ctx context.Context, file TreeFile, limit int64) ([]byte, error) {
	if (file.Mode != "100644" && file.Mode != "100755") || !ValidCommit(file.Object) {
		return nil, fmt.Errorf("the repository file must contain regular file data: %s", file.Path)
	}
	if file.Size > limit {
		return nil, fmt.Errorf("the repository file exceeds the byte limit: %s", file.Path)
	}
	body, err := g.command(ctx, nil, "cat-file", "blob", file.Object)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != file.Size {
		return nil, fmt.Errorf("the repository file size changed")
	}
	return body, nil
}

// ReadFile reads the regular file at a path of an exact commit.
func (g Git) ReadFile(ctx context.Context, commit, name string, limit int64) ([]byte, error) {
	if !validRepositoryPath(name) {
		return nil, fmt.Errorf("the repository read requires an exact commit and a relative path")
	}
	entries, err := g.Tree(ctx, commit, name)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Path == name {
			return g.ReadTreeFile(ctx, entry, limit)
		}
	}
	return nil, fmt.Errorf("the commit holds no file at %s", name)
}

func (g Git) ChangedPaths(ctx context.Context, before, after string) ([]string, error) {
	if !ValidCommit(before) || !ValidCommit(after) {
		return nil, fmt.Errorf("the repository comparison requires exact commits")
	}
	body, err := g.command(ctx, nil, "diff", "--name-only", "--no-renames", "--ignore-submodules=none", "-z", before, after, "--")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, name := range strings.Split(string(body), "\x00") {
		if name != "" {
			paths = append(paths, name)
		}
	}
	return paths, nil
}

func validRepositoryPath(name string) bool {
	return name != "" && name != "." && !strings.HasPrefix(name, "/") &&
		!strings.HasPrefix(name, "../") && path.Clean(name) == name && !strings.ContainsAny(name, "\x00\\\r\n")
}
