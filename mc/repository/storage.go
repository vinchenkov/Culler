package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// RequiredBytes includes the selected history and one uncompressed checkout.
func (g Git) RequiredBytes(ctx context.Context, commits ...string) (int64, error) {
	for _, commit := range commits {
		if !ValidCommit(commit) {
			return 0, fmt.Errorf("the storage estimate requires exact commits")
		}
	}
	if len(commits) == 0 {
		return 0, fmt.Errorf("the storage estimate requires an input commit")
	}
	objects, err := g.command(ctx, nil, append([]string{"rev-list", "--objects", "--no-object-names"}, commits...)...)
	if err != nil {
		return 0, err
	}
	sizes, err := g.command(ctx, objects, "cat-file", "--batch-check=%(objectsize)")
	if err != nil {
		return 0, err
	}
	total := int64(1 << 20)
	for _, size := range strings.Fields(string(sizes)) {
		n, err := strconv.ParseInt(size, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("the repository object size is invalid")
		}
		total += n + 128
	}
	tree, err := g.command(ctx, nil, "ls-tree", "-r", "-l", "-z", commits[0])
	if err != nil {
		return 0, err
	}
	for _, entry := range strings.Split(string(tree), "\x00") {
		header, _, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(header)
		if len(fields) != 4 || fields[3] == "-" {
			continue
		}
		n, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return 0, err
		}
		total += n + 4096
	}
	return total, nil
}

func RequireFreeSpace(path string, bytes int64) error {
	for {
		if _, err := os.Stat(path); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return fmt.Errorf("the temporary filesystem is unavailable")
		}
		path = parent
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return err
	}
	available := uint64(stat.Bavail) * uint64(stat.Bsize)
	if bytes < 0 || uint64(bytes) > available {
		return fmt.Errorf("the temporary filesystem requires %d free bytes; %d bytes remain", bytes, available)
	}
	return nil
}
