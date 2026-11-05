package substrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// MaxDispatchMountProjectionBytes reserves one quarter of the 1 MiB private
// dispatch frame for the complete Project/profile projection. The
// remaining frame carries selection, tunables, candidate, and protocol
// identity. Admission measures the exact canonical JSON bytes below.
const MaxDispatchMountProjectionBytes = 256 * 1024

type DispatchMountState struct {
	SubjectRepository *DispatchRepository `json:"subject_repository,omitempty"`
	SelectedProject   string              `json:"selected_project"`
	Projects          []DispatchProject   `json:"projects"`
}

// DispatchRepository names the isolated repository of a run. The run has one
// subject: a task, or the wiki update that WikiUpdate names by its run
// identifier. Its json keys are alphabetical: the mount plan that carries it
// replays through a generic map, and the replay must encode the same bytes.
type DispatchRepository struct {
	DestinationBranch string `json:"destination_branch"`
	InputCommit       string `json:"input_commit"`
	MaxTemporaryBytes int64  `json:"max_temporary_bytes"`
	PlanVersion       int64  `json:"plan_version,omitempty"`
	TaskID            int64  `json:"task_id,omitempty"`
	VersionID         *int64 `json:"version_id,omitempty"`
	WikiUpdate        string `json:"wiki_update,omitempty"`
}

type DispatchProject struct {
	ProjectID           string                       `json:"project_id"`
	Status              string                       `json:"status"`
	ProfilePresent      bool                         `json:"profile_present"`
	ProfileID           string                       `json:"profile_id"`
	WorkspaceRoot       string                       `json:"workspace_root"`
	ArtifactRoots       []string                     `json:"artifact_roots"`
	DeniedPaths         []string                     `json:"denied_paths"`
	ToolHomeDir         string                       `json:"tool_home_dir"`
	RuntimeControlDir   string                       `json:"runtime_control_dir"`
	GitIdentityOverride *DispatchGitIdentityOverride `json:"git_identity_override"`
	// The declared env planes and the credential-delivery class. The
	// planes are carried raw: boundary.BuildEnvPlan is their one validator,
	// and it runs in the attest leg before any lease claim.
	HarnessEnvPolicy    string `json:"harness_env_policy"`
	ToolEnvPolicy       string `json:"tool_env_policy"`
	RuntimeAuthDelivery string `json:"runtime_auth_delivery"`
}

type DispatchGitIdentityOverride struct {
	AuthorEmail string `json:"author_email"`
	AuthorName  string `json:"author_name"`
}

type dispatchProjectionQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func LoadDispatchProjectProjection(ctx context.Context, q dispatchProjectionQuerier) ([]DispatchProject, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT w.id, w.status, w.sandbox_profile,
		       p.id, p.workspace_root, p.artifact_roots,
		       p.denied_paths, p.tool_home_dir, p.runtime_control_dir,
		       p.harness_env_policy, p.tool_env_policy, p.runtime_auth_delivery,
		       p.git_author_name, p.git_author_email
		FROM projects w
		LEFT JOIN sandbox_profiles p ON p.id = w.sandbox_profile
		ORDER BY w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DispatchProject{}
	for rows.Next() {
		var ws DispatchProject
		var associatedProfile, profileID, workspace sql.NullString
		var artifactRaw, deniedRaw sql.NullString
		var toolHome, runtimeControl sql.NullString
		var harnessEnv, toolEnv, authDelivery sql.NullString
		var gitAuthorName, gitAuthorEmail sql.NullString
		if err := rows.Scan(&ws.ProjectID, &ws.Status, &associatedProfile,
			&profileID, &workspace, &artifactRaw, &deniedRaw,
			&toolHome, &runtimeControl, &harnessEnv, &toolEnv, &authDelivery,
			&gitAuthorName, &gitAuthorEmail); err != nil {
			return nil, err
		}
		ws.ProfilePresent = associatedProfile.Valid && profileID.Valid
		if associatedProfile.Valid {
			ws.ProfileID = associatedProfile.String
		}
		if ws.ProfilePresent {
			ws.WorkspaceRoot = workspace.String
			ws.ToolHomeDir = toolHome.String
			ws.RuntimeControlDir = runtimeControl.String
			ws.HarnessEnvPolicy = harnessEnv.String
			ws.ToolEnvPolicy = toolEnv.String
			ws.RuntimeAuthDelivery = authDelivery.String
			switch {
			case gitAuthorName.Valid && gitAuthorEmail.Valid:
				ws.GitIdentityOverride = &DispatchGitIdentityOverride{
					AuthorName: gitAuthorName.String, AuthorEmail: gitAuthorEmail.String,
				}
			case gitAuthorName.Valid != gitAuthorEmail.Valid:
				return nil, fmt.Errorf("Project %q carries an incomplete Git identity override", ws.ProjectID)
			}
			if ws.ArtifactRoots, err = normalizedDispatchPaths(artifactRaw, 64); err != nil {
				return nil, fmt.Errorf("Project %q artifact_roots: %w", ws.ProjectID, err)
			}
			if ws.DeniedPaths, err = normalizedDispatchPaths(deniedRaw, 512); err != nil {
				return nil, fmt.Errorf("Project %q denied_paths: %w", ws.ProjectID, err)
			}
		} else {
			ws.ArtifactRoots = []string{}
			ws.DeniedPaths = []string{}
		}
		if !validDispatchScalar(ws.ProjectID) || !validDispatchScalar(ws.Status) ||
			(ws.ProfileID != "" && !validDispatchScalar(ws.ProfileID)) ||
			(ws.WorkspaceRoot != "" && !validDispatchScalar(ws.WorkspaceRoot)) ||
			(ws.ToolHomeDir != "" && !validDispatchScalar(ws.ToolHomeDir)) ||
			(ws.RuntimeControlDir != "" && !validDispatchScalar(ws.RuntimeControlDir)) ||
			!validDispatchGitIdentity(ws.GitIdentityOverride) {
			return nil, fmt.Errorf("Project %q carries a scalar outside the dispatch frame bounds", ws.ProjectID)
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}

func validDispatchGitIdentity(identity *DispatchGitIdentityOverride) bool {
	if identity == nil {
		return true
	}
	name, email := identity.AuthorName, identity.AuthorEmail
	return name != "" && email != "" &&
		strings.TrimSpace(name) != "" && strings.TrimSpace(email) != "" &&
		strings.Contains(email, "@") &&
		len(name) <= 256 && len(email) <= 256 &&
		!strings.ContainsAny(name, "\x00\r\n") &&
		!strings.ContainsAny(email, "\x00\r\n")
}

func ValidateDispatchMountProjection(ctx context.Context, q dispatchProjectionQuerier) error {
	rows, err := LoadDispatchProjectProjection(ctx, q)
	if err != nil {
		return err
	}
	body, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	if len(body) > MaxDispatchMountProjectionBytes {
		return fmt.Errorf("Project/profile projection is %d bytes, exceeds %d-byte admission budget",
			len(body), MaxDispatchMountProjectionBytes)
	}
	return nil
}

func normalizedDispatchPaths(raw sql.NullString, max int) ([]string, error) {
	if !raw.Valid || raw.String == "" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw.String), &values); err != nil || values == nil {
		return nil, fmt.Errorf("must be a JSON array of strings")
	}
	if len(values) > max {
		return nil, fmt.Errorf("contains %d items, exceeds bound %d", len(values), max)
	}
	sort.Strings(values)
	for i, value := range values {
		if !validDispatchScalar(value) {
			return nil, fmt.Errorf("item %d is not a bounded structural string", i)
		}
		if i > 0 && values[i-1] == value {
			return nil, fmt.Errorf("duplicate path %q", value)
		}
	}
	return values, nil
}
