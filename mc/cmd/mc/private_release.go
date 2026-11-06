package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"mc/distribution"
	"mc/onboarding"
	"mc/verbs"
)

type releaseRemote interface {
	Latest(context.Context) (distribution.Record, error)
	Workflow(context.Context) (distribution.Workflow, error)
}

type releaseStatus struct {
	State     string `json:"state"`
	Installed string `json:"installed"`
	Available string `json:"available,omitempty"`
	// A version is empty when Culler cannot find the version of that release.
	InstalledVersion string                `json:"installed_version,omitempty"`
	AvailableVersion string                `json:"available_version,omitempty"`
	Detail           string                `json:"detail"`
	Compatibility    string                `json:"compatibility"`
	Workflow         distribution.Workflow `json:"workflow"`
	CheckedAt        time.Time             `json:"checked_at"`
}

func initialReleaseStatus(home string) (releaseStatus, distribution.Record, error) {
	s := releaseStatus{State: "unknown", Installed: "unknown", Compatibility: "unknown", CheckedAt: time.Now().UTC()}
	installed, installedErr := distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	if installedErr == nil {
		s.Installed, s.InstalledVersion = installed.Commit, installed.Version
	} else {
		var service struct {
			Release string `json:"releaseBuildId"`
		}
		if body, err := os.ReadFile(filepath.Join(home, "supervision", "resident.json")); err == nil && json.Unmarshal(body, &service) == nil {
			s.Installed = service.Release
			if service.Release == releaseBuildID {
				s.InstalledVersion = releaseVersion
			}
		}
	}
	return s, installed, installedErr
}

func inspectPublishedRelease(ctx context.Context, home string, remote releaseRemote) releaseStatus {
	s, installed, installedErr := initialReleaseStatus(home)
	available, err := remote.Latest(ctx)
	if err != nil && !errors.Is(err, distribution.ErrNoRelease) {
		s.Detail = releaseAccessDetail(err)
		return s
	}
	workflow, workflowErr := remote.Workflow(ctx)
	// Only a main commit with a version above the available release can
	// become a new release.
	if workflowErr != nil || distribution.CompareVersions(workflow.Version, available.Version) <= 0 {
		workflow = distribution.Workflow{State: "unknown"}
	}
	s.Workflow = workflow
	if errors.Is(err, distribution.ErrNoRelease) {
		s.State, s.Detail = "none", "No complete release is available. Check the release workflow on GitHub."
		return s
	}
	s.Available, s.AvailableVersion = available.Commit, available.Version
	s.State, s.Detail = "available", "A complete release is available. Open the installer to review this update."
	if installedErr != nil && !errors.Is(installedErr, os.ErrNotExist) {
		s.State, s.Detail = "unknown", "The installed release record is invalid. Run install.sh to inspect the deployment."
		return s
	}
	if installedErr == nil && installed.Commit == available.Commit &&
		(installed.Image != available.Image || installed.Package != available.Package || installed.Installer != available.Installer ||
			installed.Schema != available.Schema || installed.Runtimes != available.Runtimes || installed.Sequence != available.Sequence) {
		s.State, s.Detail = "unknown", "The release files differ from the installed record. Run install.sh to inspect the release."
		return s
	}
	if installedErr == nil && installed.Sequence == available.Sequence && installed.Commit != available.Commit {
		s.State, s.Detail = "unknown", "The release order is inconsistent. Ask your coding harness to inspect the release records."
		return s
	}
	if installedErr == nil && s.Installed == available.Commit {
		s.State, s.Detail = "current", "The installed release matches the available release. Service health appears separately."
	} else if installedErr == nil && installed.Sequence >= available.Sequence {
		s.State, s.Detail = "installed_newer", "The installed release is newer than the available release. Culler will preserve it."
	}
	if installedErr == nil && installed.Schema == available.Schema {
		s.Compatibility = "same_schema"
	} else {
		s.Compatibility = "check_required"
	}
	return s
}

func releaseAccessDetail(err error) string {
	if errors.Is(err, distribution.ErrAccess) {
		return "GitHub refused access. Repair the GitHub credential, then run install.sh again."
	}
	return "Release availability is unknown. Check the network and GitHub access, then retry."
}

func runPrivateRelease(args []string, stdout, stderr io.Writer) int {
	identity, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err = verbs.RequireHostScope(identity, "Release controls"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if !canInspectProjectVision() || len(args) != 1 || (args[0] != "status" && args[0] != "status-terminal" && args[0] != "open") {
		return writeVerbError(stdout, stderr, verbs.Usagef("Release controls require the Culler host."))
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if args[0] == "open" {
		if runtime.GOOS != "darwin" {
			return writeVerbError(stdout, stderr, verbs.Usagef("Open install.sh on a Mac with Apple silicon."))
		}
		path, err := releaseTerminalCommand(home)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "/usr/bin/open", "-a", "Terminal", path).Run(); err != nil {
			_ = os.Remove(path)
			return writeVerbError(stdout, stderr, verbs.Domainf("Terminal could not open. Run install.sh to review the update."))
		}
		return writeReleaseResult(stdout, stderr, map[string]any{"opened": true, "detail": "Continue in Terminal. The installer asks before service activation."})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	status := readPublishedReleaseStatus(ctx, home)
	if args[0] == "status-terminal" {
		renderReleaseStatus(onboarding.NewTerminal(os.Stdin, stdout), status)
		if status.State == "unknown" {
			return 1
		}
		return 0
	}
	return writeReleaseResult(stdout, stderr, status)
}

func readPublishedReleaseStatus(ctx context.Context, home string) releaseStatus {
	token, err := distribution.Credential(ctx)
	if err != nil {
		status, _, _ := initialReleaseStatus(home)
		status.Detail = releaseAccessDetail(distribution.ErrAccess)
		return status
	}
	return inspectPublishedRelease(ctx, home, distribution.NewClient(distribution.DefaultRepository, token))
}

func renderReleaseStatus(terminal *onboarding.Terminal, status releaseStatus) {
	lines := []string{"Installed release: " + terminalReleaseLabel(distribution.Label(status.InstalledVersion, status.Installed))}
	if status.Available != "" {
		lines = append(lines, "Available release: "+terminalReleaseLabel(distribution.Label(status.AvailableVersion, status.Available)))
	}
	lines = append(lines, status.Detail)
	previous := ""
	if status.Available != "" {
		previous = " The previous release remains available."
	}
	if version := status.Workflow.Version; status.Workflow.Commit != "" && status.Workflow.Commit != status.Available && version != "" {
		switch status.Workflow.State {
		case "preparing":
			lines = append(lines, "Version "+version+" is not available yet. Its workflow is in progress.")
		case "failed":
			lines = append(lines, "The workflow for version "+version+" failed."+previous)
		case "cancelled":
			lines = append(lines, "The workflow for version "+version+" stopped before it completed."+previous)
		case "published":
			lines = append(lines, "GitHub completed the workflow for version "+version+". Run install.sh --check-updates again to refresh the available release.")
		default:
			lines = append(lines, "Version "+version+" has no complete release. Its workflow state is unknown.")
		}
	}
	terminal.Panel("Culler releases", lines)
}

// terminalReleaseLabel keeps the version and the commit of a release label
// on one terminal line.
func terminalReleaseLabel(label string) string {
	return strings.Replace(label, " (", "\u00a0(", 1)
}

func writeReleaseResult(stdout, stderr io.Writer, value any) int {
	if err := writeJSON(stdout, value); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}

func releaseTerminalCommand(home string) (string, error) {
	launcher := filepath.Join(home, "bin", "install.sh")
	info, err := os.Lstat(launcher)
	if err != nil {
		return "", verbs.Domainf("The installer is absent. Download install.sh from the documented installer address.")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || !ok || int(owner.Uid) != os.Getuid() || owner.Nlink != 1 {
		return "", verbs.Domainf("The installer file is unsafe. Download install.sh from the documented installer address.")
	}
	f, err := os.CreateTemp(filepath.Join(home, "state"), "update-*.command")
	if err != nil {
		return "", err
	}
	path := f.Name()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	body := "#!/bin/sh\nrm -f -- \"$0\"\nexec /bin/sh " + quote(launcher) + " --mc-home " + quote(home) + "\n"
	if err = f.Chmod(0o700); err == nil {
		_, err = fmt.Fprint(f, body)
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(path)
		return "", errors.Join(err, closeErr)
	}
	return path, nil
}
