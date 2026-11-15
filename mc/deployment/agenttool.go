package deployment

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// A package manager on macOS installs commands in these directories.
var agentToolPackageDirectories = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// findAgentTool returns the absolute path of the Codex or Claude Code program.
// It reads file metadata only and executes no program.
func findAgentTool(harness, home, pathValue string) (string, error) {
	return findAgentToolOn(harness, home, pathValue, runtime.GOOS)
}

func findAgentToolOn(harness, home, pathValue, platform string) (string, error) {
	command := "codex"
	if harness == "claude-sdk" {
		command = "claude"
	}
	var candidates []string
	for _, directory := range filepath.SplitList(pathValue) {
		if directory != "" {
			candidates = append(candidates, filepath.Join(directory, command))
		}
	}
	candidates = append(candidates, filepath.Join(home, ".local", "bin", command))
	if platform == "darwin" {
		for _, directory := range agentToolPackageDirectories {
			candidates = append(candidates, filepath.Join(directory, command))
		}
		for _, applicationRoot := range []string{filepath.Join(home, "Applications"), "/Applications"} {
			if harness == "codex" {
				for _, name := range []string{"Codex.app", "ChatGPT.app"} {
					candidates = append(candidates, filepath.Join(applicationRoot, name, "Contents", "Resources", "codex"))
				}
			} else {
				candidates = append(candidates, filepath.Join(applicationRoot, "Claude.app", "Contents", "Resources", "claude"))
			}
		}
	}
	var incomplete error
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return filepath.Abs(candidate)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			incomplete = err
		}
	}
	return "", incomplete
}

// environmentValue returns the last value of one variable in an environment list.
func environmentValue(environment []string, name string) string {
	for index := len(environment) - 1; index >= 0; index-- {
		key, value, ok := strings.Cut(environment[index], "=")
		if ok && key == name {
			return value
		}
	}
	return ""
}
