package verbs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const ProjectVisionFile = "VISION.md"

const ProjectVisionExplanation = "VISION.md describes your project's open-ended, high-level goal. " +
	"Culler develops strategy and proposes high-leverage work to advance that vision as your project develops. " +
	"This vision gives Culler the direction it requires before it can start work."

const (
	ProjectVisionPresent    = "present"
	ProjectVisionAbsent     = "absent"
	ProjectVisionUnreadable = "unreadable"
	ProjectVisionEmpty      = "empty"
	ProjectVisionTemplate   = "template"
	ProjectVisionUnknown    = "unknown"
	ProjectVisionDeclined   = "declined"
)

const (
	ProjectVisionAnswerCreate  = "create"
	ProjectVisionAnswerLater   = "later"
	ProjectVisionAnswerDecline = ProjectVisionAnswerLater
)

const projectVisionTemplate = `# VISION.md

<!-- VISION.md describes your project's open-ended, high-level goal.
Culler develops strategy and proposes work that advances this vision.
Describe your vision below before Culler can start work. -->

## Purpose

<!-- Describe what this project is for and who it serves. -->

## Direction

<!-- Describe the project's intended direction. A high-level goal is sufficient. -->

## Out of scope

<!-- Describe what this project excludes. -->
`

type ProjectVisionCheck struct {
	State   string `json:"state"`
	Reason  string `json:"reason"`
	Path    string `json:"path"`
	Ready   bool   `json:"ready"`
	Content string `json:"content,omitempty"`
}

func ValidProjectVisionState(state string) bool {
	return state == ProjectVisionPresent || state == ProjectVisionDeclined || state == ProjectVisionUnknown
}

func ProjectVisionResult(root, state string) ProjectVisionCheck {
	result := ProjectVisionCheck{State: state, Path: ProjectVisionPath(root), Ready: state == ProjectVisionPresent}
	switch state {
	case ProjectVisionPresent:
	case ProjectVisionAbsent, ProjectVisionDeclined:
		result.Reason = "VISION.md is absent."
	case ProjectVisionUnreadable:
		result.Reason = "VISION.md is unreadable."
	case ProjectVisionEmpty:
		result.Reason = "VISION.md is empty."
	case ProjectVisionTemplate:
		result.Reason = "VISION.md requires your vision."
	default:
		result.State = ProjectVisionUnknown
		result.Ready = false
		result.Reason = "Culler must check VISION.md before work can start."
	}
	return result
}

// CheckProjectVision inspects the current host file without changing it.
func CheckProjectVision(root string) ProjectVisionCheck {
	result := func(state string) ProjectVisionCheck { return ProjectVisionResult(root, state) }
	if !filepath.IsAbs(root) {
		return result(ProjectVisionUnknown)
	}
	info, err := os.Stat(ProjectVisionPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return result(ProjectVisionAbsent)
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o444 == 0 {
		return result(ProjectVisionUnreadable)
	}
	file, err := os.Open(ProjectVisionPath(root))
	if err != nil {
		return result(ProjectVisionUnreadable)
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxProjectVisionBytes+1))
	if err != nil {
		return result(ProjectVisionUnreadable)
	}
	return checkProjectVisionContent(root, body)
}

func checkProjectVisionContent(root string, body []byte) ProjectVisionCheck {
	result := func(state string) ProjectVisionCheck { return ProjectVisionResult(root, state) }
	if len(body) > maxProjectVisionBytes {
		check := result(ProjectVisionUnreadable)
		check.Reason = "VISION.md exceeds the 64 KiB file limit."
		return check
	}
	if !utf8.Valid(body) {
		return result(ProjectVisionUnreadable)
	}
	if strings.TrimSpace(string(body)) == "" {
		return result(ProjectVisionEmpty)
	}
	content := visionComment.ReplaceAllString(string(body), "")
	content = strings.ReplaceAll(content, generatedVisionPurpose, "")
	var statements []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		heading := strings.ToLower(strings.TrimSpace(strings.TrimLeft(line, "#")))
		if line == "" || heading == "vision" || heading == "vision.md" || heading == "purpose" || heading == "direction" || heading == "out of scope" {
			continue
		}
		statements = append(statements, line)
	}
	if len(statements) == 0 {
		return result(ProjectVisionTemplate)
	}
	check := result(ProjectVisionPresent)
	check.Content = string(body)
	return check
}

const maxProjectVisionBytes = 64 * 1024

const generatedVisionPurpose = `This file is the North Star of this project for Culler. The
Strategist and Arbiter read it to seek, propose, and approve new work. It
states where the software should go, broad enough to point at a direction and
declarative when it points at work; it carries no implementation detail.`

var visionComment = regexp.MustCompile(`(?s)<!--.*?(?:-->|$)`)

// ProjectVisionPath returns the required file path.
func ProjectVisionPath(root string) string {
	return filepath.Join(root, ProjectVisionFile)
}

// InspectProjectVision reports whether the workspace root carries a
// VISION.md. A directory under that name is not the file: it reports absent.
func InspectProjectVision(root string) (bool, error) {
	info, err := os.Stat(ProjectVisionPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, Domainf("inspect %s: %v", ProjectVisionPath(root), err)
	}
	return !info.IsDir(), nil
}

func resolveProjectVision(project, root, answer string) (string, error) {
	switch answer {
	case "", ProjectVisionAnswerCreate, ProjectVisionAnswerLater:
	default:
		return "", Usagef("project vision answer must be create|later")
	}
	check := CheckProjectVision(root)
	if check.State != ProjectVisionAbsent {
		if check.Ready {
			return ProjectVisionPresent, nil
		}
		return ProjectVisionUnknown, nil
	}
	switch answer {
	case ProjectVisionAnswerLater:
		return ProjectVisionDeclined, nil
	case ProjectVisionAnswerCreate:
		if err := writeProjectVisionTemplate(root); err != nil {
			return "", err
		}
		if CheckProjectVision(root).Ready {
			return ProjectVisionPresent, nil
		}
		return ProjectVisionUnknown, nil
	}
	return "", Usagef("%s Project %s requires %s. Ask your agent to create the file now or after setup (create|later).", ProjectVisionExplanation, project, check.Path)
}

// writeProjectVisionTemplate creates the starter O_EXCL: a file that
// appeared between the inspection and this write is the operator's, and it
// stays exactly as they wrote it.
func writeProjectVisionTemplate(root string) error {
	path := ProjectVisionPath(root)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return Domainf("create %s: %v", path, err)
	}
	if _, err := f.WriteString(projectVisionTemplate); err != nil {
		f.Close()
		return Domainf("write %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		return Domainf("write %s: %v", path, err)
	}
	return nil
}

func ProjectVisionDetail(root, vision string) string {
	check := ProjectVisionResult(root, vision)
	if check.Ready {
		return fmt.Sprintf("%s present at %s", ProjectVisionFile, check.Path)
	}
	return fmt.Sprintf("Work cannot start. %s Describe your vision in %s. Your agent can help you write it.", check.Reason, check.Path)
}

func recordProjectVision(ctx context.Context, q Q, project, vision string) (bool, error) {
	if vision == "" {
		return false, nil
	}
	stored, err := projectVisionStored(ctx, q)
	if err != nil || !stored {
		return false, err
	}
	res, err := q.ExecContext(ctx,
		`UPDATE projects SET vision = ? WHERE id = ? AND vision <> ?`,
		vision, project, vision)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// projectVisionStored reports whether this spine's projects table carries
// the vision column at all.
func projectVisionStored(ctx context.Context, q Q) (bool, error) {
	var n int
	if err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('projects') WHERE name = 'vision'`).Scan(&n); err != nil {
		return false, err
	}
	return n == 1, nil
}
