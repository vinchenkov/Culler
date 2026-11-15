package deployment

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// UpdateCLIInstallation verifies the existing command path without shell edits.
func UpdateCLIInstallation(home, userHome, directory, release, parentPath string) (CLIInstallation, error) {
	return updateCLIInstallation(home, userHome, directory, release, parentPath, verifyCLIShell)
}

func updateCLIInstallation(home, userHome, directory, release, parentPath string, verify func(string, string, string, string, string, string) error) (CLIInstallation, error) {
	record, err := ReadCLIInstallation(home)
	if err != nil {
		return record, err
	}
	if !record.Verified || record.Path != filepath.Join(directory, "culler") || !validProductionBuildID(release) {
		return record, errors.New("The update requires the existing verified command directory.")
	}
	digest, err := CLIFileDigest(record.Path)
	if err != nil {
		return record, err
	}
	internal, err := CLIFileDigest(filepath.Join(directory, "mc"))
	if err != nil || internal != digest {
		return record, errors.New("The installed commands have different release contents.")
	}
	zdot := os.Getenv("ZDOTDIR")
	if zdot == "" {
		zdot = userHome
	}
	if !filepath.IsAbs(zdot) {
		return record, errors.New("ZDOTDIR requires an absolute path for the command check.")
	}
	for _, shell := range []struct{ program, mode string }{{"/bin/zsh", "-ic"}, {"/bin/zsh", "-lic"}, {"/bin/bash", "-ic"}, {"/bin/bash", "-lic"}} {
		if err := verify(shell.program, shell.mode, record.Path, userHome, zdot, parentPath); err != nil {
			return record, err
		}
	}
	record.Release, record.SHA256 = release, digest
	body, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	if err := writeCLIFile(filepath.Join(home, CLIInstallRecord), append(body, '\n'), 0600); err != nil {
		return record, err
	}
	return record, syncDirectory(filepath.Join(home, "state"))
}
