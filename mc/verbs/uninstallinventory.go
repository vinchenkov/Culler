package verbs

import (
	"path/filepath"
	"strings"
)

const (
	spineVolumePrefix   = "mc-spine-"
	runtimeVolumePrefix = "mc-runtime-"
	helperPrefix        = "mc-helper-"
	setupPrefix         = "mc-setup-"
	e2ePrefix           = "mc-e2e-"
)

func PipelineContainerLabelFilters() []string {
	return []string{"--filter", "label=mc-managed=true", "--filter", "label=mc-tier=pipeline"}
}

var UninstallImages = []string{"mc-prod", "mc-real-e2e", "mc-fake-e2e"}

func CullerVolumes(names []string) []string {
	var out []string
	for _, name := range names {
		switch {
		case strings.HasPrefix(name, spineVolumePrefix) && derivedSuffix(strings.TrimPrefix(name, spineVolumePrefix)):
			out = append(out, name)
		case strings.HasPrefix(name, runtimeVolumePrefix) && derivedSuffix(strings.TrimPrefix(name, runtimeVolumePrefix)):
			out = append(out, name)
		case strings.HasPrefix(name, "mc-opencode-") && derivedSuffix(strings.TrimPrefix(name, "mc-opencode-")):
			out = append(out, name)
		case strings.HasPrefix(name, e2ePrefix):
			out = append(out, name)
		}
	}
	return out
}

func CullerContainers(names []string) []string {
	var out []string
	for _, name := range names {
		switch {
		case strings.HasPrefix(name, helperPrefix) && derivedSuffix(strings.TrimPrefix(name, helperPrefix)):
			out = append(out, name)
		case strings.HasPrefix(name, setupPrefix), strings.HasPrefix(name, e2ePrefix):
			out = append(out, name)
		}
	}
	return out
}

func CullerLaunchAgents(files []string) (labels []string, plists []string) {
	for _, file := range files {
		base := filepath.Base(file)
		if !strings.HasSuffix(base, ".plist") {
			continue
		}
		label := strings.TrimSuffix(base, ".plist")
		if !CullerLaunchLabel(label) {
			continue
		}
		labels = append(labels, label)
		plists = append(plists, file)
	}
	return labels, plists
}

// CullerLaunchLabel recognizes both names used by released installations.
// Only the current name has an AgentsView unit.
func CullerLaunchLabel(label string) bool {
	for _, prefix := range []string{"com.culler.", "com.mission-control."} {
		if !strings.HasPrefix(label, prefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(label, prefix), ".")
		if len(parts) != 2 || !derivedSuffix(parts[0]) {
			return false
		}
		return parts[1] == "resident" || parts[1] == "dashboard" || (prefix == "com.culler." && parts[1] == "agentsview")
	}
	return false
}

func derivedSuffix(suffix string) bool {
	if len(suffix) != 12 {
		return false
	}
	for _, r := range suffix {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
