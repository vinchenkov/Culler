package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/distribution"
	"mc/onboarding"
)

type releaseFixtureRemote struct {
	record      distribution.Record
	err         error
	workflow    distribution.Workflow
	workflowErr error
}

func (r releaseFixtureRemote) Latest(context.Context) (distribution.Record, error) {
	return r.record, r.err
}
func (r releaseFixtureRemote) Workflow(context.Context) (distribution.Workflow, error) {
	return r.workflow, r.workflowErr
}
func releaseFixtureRecord(commit string, sequence int64) distribution.Record {
	r := distribution.Record{Format: 1, Commit: strings.Repeat(commit, 40), Sequence: sequence, Platform: "darwin-arm64", Schema: strings.Repeat("f", 64), PublishedAt: time.Now().UTC(), Runtimes: distribution.RuntimeVersions{Go: "1.25.12", Bun: "1.3.9"}, Image: "ghcr.io/owner/repo@sha256:" + strings.Repeat("c", 64)}
	r.ReleaseID, r.Tag = r.Commit, "culler-"+r.Commit
	r.Installer = distribution.Asset{Name: "installer.sh", SHA256: strings.Repeat("d", 64), Size: 10}
	r.Package = distribution.Asset{Name: "culler-darwin-arm64.tar.gz", SHA256: strings.Repeat("e", 64), Size: 100}
	for _, name := range distribution.RequiredChecks {
		r.Checks = append(r.Checks, distribution.Check{Name: name, Passed: true})
	}
	return r
}

func TestPublishedReleaseStatusPreservesUnknownAndCommitOrder(t *testing.T) {
	home := t.TempDir()
	installed := releaseFixtureRecord("a", 2)
	if err := distribution.Save(filepath.Join(home, distribution.InstalledRecord), installed); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		remote releaseFixtureRemote
		state  string
	}{
		{"current", releaseFixtureRemote{record: installed}, "current"},
		{"newer", releaseFixtureRemote{record: releaseFixtureRecord("b", 3)}, "available"},
		{"older", releaseFixtureRemote{record: releaseFixtureRecord("b", 1)}, "installed_newer"},
		{"access", releaseFixtureRemote{err: distribution.ErrAccess}, "unknown"},
		{"network", releaseFixtureRemote{err: errors.New("private response must stay hidden")}, "unknown"},
		{"none", releaseFixtureRemote{err: distribution.ErrNoRelease}, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := inspectPublishedRelease(context.Background(), home, tc.remote)
			if status.State != tc.state || status.Installed != installed.Commit {
				t.Fatalf("status: %+v", status)
			}
			if strings.Contains(status.Detail, "private response") {
				t.Fatal("The status disclosed response bytes.")
			}
		})
	}
	status := inspectPublishedRelease(context.Background(), home, releaseFixtureRemote{record: installed, workflow: distribution.Workflow{State: "failed", Commit: strings.Repeat("b", 40)}})
	if status.State != "current" || status.Workflow.State != "failed" {
		t.Fatalf("workflow status: %+v", status)
	}
	changed := installed
	changed.Image = "ghcr.io/owner/repo@sha256:" + strings.Repeat("d", 64)
	if status := inspectPublishedRelease(context.Background(), home, releaseFixtureRemote{record: changed}); status.State != "unknown" {
		t.Fatalf("Changed contents appeared current: %+v", status)
	}
}

func TestPublishedReleaseStatusRetainsIdentityAfterCredentialRefusal(t *testing.T) {
	home := t.TempDir()
	installed := releaseFixtureRecord("a", 2)
	if err := distribution.Save(filepath.Join(home, distribution.InstalledRecord), installed); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(home, "credential")
	if err := os.WriteFile(credential, []byte("private-value"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(credential, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CULLER_GITHUB_TOKEN_FILE", credential)
	status := readPublishedReleaseStatus(context.Background(), home)
	if status.State != "unknown" || status.Installed != installed.Commit || status.Available != "" {
		t.Fatalf("Credential refusal lost the installed identity: %+v", status)
	}
	if !strings.Contains(status.Detail, "GitHub refused access") || strings.Contains(status.Detail, "private-value") {
		t.Fatalf("Invalid credential detail: %s", status.Detail)
	}
}

func TestReleaseStatusPresentation(t *testing.T) {
	samples := []releaseStatus{
		{State: "current", Installed: strings.Repeat("a", 40), Available: strings.Repeat("a", 40), Detail: "The installed release matches the available release. Service health appears separately."},
		{State: "available", Installed: strings.Repeat("a", 40), Available: strings.Repeat("b", 40), Detail: "A complete release is available. Open the installer to review this update."},
		{State: "unknown", Installed: strings.Repeat("a", 40), Detail: releaseAccessDetail(distribution.ErrAccess)},
		{State: "none", Installed: "unknown", Detail: "No complete release is available. Check the release workflow on GitHub."},
	}
	for _, sample := range samples {
		var output bytes.Buffer
		renderReleaseStatus(onboarding.NewTerminal(nil, &output), sample)
		if !strings.Contains(output.String(), "Installed release:") || strings.Contains(output.String(), "mc __") {
			t.Fatalf("Invalid operator output: %s", output.String())
		}
		if os.Getenv("CULLER_RELEASE_PREVIEW") == "1" {
			renderReleaseStatus(onboarding.NewTerminal(os.Stdin, os.Stdout), sample)
		}
	}
	for _, state := range []string{"preparing", "failed", "cancelled", "published", "unknown"} {
		sample := samples[0]
		sample.Workflow = distribution.Workflow{State: state, Commit: strings.Repeat("c", 40)}
		var output bytes.Buffer
		renderReleaseStatus(onboarding.NewTerminal(nil, &output), sample)
		text := strings.Join(strings.Fields(output.String()), " ")
		if !strings.Contains(text, "main commit") {
			t.Fatalf("Absent workflow state: %s", output.String())
		}
		if state == "published" && (!strings.Contains(text, "GitHub completed the workflow") ||
			!strings.Contains(text, "Run install.sh --check-updates again") ||
			strings.Contains(text, "no complete release") || strings.Contains(text, "state is unknown")) {
			t.Fatalf("A completed workflow received the wrong status: %s", output.String())
		}
		if os.Getenv("CULLER_RELEASE_PREVIEW") == "1" {
			renderReleaseStatus(onboarding.NewTerminal(os.Stdin, os.Stdout), sample)
		}
	}
}

func TestReleaseTerminalCommandPreservesHomeAndArguments(t *testing.T) {
	home := filepath.Join(t.TempDir(), "operator's $(touch unexpected) home")
	for _, dir := range []string{"bin", "state"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	launcher := filepath.Join(home, "bin", "install.sh")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command, err := releaseTerminalCommand(home)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", command).CombinedOutput()
	if err != nil || string(out) != "--mc-home\n"+home+"\n" {
		t.Fatalf("command: %s %v", out, err)
	}
	if _, err := os.Stat(command); !os.IsNotExist(err) {
		t.Fatal("The temporary command remains.")
	}
	if err := os.Chmod(launcher, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := releaseTerminalCommand(home); err == nil {
		t.Fatal("A writable launcher was accepted.")
	}
}
