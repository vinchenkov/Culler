package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

type applicationJournal struct {
	Application Application `json:"application"`
	IndexDevice uint64      `json:"index_device"`
	IndexInode  uint64      `json:"index_inode"`
}

func readJournal(path string, a Application) (*applicationJournal, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var journal applicationJournal
	if err := json.Unmarshal(body, &journal); err != nil {
		return nil, err
	}
	actual, err := json.Marshal(journal.Application)
	if err != nil {
		return nil, err
	}
	expected, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(actual, expected) {
		return nil, fmt.Errorf("the update journal identifies another authorization")
	}
	return &journal, nil
}

func indexIdentity(path string) (uint64, uint64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return 0, 0, fmt.Errorf("the reserved index is not a regular file")
	}
	return uint64(stat.Dev), stat.Ino, nil
}

func (j applicationJournal) owns(path string) bool {
	device, inode, err := indexIdentity(path)
	return err == nil && device == j.IndexDevice && inode == j.IndexInode
}

func writeJournal(path string, j applicationJournal) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	body, err := json.Marshal(j)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".application-")
	if err != nil {
		return err
	}
	defer file.Close()
	defer os.Remove(file.Name())
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := os.Link(file.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func recoverAppliedIndex(ctx context.Context, g Git, common, path string, j *applicationJournal) error {
	if j == nil {
		return nil
	}
	if j.IndexInode == 0 {
		return os.Remove(path)
	}
	lock := filepath.Join(common, "index.lock")
	if _, err := os.Lstat(lock); os.IsNotExist(err) {
		return os.Remove(path)
	}
	if !j.owns(lock) {
		return fmt.Errorf("the index reservation belongs to another operation")
	}
	actual, err := indexTree(ctx, g, lock)
	if err != nil {
		return err
	}
	// The reserved index keeps the operator entries and holds the candidate
	// entry of each path that the update changed.
	c := j.Application.Candidate
	changed, err := g.ChangedPaths(ctx, c.DestinationCommit, c.Commit)
	if err != nil {
		return err
	}
	different, err := g.command(ctx, nil, "diff-tree", "-r", "--no-renames", "--name-only", "-z", actual, c.Commit)
	if err != nil {
		return err
	}
	for _, path := range strings.Split(string(different), "\x00") {
		if path != "" && slices.Contains(changed, path) {
			return fmt.Errorf("the reserved index differs from the applied commit")
		}
	}
	if err := os.Rename(lock, filepath.Join(common, "index")); err != nil {
		return err
	}
	if err := syncDirectory(common); err != nil {
		return err
	}
	return os.Remove(path)
}

func indexTree(ctx context.Context, g Git, path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(g.directory, "index-read-")
	if err != nil {
		return "", err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(body); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	g.index = name
	return g.output(ctx, "write-tree")
}
