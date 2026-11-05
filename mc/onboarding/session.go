package onboarding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Session holds setup choices. Credential values and vision content stay outside this record.
type Session struct {
	StageName             string            `json:"stage_name,omitempty"`
	Stage                 int               `json:"stage"`
	Completed             []string          `json:"completed,omitempty"`
	WorkspaceRoot         string            `json:"workspace_root,omitempty"`
	WorkspaceIdentity     string            `json:"workspace_identity,omitempty"`
	Project               string            `json:"project,omitempty"`
	GitAuthorName         string            `json:"git_author_name,omitempty"`
	GitAuthorEmail        string            `json:"git_author_email,omitempty"`
	ProjectGitAuthorName  string            `json:"project_git_author_name,omitempty"`
	ProjectGitAuthorEmail string            `json:"project_git_author_email,omitempty"`
	Vision                string            `json:"vision,omitempty"`
	VisionDeferred        bool              `json:"vision_deferred,omitempty"`
	SandboxAccepted       bool              `json:"sandbox_accepted,omitempty"`
	ProjectConfirmed      bool              `json:"project_confirmed,omitempty"`
	ProjectStep           string            `json:"project_step,omitempty"`
	RuntimeBindings       []string          `json:"runtime_bindings,omitempty"`
	RuntimeIncomplete     []string          `json:"runtime_incomplete,omitempty"`
	RuntimeVerified       map[string]bool   `json:"runtime_verified,omitempty"`
	RuntimeIdentities     map[string]string `json:"runtime_identities,omitempty"`
	Access                *AccessChoice     `json:"access,omitempty"`
	RoutingFile           string            `json:"routing_file,omitempty"`
	DashboardBind         string            `json:"dashboard_bind,omitempty"`
	Tunables              map[string]int    `json:"tunables,omitempty"`
	ReleaseID             string            `json:"release_id,omitempty"`
}

const maxSessionBytes = 64 << 10

var sessionIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validIdentifier(value string) bool {
	return len(value) <= 128 && sessionIdentifier.MatchString(value)
}

func (s Session) validate() error {
	if s.Stage < 0 || s.Stage > 6 {
		return fmt.Errorf("the saved setup stage is invalid")
	}
	if s.StageName != "" && (s.Stage >= len(stageIDs) || !slices.Contains(stageIDs, s.StageName)) {
		return fmt.Errorf("the saved setup stage name is invalid")
	}
	if s.WorkspaceIdentity != "" && !validSessionDigest(s.WorkspaceIdentity) {
		return fmt.Errorf("the saved workspace identity is invalid")
	}
	if s.Access != nil {
		if err := s.Access.validate(); err != nil {
			return err
		}
	}
	identifiers := append(append(append([]string{}, s.Completed...), s.RuntimeBindings...), s.RuntimeIncomplete...)
	for _, value := range identifiers {
		if !validIdentifier(value) {
			return fmt.Errorf("a saved setup identifier is invalid")
		}
	}
	for _, value := range []string{s.Project, s.ReleaseID} {
		if value != "" && !validIdentifier(value) {
			return fmt.Errorf("a saved setup identifier is invalid")
		}
	}
	for _, value := range []string{s.WorkspaceRoot, s.RoutingFile} {
		if value != "" && (!filepath.IsAbs(value) || filepath.Clean(value) != value) {
			return fmt.Errorf("a saved setup path must be absolute and clean")
		}
	}
	for _, value := range []string{
		s.WorkspaceRoot, s.RoutingFile, s.GitAuthorName, s.GitAuthorEmail,
		s.ProjectGitAuthorName, s.ProjectGitAuthorEmail, s.DashboardBind,
	} {
		if len(value) > 4096 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return fmt.Errorf("a saved setup value contains unsupported text")
		}
	}
	switch s.Vision {
	case "", "existing", "create", "later":
	default:
		return fmt.Errorf("the saved vision choice is invalid")
	}
	switch s.ProjectStep {
	case "", "workspace", "project", "author-name", "author-email", "vision", "goal", "sandbox":
	default:
		return fmt.Errorf("the saved project choice is invalid")
	}
	for binding := range s.RuntimeVerified {
		if !validIdentifier(binding) {
			return fmt.Errorf("a saved agent configuration identifier is invalid")
		}
	}
	for binding, identity := range s.RuntimeIdentities {
		if !validIdentifier(binding) || !validSessionDigest(identity) {
			return fmt.Errorf("a saved credential identity is invalid")
		}
	}
	for name, value := range s.Tunables {
		if !validIdentifier(name) || value < 0 {
			return fmt.Errorf("a saved setup interval is invalid")
		}
	}
	return nil
}

func validSessionDigest(value string) bool {
	return len(value) == 64 && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f')
	}) < 0
}

func encodeSession(session Session) ([]byte, error) {
	if err := session.validate(); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode the setup record: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxSessionBytes {
		return nil, fmt.Errorf("the setup record exceeds its size limit")
	}
	return body, nil
}

func decodeSession(body []byte) (Session, error) {
	var session Session
	if len(body) > maxSessionBytes {
		return session, fmt.Errorf("the setup record exceeds its size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&session); err != nil {
		return Session{}, fmt.Errorf("the setup record is invalid")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Session{}, fmt.Errorf("the setup record contains extra data")
	}
	if err := session.validate(); err != nil {
		return Session{}, err
	}
	return session, nil
}
