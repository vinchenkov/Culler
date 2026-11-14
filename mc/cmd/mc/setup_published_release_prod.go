//go:build !test_fake_routing

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"mc/deployment"
	"mc/distribution"
	"mc/onboarding"
	"mc/verbs"
)

func inspectPublishedSetupRelease(home string, args verbs.OnboardArgs, docker onboarding.DockerStatus, path string) setupRelease {
	r := setupRelease{Installed: "unknown", Proposed: releaseBuildID, SourceState: "published"}
	proposed, err := distribution.Load(path)
	if err != nil {
		r.Components = []setupReleaseComponent{{Name: "Published release", Status: "unknown", Detail: err.Error()}}
		return r
	}
	var resident struct {
		Release string `json:"releaseBuildId"`
	}
	body, _ := os.ReadFile(filepath.Join(home, "supervision", "resident.json"))
	if json.Unmarshal(body, &resident) == nil && validReleaseBuildID(resident.Release) {
		r.Installed = resident.Release
	}
	if installed, err := distribution.Load(filepath.Join(home, distribution.InstalledRecord)); err == nil {
		if r.Installed == "unknown" {
			r.Installed = installed.Commit
		}
		if installed.Sequence > proposed.Sequence {
			r.Relation = "older"
		} else if installed.Sequence < proposed.Sequence {
			r.Relation = "newer"
		}
		if installed.Commit == proposed.Commit && !samePublishedRecord(installed, proposed) {
			r.Components = append(r.Components, setupReleaseComponent{Name: "Published release", Status: "unknown", Detail: "The release contents differ from the installed record."})
		}
	} else if !os.IsNotExist(err) {
		r.Components = append(r.Components, setupReleaseComponent{Name: "Published release", Status: "unknown", Detail: err.Error()})
	}
	status := "same"
	if r.Installed != proposed.Commit || resident.Release != proposed.Commit {
		status = "different"
	}
	r.Components = append(r.Components, setupReleaseComponent{Name: "Service release", Installed: r.Installed, Proposed: proposed.Commit, Status: status})
	for _, item := range []struct {
		name, source string
		matches      func(string, string) (bool, error)
	}{
		{"Host files", args.HostReleaseSource, deployment.HostReleaseMatches},
		{"Runner files", args.ReleaseSource, deployment.RunnerReleaseMatches},
	} {
		component := setupReleaseComponent{Name: item.name, Status: "same"}
		if same, err := item.matches(home, item.source); err != nil {
			component.Status = "incomplete"
			component.Detail = err.Error()
		} else if !same {
			component.Status = "different"
		}
		r.Components = append(r.Components, component)
	}
	component := setupReleaseComponent{Name: "Agent image", Installed: "unknown", Proposed: proposed.Commit, Status: "unknown"}
	if docker.State == onboarding.DockerReady {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		body, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{json .}}", proposed.Image).Output()
		cancel()
		if err == nil {
			err = validatePublishedImage(body, proposed)
		}
		if err == nil {
			component.Status = "same"
			component.Installed = proposed.Commit
		} else {
			component.Status = "incomplete"
			component.Detail = err.Error()
		}
	}
	r.Components = append(r.Components, component)
	if r.Installed != "unknown" {
		r.InstalledDisplay = distribution.Label(cullerInstalledVersion(home, r.Installed), r.Installed)
	}
	r.ProposedDisplay = proposed.Label()
	return r
}

func shortPublishedID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
