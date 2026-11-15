package deployment

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

const agentSkillIntegrationRecord = "state/agent-skill-integration.json"

// AgentSkillRoots lists the personal configuration directories that can receive the Culler skill.
var AgentSkillRoots = []string{".agents", ".claude", ".codex"}

type AgentSkillIntegration struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

// InstallAgentSkillIntegration links the installed Culler skill into the personal skills directory of each root.
// An entry that does not link to the installed skill stays intact.
// A link that Culler owns outside the selected roots is removed.
func InstallAgentSkillIntegration(home, userHome string, roots []string) error {
	if !filepath.IsAbs(home) || !filepath.IsAbs(userHome) {
		return errors.New("The skill integration requires absolute home paths.")
	}
	target := filepath.Join(home, "release", "host", "skills", "culler")
	if info, err := os.Lstat(filepath.Join(target, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
		return errors.New("The installed Culler skill is unavailable.")
	}
	existing, err := readAgentSkillIntegration(home)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var wanted []string
	for _, root := range roots {
		if !slices.Contains(AgentSkillRoots, root) {
			return errors.New("The skill integration names an unsupported directory.")
		}
		wanted = append(wanted, filepath.Join(userHome, root, "skills", "culler"))
	}
	for _, record := range existing {
		if !slices.Contains(wanted, record.Path) {
			if err := removeOwnedSkillLink(record); err != nil {
				return err
			}
		}
	}
	var owned []AgentSkillIntegration
	var created []string
	for _, path := range wanted {
		if _, err := os.Lstat(path); err == nil {
			if link, err := os.Readlink(path); err == nil && link == target {
				owned = append(owned, AgentSkillIntegration{Path: path, Target: target})
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if !operatorSkillDirectory(filepath.Dir(path)) {
			continue
		}
		if err := os.Symlink(target, path); err != nil {
			return err
		}
		created = append(created, path)
		owned = append(owned, AgentSkillIntegration{Path: path, Target: target})
	}
	if err := writeAgentSkillIntegration(home, owned); err != nil {
		for _, path := range created {
			_ = os.Remove(path)
		}
		return err
	}
	return nil
}

// operatorSkillDirectory creates a missing skills directory and its parent.
// It reports false for a directory that the operator does not own exclusively.
func operatorSkillDirectory(skills string) bool {
	for _, dir := range []string{filepath.Dir(skills), skills} {
		if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return false
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return false
		}
		uid, _, ok := statOwner(info)
		if !info.IsDir() || !ok || int(uid) != os.Getuid() || info.Mode().Perm()&0022 != 0 {
			return false
		}
	}
	return true
}

func removeOwnedSkillLink(record AgentSkillIntegration) error {
	if link, err := os.Readlink(record.Path); err == nil && link == record.Target {
		if err := os.Remove(record.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func writeAgentSkillIntegration(home string, records []AgentSkillIntegration) error {
	path := filepath.Join(home, agentSkillIntegrationRecord)
	if len(records) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := ensureOwnerDirectory(filepath.Join(home, "state"), "skill integration state"); err != nil {
		return err
	}
	body, _ := json.Marshal(records)
	return writeCLIFile(path, append(body, '\n'), 0600)
}

// readAgentSkillIntegration also accepts the single-link record of an earlier release.
func readAgentSkillIntegration(home string) ([]AgentSkillIntegration, error) {
	path := filepath.Join(home, agentSkillIntegrationRecord)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || links != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 16384 {
		return nil, errors.New("The skill integration record requires an owner-only regular file.")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records []AgentSkillIntegration
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '{' {
		var record AgentSkillIntegration
		err = json.Unmarshal(body, &record)
		records = []AgentSkillIntegration{record}
	} else {
		err = json.Unmarshal(body, &records)
	}
	if err != nil {
		return nil, errors.New("The skill integration record is invalid.")
	}
	target := filepath.Join(home, "release", "host", "skills", "culler")
	for _, record := range records {
		if !filepath.IsAbs(record.Path) || filepath.Clean(record.Path) != record.Path ||
			filepath.Base(record.Path) != "culler" || filepath.Base(filepath.Dir(record.Path)) != "skills" ||
			!slices.Contains(AgentSkillRoots, filepath.Base(filepath.Dir(filepath.Dir(record.Path)))) || record.Target != target {
			return nil, errors.New("The skill integration record is invalid.")
		}
	}
	return records, nil
}

// PrepareAgentSkillUninstall keeps ownership data after removal of deployment files.
func PrepareAgentSkillUninstall(home string) (func() error, error) {
	records, err := readAgentSkillIntegration(home)
	if errors.Is(err, os.ErrNotExist) {
		return func() error { return nil }, nil
	}
	if err != nil {
		return nil, err
	}
	return func() error {
		for _, record := range records {
			if err := removeOwnedSkillLink(record); err != nil {
				return err
			}
		}
		if err := os.Remove(filepath.Join(home, agentSkillIntegrationRecord)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}, nil
}
