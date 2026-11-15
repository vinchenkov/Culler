//go:build !test_fake_routing

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mc/deployment"
	"mc/distribution"
	"mc/onboarding"
	"mc/verbs"
)

type setupReleaseComponent struct {
	Name      string `json:"name"`
	Installed string `json:"installed,omitempty"`
	Proposed  string `json:"proposed,omitempty"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
}

type setupRelease struct {
	SourceDigest     string                  `json:"source_digest,omitempty"`
	InstalledDisplay string                  `json:"-"`
	ProposedDisplay  string                  `json:"-"`
	Installed        string                  `json:"installed"`
	Proposed         string                  `json:"proposed"`
	SourceState      string                  `json:"source_state"`
	Relation         string                  `json:"relation,omitempty"`
	Components       []setupReleaseComponent `json:"components"`
}

func inspectSetupRelease(home string, args verbs.OnboardArgs, docker onboarding.DockerStatus) setupRelease {
	if path := os.Getenv("CULLER_RELEASE_RECORD"); path != "" {
		return inspectPublishedSetupRelease(home, args, docker, path)
	}
	r := setupRelease{Installed: "unknown", Proposed: releaseBuildID, SourceState: "unknown"}
	var resident struct {
		Release string `json:"releaseBuildId"`
	}
	body, err := os.ReadFile(filepath.Join(home, "supervision", "resident.json"))
	service := setupReleaseComponent{Name: "Service release", Installed: "unknown", Proposed: releaseBuildID, Status: "unknown"}
	switch {
	case errors.Is(err, os.ErrNotExist):
		service.Status = "incomplete"
	case err != nil:
		service.Detail = err.Error()
	case json.Unmarshal(body, &resident) != nil || !validReleaseBuildID(resident.Release):
		service.Detail = "The service release identifier is unavailable."
	default:
		r.Installed, service.Installed = resident.Release, resident.Release
		service.Status = "same"
		if resident.Release != releaseBuildID {
			service.Status = "different"
		}
	}
	r.Components = append(r.Components, service)
	for _, item := range []struct {
		name, root, source string
		validate           func(string) error
		matches            func(string, string) (bool, error)
	}{
		{"Host files", filepath.Join(home, "release", "host"), args.HostReleaseSource, deployment.ValidateHostRelease, deployment.HostReleaseMatches},
		{"Runner files", filepath.Join(home, "release", "runner"), args.ReleaseSource, deployment.ValidateRunnerRelease, deployment.RunnerReleaseMatches},
	} {
		c := setupReleaseComponent{Name: item.name, Status: "same"}
		if err := item.validate(item.root); err != nil {
			c.Status, c.Detail = "incomplete", err.Error()
		} else if item.source == "" {
			c.Status, c.Detail = "unknown", "The release source is unavailable."
		} else if same, err := item.matches(home, item.source); err != nil {
			c.Status, c.Detail = "unknown", err.Error()
		} else if !same {
			c.Status = "different"
		}
		r.Components = append(r.Components, c)
	}
	image := setupReleaseComponent{Name: "Agent image", Installed: "unknown", Proposed: releaseBuildID, Status: "unknown"}
	if docker.State == onboarding.DockerReady {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		body, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{json .}}", productionImageRef).Output()
		var info struct {
			ID           string `json:"Id"`
			OS           string `json:"Os"`
			Architecture string `json:"Architecture"`
			Config       struct {
				Labels map[string]string `json:"Labels"`
			} `json:"Config"`
		}
		if err != nil {
			image.Detail = err.Error()
		} else if err := json.Unmarshal(body, &info); err != nil {
			image.Detail = err.Error()
		} else {
			id := info.Config.Labels["org.opencontainers.image.revision"]
			if validReleaseBuildID(id) {
				image.Installed = id
			}
			image.Status = "same"
			if id != releaseBuildID || info.OS != "linux" || info.Architecture != "arm64" || (!strings.HasPrefix(info.ID, "sha256:") || !validContainerID(strings.TrimPrefix(info.ID, "sha256:"))) {
				image.Status = "different"
			}
		}
	} else {
		image.Detail = docker.Detail
	}
	r.Components = append(r.Components, image)
	if args.HostReleaseSource != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		body, err := exec.CommandContext(ctx, "git", "-C", args.HostReleaseSource, "status", "--porcelain", "--untracked-files=normal", "--", "mc", "resident", "dashboard", "runner", ".mise.toml").Output()
		if err == nil {
			r.SourceState = "clean"
			if len(body) > 0 {
				r.SourceState = "modified"
			}
		}
		if r.Installed != "unknown" && r.Installed != r.Proposed {
			for _, relation := range []struct{ name, from, to string }{{"newer", r.Installed, r.Proposed}, {"older", r.Proposed, r.Installed}} {
				if exec.CommandContext(ctx, "git", "-C", args.HostReleaseSource, "merge-base", "--is-ancestor", relation.from, relation.to).Run() == nil {
					r.Relation = relation.name
					break
				}
			}
		}
	}
	r.compareLocalSource(home, args.HostReleaseSource)
	for _, id := range []struct {
		value   string
		display *string
	}{{r.Installed, &r.InstalledDisplay}, {r.Proposed, &r.ProposedDisplay}} {
		if id.value == "unknown" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		out, err := exec.CommandContext(ctx, "git", "-C", args.HostReleaseSource, "rev-parse", "--verify", id.value+"^{commit}").Output()
		version := releaseVersion
		if err == nil && id.value != releaseBuildID {
			body, _ := exec.CommandContext(ctx, "git", "-C", args.HostReleaseSource, "show", id.value+":VERSION").Output()
			version = strings.TrimSuffix(string(body), "\n")
		}
		cancel()
		if err == nil {
			*id.display = distribution.Label(version, strings.TrimSpace(string(out)))
		}
	}
	return r
}

func (r setupRelease) differs() bool {
	for _, c := range r.Components {
		if c.Status == "different" {
			return true
		}
	}
	return false
}

func (r setupRelease) matches() bool {
	if len(r.Components) == 0 {
		return false
	}
	for _, c := range r.Components {
		if c.Status != "same" {
			return false
		}
	}
	return true
}

func (r setupRelease) lines() []string {
	installed, proposed := r.Installed, r.Proposed
	if r.InstalledDisplay != "" {
		installed = r.InstalledDisplay
	}
	if r.ProposedDisplay != "" {
		proposed = r.ProposedDisplay
	}
	if installed == "" {
		installed = "unknown"
	}
	if proposed == "" {
		proposed = "unknown"
	}
	lines := []string{"Installed release: " + terminalReleaseLabel(installed), "Proposed release: " + terminalReleaseLabel(proposed)}
	switch r.SourceState {
	case "modified":
		lines = append(lines, "The proposed release includes local source changes.")
	case "unknown":
		lines = append(lines, "Local source changes: unknown.")
	}
	if r.Relation != "" {
		lines = append(lines, "The proposed commit is "+r.Relation+" than the installed commit.")
	}
	for _, c := range r.Components {
		if c.Status == "same" {
			continue
		}
		line := c.Name + ": " + c.Status + "."
		if c.Name == "Agent image" {
			imageRelease := c.Installed
			switch {
			case imageRelease == "":
				imageRelease = "unknown"
			case imageRelease == r.Installed && r.InstalledDisplay != "":
				imageRelease = terminalReleaseLabel(r.InstalledDisplay)
			case imageRelease == r.Proposed && r.ProposedDisplay != "":
				imageRelease = terminalReleaseLabel(r.ProposedDisplay)
			default:
				imageRelease = shortPublishedID(imageRelease)
			}
			line = "Agent image: " + c.Status + ". Release: " + imageRelease + "."
		}
		if c.Status == "different" && (c.Name == "Host files" || c.Name == "Runner files") {
			line = c.Name + " differ from the proposed files."
		}
		lines = append(lines, line)
	}
	return lines
}
