package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"

	"mc/config"
	"mc/deployment"
	"mc/routing"
	"mc/verbs"
)

type projectSetupRequest struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Path          string  `json:"path"`
	Identity      string  `json:"identity"`
	Create        bool    `json:"create"`
	ConfirmAccess bool    `json:"confirm_access"`
	Vision        *string `json:"vision"`
	Revision      string  `json:"revision"`
	SubmissionID  string  `json:"submission_id"`
}

func isProjectSetupAction(action string) bool {
	switch action {
	case "setup-preview", "setup-save", "details", "vision-save", "vision-open", "vision-workspace", "vision-apply", "start", "readiness", "workspaces":
		return true
	}
	return false
}

func setupCommand(args ...string) (map[string]any, error) {
	return setupCommandInput(nil, args...)
}

func setupCommandInput(input any, args ...string) (map[string]any, error) {
	var stdin io.Reader
	if input != nil {
		body, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		stdin = bytes.NewReader(body)
	}
	return setupCommandReader(stdin, args...)
}

func setupCommandReader(stdin io.Reader, args ...string) (map[string]any, error) {
	var out, diagnostic bytes.Buffer
	code := run(args, stdin, &out, &diagnostic)
	if code != 0 {
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(out.Bytes(), &envelope)
		message := envelope.Error.Message
		if message == "" {
			message = strings.TrimSpace(diagnostic.String())
		}
		if message == "" {
			message = "The project operation failed."
		}
		return nil, verbs.Domainf("%s", message)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return nil, verbs.Domainf("The project operation returned an invalid result.")
	}
	return result, nil
}

func commandProjectSetup(args []string, stdin io.Reader) (result any, err error) {
	defer func() {
		var usage *verbs.UsageError
		var domain *verbs.DomainError
		if err != nil && !errors.As(err, &usage) && !errors.As(err, &domain) {
			err = verbs.Domainf("%s", err)
		}
	}()
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "Project setup"); err != nil {
		return nil, err
	}
	if !canInspectProjectVision() {
		return nil, verbs.Domainf("Project setup requires the Culler host.")
	}
	if len(args) != 1 {
		return nil, verbs.Usagef("The Project action has unexpected arguments.")
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return nil, err
	}
	if args[0] == "readiness" {
		return projectAgentReadiness(home)
	}
	if args[0] == "workspaces" {
		listed, err := readHostProjectList()
		if err != nil {
			return nil, err
		}
		roots := []verbs.OnboardWorkspaceRoot{}
		for _, row := range listed["projects"].([]map[string]any) {
			root, _ := row["workspace_root"].(string)
			if row["status"] == "archived" || root == "" {
				continue
			}
			roots = append(roots, verbs.OnboardWorkspaceRoot{ID: row["id"].(string), Root: root})
		}
		if err := verbs.ValidateOnboardWorkspaceRoots(roots); err != nil {
			return nil, err
		}
		return map[string]any{"workspaces": roots}, nil
	}
	var req projectSetupRequest
	dec := json.NewDecoder(io.LimitReader(stdin, 128*1024+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, verbs.Usagef("The Project request is invalid.")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, verbs.Usagef("The Project request contains extra data.")
	}
	if args[0] == "vision-apply" {
		release, err := admitProjectWork(home, req.ID)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	if args[0] == "setup-preview" {
		preview, err := verbs.PreviewWorkspace(req.Path)
		if err != nil {
			return nil, err
		}
		cfg, err := config.Load(filepath.Join(home, "config.toml"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"workspace": preview, "git_author": cfg.Git.AuthorName + " <" + cfg.Git.AuthorEmail + ">"}, nil
	}
	if !verbs.ValidProjectID(req.ID) {
		return nil, verbs.Usagef("The Project identifier is invalid.")
	}
	// This lock serializes dashboard project writes without a database connection on the host.
	lock, err := os.OpenFile(filepath.Join(home, "project-setup.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, verbs.Domainf("Another project operation is active. Retry when it completes.")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	listed, err := readHostProjectList()
	if err != nil {
		return nil, err
	}
	var row map[string]any
	for _, candidate := range listed["projects"].([]map[string]any) {
		if candidate["id"] == req.ID {
			row = candidate
		}
	}
	if args[0] == "setup-save" {
		return saveProjectSetup(req, row, listed)
	}
	if row == nil {
		return nil, verbs.Domainf("This Project no longer exists.")
	}
	if args[0] == "vision-apply" {
		return applyProjectVision(req, row)
	}
	if args[0] == "vision-open" {
		if draft, err := setupCommandInput(map[string]any{"id": req.ID}, "project", "vision-draft"); err == nil {
			return draft, nil
		}
	}
	path, _ := row["workspace_root"].(string)
	preview, err := verbs.PreviewWorkspace(path)
	if err != nil {
		return nil, err
	}
	if args[0] == "details" {
		cfg, err := config.Load(filepath.Join(home, "config.toml"))
		if err != nil {
			return nil, err
		}
		name, email := cfg.Git.AuthorName, cfg.Git.AuthorEmail
		if override, ok := row["git_author_name"].(string); ok && override != "" {
			name = override
			email, _ = row["git_author_email"].(string)
		}
		return map[string]any{"project": row, "workspace": preview, "git_author": name + " <" + email + ">"}, nil
	}
	if args[0] == "vision-workspace" {
		return map[string]any{"workspace": preview}, nil
	}
	if row["status"] == "archived" {
		return nil, verbs.Domainf("An archived Project permits inspection only.")
	}
	if args[0] == "vision-open" {
		return initializeProjectVisionDraft(req.ID, preview)
	}
	if req.Identity == "" || req.Identity != preview.Identity {
		return nil, verbs.Domainf("The project folder changed. Reload Project details before another change.")
	}
	if args[0] == "vision-save" {
		if row["setup_status"] == "vision" || row["setup_status"] == "configured" {
			return nil, verbs.Domainf("Use Save Vision on the Vision page for this Project.")
		}
		if req.Vision == nil {
			return nil, verbs.Usagef("The vision text is required.")
		}
		vision, err := verbs.SaveWorkspaceVision(path, req.Revision, *req.Vision, req.Identity)
		if err != nil {
			return nil, err
		}
		return map[string]any{"vision": vision}, nil
	}
	if args[0] == "start" {
		start, err := projectStartRequest(req, preview)
		if err != nil {
			return nil, err
		}
		if !preview.Vision.Check.Ready {
			return nil, verbs.Domainf("%s Add the project goal before work starts.", preview.Vision.Check.Reason)
		}
		readiness, err := projectAgentReadiness(home)
		if err != nil {
			return nil, err
		}
		if !readiness.Ready {
			return nil, verbs.Domainf("%s", strings.Join(readiness.Issues, " "))
		}
		report, err := setupCommand("doctor")
		if err != nil {
			return nil, err
		}
		for _, item := range report["findings"].([]any) {
			finding := item.(map[string]any)
			if finding["status"] == "fail" && finding["check"] != "projects" {
				return nil, verbs.Domainf("A deployment check failed: %v. Open Configs to inspect deployment checks.", finding["check"])
			}
		}
		// Reuse the full setup check for this workspace before the pause changes.
		args := []string{"onboard", "project", "--project", req.ID, "--workspace-root", path, "--accept-generated-sandbox"}
		if name, ok := row["git_author_name"].(string); ok && name != "" {
			email, _ := row["git_author_email"].(string)
			args = append(args, "--project-git-author-name", name, "--project-git-author-email", email)
		}
		if _, err := setupCommand(args...); err != nil {
			return nil, err
		}
		preview, err = verbs.PreviewWorkspace(path)
		if err != nil {
			return nil, err
		}
		if preview.Identity != start.WorkspaceIdentity || preview.Vision.Revision != start.WorkspaceRevision {
			return nil, verbs.Domainf("The workspace Vision changed during initialization. Compare it with the draft before you retry.")
		}
		return setupCommandInput(start, "project", "vision-start")
	}
	return nil, verbs.Usagef("The Project action is unknown.")
}

func projectStartRequest(req projectSetupRequest, preview verbs.WorkspacePreview) (verbs.VisionDraftRequest, error) {
	request := verbs.VisionDraftRequest{
		ID: req.ID, Revision: req.Revision, SubmissionID: req.SubmissionID,
		WorkspaceIdentity: preview.Identity, WorkspaceRevision: preview.Vision.Revision,
	}
	result, err := setupCommandInput(map[string]any{"id": req.ID}, "project", "vision-draft")
	if err != nil {
		return request, err
	}
	body, err := json.Marshal(result["draft"])
	var draft verbs.VisionDraft
	if err != nil || json.Unmarshal(body, &draft) != nil {
		return request, verbs.Domainf("The Vision draft is unavailable. Reload the Vision page before you retry.")
	}
	return request, verbs.ValidateVisionStart(draft, request)
}

func saveProjectSetup(req projectSetupRequest, row map[string]any, listed map[string]any) (any, error) {
	if !req.ConfirmAccess || strings.TrimSpace(req.Title) == "" || len(req.Title) > verbs.MaxProjectTitleBytes || strings.IndexFunc(req.Title, unicode.IsControl) >= 0 {
		return nil, verbs.Usagef("Enter a Project name and confirm project access.")
	}
	preview, err := verbs.PreviewWorkspace(req.Path)
	if err != nil {
		return nil, err
	}
	if strings.TrimRight(req.Path, "/") != preview.Path || (row == nil && req.Identity != preview.Identity) {
		return nil, verbs.Domainf("The Project folder changed. Select the folder again before you continue.")
	}
	if row != nil {
		root, _ := row["workspace_root"].(string)
		if row["status"] == "archived" || row["title"] != req.Title || (root != "" && root != req.Path) {
			return nil, verbs.Domainf("This Project identifier already belongs to another project. Reload the form.")
		}
		stored, readErr := setupCommandInput(map[string]any{"id": req.ID}, "project", "vision-draft")
		if readErr == nil {
			draft, ok := stored["draft"].(map[string]any)
			if !ok || draft["workspace_identity"] != preview.Identity {
				return nil, verbs.Domainf("The Project folder changed. The Vision draft remains available for comparison.")
			}
			stored["project_id"], stored["status"] = req.ID, row["status"]
			return stored, nil
		}
		if req.Identity != preview.Identity {
			return nil, verbs.Domainf("Select the Project folder again to complete its registration.")
		}
	}
	for _, candidate := range listed["projects"].([]map[string]any) {
		if candidate["id"] != req.ID && candidate["workspace_root"] == req.Path {
			return nil, verbs.Domainf("This folder already belongs to Project %v. Open its details to edit the vision.", candidate["title"])
		}
	}
	if !preview.Exists {
		if !req.Create {
			return nil, verbs.Usagef("Confirm creation of the project folder.")
		}
		if err := os.Mkdir(preview.Path, 0o755); err != nil {
			return nil, err
		}
		preview, err = verbs.PreviewWorkspace(preview.Path)
		if err != nil {
			return nil, err
		}
	}
	argv := []string{"onboard", "project", "--project", req.ID, "--workspace-root", preview.Path, "--project-vision", "later", "--accept-generated-sandbox"}
	if row == nil {
		argv = append(argv, "--title", req.Title, "--paused", "--project-vision-setup")
	}
	if _, err := setupCommand(argv...); err != nil {
		return nil, err
	}
	draft, err := initializeProjectVisionDraft(req.ID, preview)
	if err != nil {
		return nil, err
	}
	draft["project_id"], draft["status"] = req.ID, "paused"
	if row != nil {
		draft["status"] = row["status"]
	}
	return draft, nil
}

func initializeProjectVisionDraft(id string, preview verbs.WorkspacePreview) (map[string]any, error) {
	entries, err := os.ReadDir(preview.Path)
	if err != nil {
		return nil, err
	}
	hasFiles := false
	for _, entry := range entries {
		if entry.Name() != ".git" && entry.Name() != ".mc-artifacts" {
			hasFiles = true
			break
		}
	}
	return setupCommandInput(map[string]any{
		"id": id, "workspace_identity": preview.Identity,
		"workspace_revision": preview.Vision.Revision, "content": preview.Vision.Content,
		"workspace_has_files": hasFiles, "workspace_has_git": preview.Repository,
	}, "project", "vision-init")
}

type projectReadiness struct {
	Ready    bool                          `json:"ready"`
	Issues   []string                      `json:"issues"`
	Profiles []deployment.AgentProfileView `json:"profiles"`
}

func projectAgentReadiness(home string) (projectReadiness, error) {
	result := projectReadiness{Ready: true, Issues: []string{}, Profiles: []deployment.AgentProfileView{}}
	cfg, err := config.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return result, err
	}
	body, err := os.ReadFile(filepath.Join(home, "routing.md"))
	if err != nil {
		return result, err
	}
	registry := routing.RegistryFromRuntimes(cfg.Runtimes)
	_, fake := routing.ActiveRegistry()
	if fake {
		registry["fake"] = "fake"
	}
	table, err := routing.Parse(body, registry)
	if err != nil {
		return result, err
	}
	catalog, err := deployment.AgentProfileList(home)
	if err != nil {
		return result, err
	}
	result.Issues, result.Profiles = agentReadinessIssues(table.Rows(), catalog.Profiles, fake)
	result.Ready = len(result.Issues) == 0
	return result, nil
}

// Readiness reads the current configuration only. Connection health reports each provider failure.
func agentReadinessIssues(routes []routing.Route, profiles []deployment.AgentProfileView, fake bool) ([]string, []deployment.AgentProfileView) {
	issues, assigned := []string{}, []deployment.AgentProfileView{}
	seen := map[string]bool{}
	for _, route := range routes {
		if seen[route.Binding] {
			continue
		}
		seen[route.Binding] = true
		if fake && route.Harness == "fake" {
			continue
		}
		var profile *deployment.AgentProfileView
		for i := range profiles {
			if profiles[i].ID == route.Binding {
				profile = &profiles[i]
				break
			}
		}
		if profile == nil {
			issues = append(issues, fmt.Sprintf("Select an agent profile for %s in Model configuration.", route.Role))
			continue
		}
		assigned = append(assigned, *profile)
		if !profile.Enabled || (profile.AccessStatus != "connected" && profile.AccessStatus != "no_token") {
			issues = append(issues, fmt.Sprintf("Complete access for %s in Model configuration.", profile.Name))
		}
	}
	return issues, assigned
}
