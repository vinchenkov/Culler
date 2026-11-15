package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// ChangeFile is one entry of the file inventory of a Git comparison. The patch
// of the entry is the byte range from PatchOffset of the comparison patch.
type ChangeFile struct {
	Status       string `json:"status"`
	Path         string `json:"path"`
	PreviousPath string `json:"previous_path,omitempty"`
	OldMode      string `json:"old_mode,omitempty"`
	NewMode      string `json:"new_mode,omitempty"`
	Similarity   int    `json:"similarity,omitempty"`
	Additions    int64  `json:"additions"`
	Deletions    int64  `json:"deletions"`
	Binary       bool   `json:"binary"`
	TextDiff     bool   `json:"text_diff"`
	PatchOffset  int64  `json:"patch_offset"`
	PatchBytes   int64  `json:"patch_bytes"`
}

// Comparison is the file inventory of the Git range from FromCommit to
// ToCommit, with the size and digest of its complete patch.
type Comparison struct {
	FromCommit  string       `json:"from_commit"`
	ToCommit    string       `json:"to_commit"`
	PatchBytes  int64        `json:"patch_bytes"`
	PatchSHA256 string       `json:"patch_sha256"`
	Files       []ChangeFile `json:"files"`
}

var changeStatuses = map[byte]string{
	'A': "added", 'C': "copied", 'D': "deleted", 'M': "modified", 'R': "renamed", 'T': "type-changed",
}

// Repository configuration cannot change the comparison output, so every
// reader of the stored patch sees the same format.
var comparisonOptions = []string{
	"-c", "diff.suppressBlankEmpty=false",
	"diff", "-O/dev/null", "--no-color", "--no-ext-diff", "--no-textconv", "--no-relative",
	"--find-renames", "--ignore-submodules=none", "--diff-algorithm=myers",
}

// Compare derives the complete file inventory and patch of one commit range.
// Each inventory entry holds the location of its own part of the patch.
func (g Git) Compare(ctx context.Context, from, to string) (Comparison, []byte, error) {
	c := Comparison{FromCommit: from, ToCommit: to, Files: []ChangeFile{}}
	if !ValidCommit(from) || !ValidCommit(to) {
		return c, nil, fmt.Errorf("the repository comparison requires exact commits")
	}
	command := func(args ...string) ([]byte, error) {
		return g.command(ctx, nil, append(append(append([]string{}, comparisonOptions...), args...), from, to, "--")...)
	}
	raw, err := command("--raw", "-z", "--no-abbrev")
	if err != nil {
		return c, nil, err
	}
	numstat, err := command("--numstat", "-z")
	if err != nil {
		return c, nil, err
	}
	patch, err := command("--patch", "--src-prefix=a/", "--dst-prefix=b/", "--submodule=short", "--unified=3")
	if err != nil {
		return c, nil, err
	}
	if c.Files, err = parseRawChanges(raw); err != nil {
		return c, nil, err
	}
	if err := addLineCounts(c.Files, numstat); err != nil {
		return c, nil, err
	}
	if err := addPatchRanges(c.Files, patch); err != nil {
		return c, nil, err
	}
	sum := sha256.Sum256(patch)
	c.PatchBytes, c.PatchSHA256 = int64(len(patch)), hex.EncodeToString(sum[:])
	return c, patch, nil
}

func parseRawChanges(raw []byte) ([]ChangeFile, error) {
	fields := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	files := []ChangeFile{}
	if len(raw) == 0 {
		return files, nil
	}
	for i := 0; i < len(fields); {
		header := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		if !strings.HasPrefix(fields[i], ":") || len(header) != 5 || header[4] == "" {
			return nil, fmt.Errorf("the Git comparison contains an invalid entry")
		}
		status, ok := changeStatuses[header[4][0]]
		if !ok {
			return nil, fmt.Errorf("the Git comparison contains an unknown change: %s", header[4])
		}
		file := ChangeFile{Status: status}
		if header[0] != "000000" {
			file.OldMode = header[0]
		}
		if header[1] != "000000" {
			file.NewMode = header[1]
		}
		paths := 1
		if status == "renamed" || status == "copied" {
			paths = 2
			if file.Similarity, _ = strconv.Atoi(header[4][1:]); file.Similarity < 0 || file.Similarity > 100 {
				return nil, fmt.Errorf("the Git comparison contains an invalid similarity")
			}
		}
		if i+paths >= len(fields) {
			return nil, fmt.Errorf("the Git comparison entry has no path")
		}
		file.Path = fields[i+paths]
		if paths == 2 {
			file.PreviousPath = fields[i+1]
		}
		files = append(files, file)
		i += paths + 1
	}
	return files, nil
}

func addLineCounts(files []ChangeFile, numstat []byte) error {
	fields := strings.Split(strings.TrimSuffix(string(numstat), "\x00"), "\x00")
	entry := 0
	for i := 0; len(numstat) > 0 && i < len(fields); entry++ {
		counts := strings.SplitN(fields[i], "\t", 3)
		if len(counts) != 3 || entry >= len(files) {
			return fmt.Errorf("the Git line counts do not match the file inventory")
		}
		// A rename or a copy states its two paths in the next two fields.
		if counts[2] == "" {
			i += 2
		}
		i++
		if counts[0] == "-" && counts[1] == "-" {
			files[entry].Binary = true
			continue
		}
		added, addErr := strconv.ParseInt(counts[0], 10, 64)
		removed, removeErr := strconv.ParseInt(counts[1], 10, 64)
		if addErr != nil || removeErr != nil || added < 0 || removed < 0 {
			return fmt.Errorf("the Git line counts are invalid")
		}
		files[entry].Additions, files[entry].Deletions = added, removed
	}
	if entry != len(files) {
		return fmt.Errorf("the Git line counts do not match the file inventory")
	}
	return nil
}

// addPatchRanges assigns each part of the patch to its inventory entry. Git
// writes the patch parts in inventory order. A change between a file and a
// symbolic link or a submodule has two parts: the deletion and the creation.
func addPatchRanges(files []ChangeFile, patch []byte) error {
	header := []byte("diff --git ")
	var starts []int
	for at := 0; at < len(patch); {
		if bytes.HasPrefix(patch[at:], header) {
			starts = append(starts, at)
		}
		next := bytes.IndexByte(patch[at:], '\n')
		if next < 0 {
			break
		}
		at += next + 1
	}
	part := 0
	for i := range files {
		parts := 1
		if files[i].Status == "type-changed" {
			parts = 2
		}
		if part+parts > len(starts) || (part == 0 && len(starts) > 0 && starts[0] != 0) {
			return fmt.Errorf("the Git patch does not match the file inventory")
		}
		end := len(patch)
		if part+parts < len(starts) {
			end = starts[part+parts]
		}
		body := patch[starts[part]:end]
		files[i].PatchOffset, files[i].PatchBytes = int64(starts[part]), int64(len(body))
		files[i].TextDiff = bytes.Contains(body, []byte("\n@@ "))
		if bytes.Contains(body, []byte("\nBinary files ")) {
			files[i].Binary = true
		}
		part += parts
	}
	if part != len(starts) {
		return fmt.Errorf("the Git patch does not match the file inventory")
	}
	return nil
}
