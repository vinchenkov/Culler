package deployment

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const agentSkillIntegrationRecord = "state/agent-skill-integration.json"

type AgentSkillIntegration struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

// InstallAgentSkillIntegration preserves a skill that another installer owns.
func InstallAgentSkillIntegration(home, userHome string) (string, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(userHome) {
		return "", errors.New("The skill integration requires absolute home paths.")
	}
	target := filepath.Join(home, "release", "host", "skills", "culler")
	if info, err := os.Lstat(filepath.Join(target, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
		return "", errors.New("The installed Culler skill is unavailable.")
	}
	path := filepath.Join(userHome, ".claude", "skills", "culler")
	existing, err := readAgentSkillIntegration(home)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := os.Lstat(path); err == nil {
		link, linkErr := os.Readlink(path)
		if linkErr == nil && existing.Path == path && existing.Target == target && link == target {
			return "installed", nil
		}
		return "conflict", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, dir := range []string{filepath.Join(userHome, ".claude"), filepath.Join(userHome, ".claude", "skills")} {
		if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return "", err
		}
		uid, _, ok := statOwner(info)
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || int(uid) != os.Getuid() || info.Mode().Perm()&0022 != 0 {
			return "", fmt.Errorf("The skill directory %s requires an operator-owned directory.", dir)
		}
	}
	if err := ensureOwnerDirectory(filepath.Join(home, "state"), "skill integration state"); err != nil {
		return "", err
	}
	record := AgentSkillIntegration{Path: path, Target: target}
	body, _ := json.Marshal(record)
	if err := os.Symlink(target, path); err != nil {
		return "", err
	}
	if err := writeCLIFile(filepath.Join(home, agentSkillIntegrationRecord), append(body, '\n'), 0600); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return "installed", nil
}

func readAgentSkillIntegration(home string) (AgentSkillIntegration, error) {
	var record AgentSkillIntegration
	path := filepath.Join(home, agentSkillIntegrationRecord)
	info, err := os.Lstat(path)
	if err != nil {
		return record, err
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || links != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 16384 {
		return record, errors.New("The skill integration record requires an owner-only regular file.")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return record, err
	}
	if json.Unmarshal(body, &record) != nil || !filepath.IsAbs(record.Path) || filepath.Clean(record.Path) != record.Path ||
		filepath.Base(record.Path) != "culler" || filepath.Base(filepath.Dir(record.Path)) != "skills" ||
		filepath.Base(filepath.Dir(filepath.Dir(record.Path))) != ".claude" || record.Target != filepath.Join(home, "release", "host", "skills", "culler") {
		return record, errors.New("The skill integration record is invalid.")
	}
	return record, nil
}

// PrepareAgentSkillUninstall keeps ownership data after removal of deployment files.
func PrepareAgentSkillUninstall(home string) (func() error, error) {
	record, err := readAgentSkillIntegration(home)
	if errors.Is(err, os.ErrNotExist) {
		return func() error { return nil }, nil
	}
	if err != nil {
		return nil, err
	}
	return func() error {
		target, err := os.Readlink(record.Path)
		if err == nil && target == record.Target {
			if err := os.Remove(record.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := os.Remove(filepath.Join(home, agentSkillIntegrationRecord)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}, nil
}
