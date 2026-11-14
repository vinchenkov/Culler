package verbs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mc/dispatch"
)

// Operators supply additional skills under MC_HOME:
//
//	collie/<skill-name>/SKILL.md
//	projects/<project-id>/<role-directory>/<skill-name>/SKILL.md
//
// Both paths start under skills/. Culler creates the directories and preserves their contents.
// The resident copies these skills into each run. The spawn effect carries the relative store path.

// skillStoreDirName is the store root's name under MC_HOME.
const skillStoreDirName = "skills"

// skillStoreProjectsDirName holds one directory per Project.
const skillStoreProjectsDirName = "projects"

// CollieSkillStoreRelDir is the MC_HOME-relative store directory of the Collie
// tier, which is not a pipeline role and not specific to a Project.
const CollieSkillStoreRelDir = skillStoreDirName + "/" + CollieSkillBasename

// SkillStoreRoleDirs lists the sorted role directories of a Project's store.
func SkillStoreRoleDirs() []string {
	dirs := make([]string, 0, len(roleDirectiveBasename)+1)
	dirs = append(dirs, "setup")
	for role, basename := range roleDirectiveBasename {
		dirs = append(dirs, roleSkillStoreBasename(role, basename))
	}
	sort.Strings(dirs)
	return dirs
}

// SkillStoreRoot is the store root under MC_HOME.
func SkillStoreRoot(home string) string {
	return filepath.Join(home, skillStoreDirName)
}

// RoleSkillStoreRelDir is the MC_HOME-relative store directory the resident
// reads for one role's run in one Project. Forward slashes: the value is a
// carrier member, not a host path, and the resident joins it under its own
// MC_HOME.
func RoleSkillStoreRelDir(project string, role dispatch.Role) (string, error) {
	if !validSkillStoreSegment(project) {
		return "", fmt.Errorf("project id %q cannot name a skill store directory", project)
	}
	basename, ok := roleDirectiveBasename[role]
	if !ok {
		return "", fmt.Errorf("no skill store directory for role %q", role)
	}
	basename = roleSkillStoreBasename(role, basename)
	return strings.Join([]string{skillStoreDirName, skillStoreProjectsDirName, project, basename}, "/"), nil
}

func roleSkillStoreBasename(role dispatch.Role, basename string) string {
	if role == dispatch.RoleStrategist {
		// Existing operator skills use this directory.
		return "strategist-propose"
	}
	return basename
}

// validSkillStoreSegment admits exactly the identifiers that can be one
// directory name under the store: non-empty, not a dot segment, no separator
// and no NUL. A Project id outside this set has no store directory, so
// nothing is created for it and nothing is read for its runs.
func validSkillStoreSegment(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, "/\\\x00")
}

// EnsureSkillStore creates the empty store shape the home scaffold owns:
// `skills/` and `skills/collie/`. Reports whether anything was created.
func EnsureSkillStore(home string) (bool, error) {
	changed := false
	for _, dir := range []string{SkillStoreRoot(home), filepath.Join(SkillStoreRoot(home), CollieSkillBasename)} {
		created, err := ensureSkillStoreDir(dir)
		if err != nil {
			return false, err
		}
		changed = changed || created
	}
	return changed, nil
}

// ExtendSkillStore creates one Project's store directories: the Project
// directory and one directory per role directory name. It extends a store
// that exists and leaves a home without one alone — the store root is the
// scaffold's to create, and a process that cannot see it (the helper
// container, an agent container) must not grow a private copy of it.
// Reports whether anything was created.
func ExtendSkillStore(home, project string) (bool, error) {
	if !validSkillStoreSegment(project) {
		return false, Domainf("project id %q cannot name a skill store directory", project)
	}
	if st, err := os.Lstat(SkillStoreRoot(home)); err != nil || !st.IsDir() {
		if err != nil && !os.IsNotExist(err) {
			return false, Domainf("inspect skill store: %v", err)
		}
		return false, nil
	}
	changed := false
	wsDir := filepath.Join(SkillStoreRoot(home), skillStoreProjectsDirName, project)
	for _, dir := range append([]string{wsDir}, roleSkillStoreDirs(wsDir)...) {
		created, err := ensureSkillStoreDir(dir)
		if err != nil {
			return false, err
		}
		changed = changed || created
	}
	return changed, nil
}

func roleSkillStoreDirs(wsDir string) []string {
	var dirs []string
	for _, role := range SkillStoreRoleDirs() {
		dirs = append(dirs, filepath.Join(wsDir, role))
	}
	return dirs
}

func ensureSkillStoreDir(dir string) (bool, error) {
	st, err := os.Lstat(dir)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, Domainf("create skill store directory %q: %v", dir, err)
		}
		return true, nil
	case err != nil:
		return false, Domainf("inspect skill store directory %q: %v", dir, err)
	case !st.IsDir():
		return false, Domainf("skill store path %q is not a directory", dir)
	}
	return false, nil
}
