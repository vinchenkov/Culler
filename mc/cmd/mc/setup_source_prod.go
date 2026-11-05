//go:build !test_fake_routing

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mc/verbs"
)

type setupSourceRecord struct {
	Release string `json:"release"`
	Digest  string `json:"digest"`
}

func setupSourceDigest(source string) (string, error) {
	if source == "" {
		return "", fmt.Errorf("the release source is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := exec.CommandContext(ctx, "git", "-C", source, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "mc", "resident", "dashboard", "runner", ".mise.toml").Output()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, name := range bytes.Split(body, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		path := filepath.Join(source, string(name))
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(hash, "%s\x00absent\x00", name)
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("release source %q is not a regular file", path)
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", name, info.Size())
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (r *setupRelease) compareLocalSource(home, source string) {
	digest, err := setupSourceDigest(source)
	if err != nil {
		return
	}
	r.SourceDigest = digest
	var prior setupSourceRecord
	body, err := os.ReadFile(filepath.Join(home, "release", "setup-source.json"))
	recorded := err == nil && json.Unmarshal(body, &prior) == nil && prior.Release == r.Proposed
	if (recorded && prior.Digest != digest) || (!recorded && r.SourceState == "modified") {
		r.Components = append(r.Components, setupReleaseComponent{Name: "Local source", Status: "different", Detail: "The proposed source differs from the source recorded at installation."})
	}
}

func saveSetupSource(home string, args verbs.OnboardArgs) error {
	digest, err := setupSourceDigest(args.HostReleaseSource)
	if err != nil {
		return err
	}
	body, err := json.Marshal(setupSourceRecord{Release: releaseBuildID, Digest: digest})
	if err != nil {
		return err
	}
	root := filepath.Join(home, "release")
	file, err := os.CreateTemp(root, ".setup-source-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(body, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), filepath.Join(root, "setup-source.json"))
}

func setupSourceModified(source string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := exec.CommandContext(ctx, "git", "-C", source, "status", "--porcelain", "--untracked-files=normal", "--", "mc", "resident", "dashboard", "runner", ".mise.toml").Output()
	return err != nil || strings.TrimSpace(string(body)) != ""
}
