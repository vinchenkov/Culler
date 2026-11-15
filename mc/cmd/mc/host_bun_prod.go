//go:build !test_fake_routing

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"mc/deployment"
	"mc/distribution"
)

// hostBunRoot is the folder that holds one host Bun folder for each version.
// The provider proxy of a published install runs the host Bun of its release record.
func init() {
	deployment.HostBunPath = func(home string) (string, error) {
		record, err := publishedSupervisionRecord(home)
		if err != nil {
			return "", err
		}
		return hostBunPath(home, record)
	}
}

func hostBunRoot(home string) string {
	return filepath.Join(home, "bun")
}

// hostBunExecutable is the Bun program of one version in the Culler home.
func hostBunExecutable(home, version string) string {
	return filepath.Join(hostBunRoot(home), version, "bin", "bun")
}

// publishedSupervisionRecord returns the release record of a published
// install, or nil for a source install. It selects the record of this setup
// program the same way that the agent image selection does.
func publishedSupervisionRecord(home string) (*distribution.Record, error) {
	path := os.Getenv("CULLER_RELEASE_RECORD")
	if path == "" {
		if pending, err := distribution.Load(filepath.Join(home, "state", "pending-release.json")); err == nil && pending.Commit == releaseBuildID {
			return &pending, nil
		}
		path = filepath.Join(home, distribution.InstalledRecord)
	}
	r, err := distribution.Load(path)
	if os.IsNotExist(err) && os.Getenv("CULLER_RELEASE_RECORD") == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.Commit != releaseBuildID && os.Getenv("CULLER_RELEASE_RECORD") == "" {
		return nil, nil
	}
	return &r, nil
}

// hostBunPath selects the Bun that the resident and the dashboard run.
// A published install runs the host Bun of its release record.
// A source install runs the bun on PATH.
func hostBunPath(home string, record *distribution.Record) (string, error) {
	if record == nil {
		return exec.LookPath("bun")
	}
	if record.HostBun == nil {
		return "", errors.New("the release record names no host Bun")
	}
	path := hostBunExecutable(home, record.HostBun.Version)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("the host Bun %s is absent", path)
	}
	return path, nil
}

// checkPublishedHostBun requires that the release record names the Bun version
// of this setup program and that the installer placed that Bun.
// The installer verifies the archive digest against the record.
func checkPublishedHostBun(home string, r distribution.Record, pin deployment.HostBunPin) error {
	h := r.HostBun
	if h == nil {
		return errors.New("The release record has no Bun entry. Download install.sh again.")
	}
	if h.Version != pin.Version {
		return errors.New("The release record names a different Bun release. Download install.sh again.")
	}
	if _, err := hostBunPath(home, &r); err != nil {
		return errors.New("Culler has no Bun program. Run install.sh again.")
	}
	return nil
}

// removePreviousHostBun removes each folder under MC_HOME/bun except the
// host Bun of the installed release. The previous host Bun stays until the
// installed release completes its activation. A removal problem goes into
// the operation record and does not stop the completion.
func (p *publishedSetup) removePreviousHostBun() error {
	if p.record.HostBun == nil {
		return nil
	}
	root := hostBunRoot(p.home)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var problems []error
	for _, entry := range entries {
		if entry.Name() == p.record.HostBun.Version {
			continue
		}
		problems = append(problems, os.RemoveAll(filepath.Join(root, entry.Name())))
	}
	return errors.Join(problems...)
}
