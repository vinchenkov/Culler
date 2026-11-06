//go:build !test_fake_routing

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"mc/deployment"
	"mc/verbs"
)

func brokerOnboardHome(args []string, private bool, stdout, stderr io.Writer) int {

	a, err := parseOnboardArgs(args[1:])
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if a.Section != "home" {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc onboard home [--restore-latest]"))
	}
	if !private {
		if err := verbs.GateSetupOnboardArgs(a); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	}
	restoreDetail := ""
	if a.RestoreLatest {
		snapshot, size, err := restoreLatestSpine()
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		restoreDetail = fmt.Sprintf("; restore verified %d bytes from %s", size, snapshot)
	}
	req, err := verbs.PrepareOnboardHome(false)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	update, err := deployment.LoadUpdateJournal(home)
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("read update state: %v", err))
	}
	publicUpdate := update != nil && update.BlocksWork()
	// Release replacement remains an offline operation, even with a compatible database.
	releaseChanged := false
	for _, source := range []struct {
		path    string
		matches func(string, string) (bool, error)
	}{
		{a.ReleaseSource, deployment.RunnerReleaseMatches}, {a.HostReleaseSource, deployment.HostReleaseMatches},
	} {
		if source.path == "" {
			continue
		}
		matches, err := source.matches(home, source.path)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if !matches {
			releaseChanged = true
			if err := requireSupervisionUnloaded(home, "release update"); err != nil {
				return writeVerbError(stdout, stderr, err)
			}
		}
	}
	helper, err := ensureRuntimeHelper()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("ensure warm helper: %v", err))
	}
	result, err := provisionOnboardSpine(req, private && a.AllowDatabaseReset,
		func(req verbs.OnboardSpineRequest) (verbs.OnboardSpineResult, error) {
			return callOnboardSpine(helper, req)
		},
		func() error {
			if err := requireSupervisionUnloaded(home, "database reset"); err != nil {
				return err
			}
			manager, err := productionHelperManager(execDockerRunner{})
			if err != nil {
				return err
			}
			if err := removeManagedDatabaseUsers(manager); err != nil {
				return err
			}
			return manager.requireDatabaseUsersStopped()
		},
	)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	status, detail, err := verbs.FinalizeOnboardHome(result)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	detail += restoreDetail
	if releaseChanged && !publicUpdate {
		manager, err := productionHelperManager(execDockerRunner{})
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := deployment.RetireProviderCredentialArtifacts(home, func() error { return removeManagedReleaseUsers(manager) }); err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("stop the previous release containers: %v", err))
		}
	}
	if a.ReleaseSource != "" {
		home, err := configuredCanonicalHome()
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("resolve runner release home: %v", err))
		}
		releaseStatus, err := deployment.InstallRunnerRelease(home, a.ReleaseSource)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("install production runner release: %v", err))
		}
		image, err := configuredProductionImage()
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := deployment.InstallRuntimeVolume(context.Background(), home, image, releaseBuildID, nil); err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("install Culler runtime: %v", err))
		}
		if err := deployment.ConfigureRuntimeSource(home, a.ReleaseSource, releaseBuildID); err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("record runtime source: %v", err))
		}
		if err := deployment.UpdateOpenCodeVolume(context.Background(), home, image, releaseBuildID); err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("update OpenCode: %v", err))
		}
		if releaseStatus == "done" {
			status = "done"
		}
		detail += "; runner release " + releaseStatus
	}
	if a.HostReleaseSource != "" {
		home, err := configuredCanonicalHome()
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("resolve host release home: %v", err))
		}
		hostStatus, err := deployment.InstallHostRelease(home, a.HostReleaseSource)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("install native host release: %v", err))
		}
		if hostStatus == "done" {
			status = "done"
		}
		detail += "; host release " + hostStatus
	}
	if private && !publicUpdate {
		// The front doors own the grant store's shape. A store from a release
		// that keyed grants by binding id moves to credential source ids here,
		// before supervision starts a resident that reads only the new shape.
		home, err := configuredCanonicalHome()
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("resolve runtime grant store home: %v", err))
		}
		migrated, err := deployment.MigrateRuntimeGrantStore(home)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("migrate runtime grant store: %v", err))
		}
		if len(migrated) > 0 {
			status = "done"
			detail += "; runtime grants moved to credential sources: " + strings.Join(migrated, ", ")
		}
	}
	if changed, err := installOnboardCLI(private, a); err != nil {
		return writeVerbError(stdout, stderr, err)
	} else if changed {
		status = "done"
	}
	return writeOnboardSection(stdout, stderr, "home", status, detail)
}

func writeOnboardSection(stdout, stderr io.Writer, section, status, detail string) int {
	if err := writeJSON(stdout, map[string]any{
		"ok":       true,
		"sections": []map[string]any{{"section": section, "status": status, "detail": detail}},
	}); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}

// The production path keeps the database or replaces it on an explicit request.
// A reset request reaches the helper only after every database user stops.
func provisionOnboardSpine(req verbs.OnboardSpineRequest, reset bool,
	call func(verbs.OnboardSpineRequest) (verbs.OnboardSpineResult, error), stopped func() error,
) (verbs.OnboardSpineResult, error) {
	req.AllowReset, req.Reset = false, false
	if reset {
		if err := stopped(); err != nil {
			return verbs.OnboardSpineResult{}, err
		}
		req.Reset = true
	}
	return call(req)
}

// removeManagedDatabaseUsers removes the running agent and Homie containers
// that use the database volume. The reset deletes the records of their runs
// and sessions. The helper stays, because it replaces the database.
func removeManagedDatabaseUsers(m *helperManager) error {
	helper, exists, err := inspectJSON[helperContainerInspect](m.docker, "container", m.names.Helper)
	if err != nil || !exists || !validContainerID(helper.ID) {
		return fmt.Errorf("inspect database reset helper: exists=%v err=%v", exists, err)
	}
	out, err := m.docker.CombinedOutput("container", "ls", "--no-trunc", "--quiet",
		"--filter", "volume="+m.names.Volume, "--filter", "label=mc-managed=true")
	if err != nil {
		return fmt.Errorf("inspect managed database users: %w", err)
	}
	for _, id := range strings.Fields(string(out)) {
		if id == helper.ID {
			continue
		}
		if !validContainerID(id) {
			return fmt.Errorf("a managed database user has an invalid container identity")
		}
		if out, err := m.docker.CombinedOutput("container", "rm", "--force", id); err != nil {
			return fmt.Errorf("remove managed database user %s: %v: %s", id, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// A release replaces container authority and keeps the spine and native sessions.
func removeManagedReleaseUsers(m *helperManager) error {
	helper, exists, err := inspectJSON[helperContainerInspect](m.docker, "container", m.names.Helper)
	if err != nil || !exists || !validContainerID(helper.ID) {
		return fmt.Errorf("inspect release helper: exists=%v err=%v", exists, err)
	}
	seen := map[string]bool{helper.ID: true}
	var containers []string
	for _, volume := range []string{m.names.Volume, m.names.RuntimeVolume} {
		out, err := m.docker.CombinedOutput("container", "ls", "--all", "--no-trunc", "--quiet",
			"--filter", "volume="+volume, "--filter", "label=mc-managed=true")
		if err != nil {
			return fmt.Errorf("inspect previous release containers: %w", err)
		}
		for _, id := range strings.Fields(string(out)) {
			if !validContainerID(id) {
				return fmt.Errorf("a previous release container has an invalid identity")
			}
			if !seen[id] {
				seen[id] = true
				containers = append(containers, id)
			}
		}
	}
	for _, id := range containers {
		if out, err := m.docker.CombinedOutput("container", "rm", "--force", id); err != nil {
			return fmt.Errorf("remove previous release container %s: %v: %s", id, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func callOnboardSpine(helper string, req verbs.OnboardSpineRequest) (verbs.OnboardSpineResult, error) {
	var result verbs.OnboardSpineResult
	frame, err := json.Marshal(req)
	if err != nil {
		return result, err
	}
	output := boundedDoctorBuffer{limit: privateDoctorOutputLimit}
	diagnostic := boundedDoctorBuffer{limit: privateDoctorOutputLimit}
	cmd := exec.Command("docker", "exec", "-i", helper, "mc", "__onboard-spine")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(frame), &output, &diagnostic
	if err := cmd.Run(); err != nil {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(output.Bytes(), &envelope) == nil && envelope.Error.Message != "" {
			if envelope.Error.Code == "usage" {
				return result, verbs.Usagef("%s", envelope.Error.Message)
			}
			return result, &verbs.DomainError{Code: envelope.Error.Code, Msg: envelope.Error.Message}
		}
		return result, verbs.Usagef("database preparation failed: %v: %s", err, strings.TrimSpace(diagnostic.String()))
	}
	if output.overflow || diagnostic.overflow {
		return result, verbs.Usagef("database preparation output exceeded its limit")
	}
	dec := json.NewDecoder(&output)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return result, verbs.Usagef("decode database preparation result: %v", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return result, verbs.Usagef("database preparation result carries trailing data")
	}
	return result, nil
}
