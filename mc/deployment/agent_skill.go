package deployment

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// AgentSkillReleaseID identifies the executable that installs the skill.
var AgentSkillReleaseID = "development"

// HostReleaseSourcePath maps installed skill files to their authoritative sources.
func HostReleaseSourcePath(root, relative string) string {
	direct := filepath.Join(root, filepath.FromSlash(relative))
	if _, err := os.Lstat(direct); !errors.Is(err, os.ErrNotExist) {
		return direct
	}
	const prefix = "skills/culler/"
	if strings.HasPrefix(relative, prefix) {
		file := strings.TrimPrefix(relative, prefix)
		if file == "references/writing.md" || file == "references/task-description.md" {
			name := strings.TrimSuffix(strings.TrimPrefix(file, "references/"), ".md")
			return filepath.Join(root, "mc", "verbs", "skills", "shared", name, "SKILL.md")
		}
		return filepath.Join(root, "mc", "verbs", "skills", "collie", "culler", filepath.FromSlash(file))
	}
	return direct
}

func readHostReleaseSource(root, relative string) ([]byte, error) {
	source := HostReleaseSourcePath(root, relative)
	rel, err := filepath.Rel(root, source)
	if err != nil {
		return nil, err
	}
	return readReleaseSource(root, filepath.ToSlash(rel))
}

func agentSkillInstallation(home string) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if directory := os.Getenv("MC_BIN_DIR"); filepath.IsAbs(directory) {
		executable = filepath.Join(directory, "mc")
	} else if record, err := ReadCLIInstallation(home); err == nil {
		executable = filepath.Join(filepath.Dir(record.Path), "mc")
	}
	bun, err := HostBunPath(home)
	if err != nil {
		pin, pinErr := LoadHostBunPin()
		if pinErr != nil {
			return nil, pinErr
		}
		bun = filepath.Join(home, "bun", pin.Version, "bin", "bun")
	}
	body, err := json.MarshalIndent(map[string]any{
		"environment": "host", "mc_home": home, "executable": executable,
		"command_prefix": []string{"env", "MC_HOME=" + home, executable},
		"release":        AgentSkillReleaseID, "skill": filepath.Join(home, "release", "host", "skills", "culler"),
		"trace_root":   filepath.Join(home, "agentsview", "inspection"),
		"trace_reader": filepath.Join(home, "release", "host", "runner", "shared", "trace-read.ts"),
		"bun":          bun, "scratch": os.TempDir(),
	}, "", "  ")
	return append(body, '\n'), err
}
