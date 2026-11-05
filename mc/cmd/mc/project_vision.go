package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"mc/verbs"
)

// readHostProjectList reads database facts before the host inspects files.
func readHostProjectList() (map[string]any, error) {
	var stdout, stderr bytes.Buffer
	args := []string{"project", "list"}
	var code int
	if shouldDelegateToHelper() {
		if _, err := ensureRuntimeHelper(); err != nil {
			return nil, err
		}
		code = delegate(args, nil, &stdout, &stderr)
	} else {
		code = runLocal(args, nil, &stdout, &stderr)
	}
	if code != 0 {
		return nil, fmt.Errorf("read Projects: %s", stderr.String())
	}
	var frame struct {
		Projects []map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
		return nil, err
	}
	if frame.Projects == nil {
		return nil, fmt.Errorf("Project list has no rows field")
	}
	return map[string]any{"projects": frame.Projects}, nil
}

func brokerProjectList(stdout, stderr io.Writer) int {
	result, err := readHostProjectList()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("inspect Project vision: %v", err))
	}
	verbs.InspectProjectListVision(result)
	if err := writeJSON(stdout, result); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}

func inspectDispatchVision(project string) (*verbs.ProjectVisionCheck, error) {
	if project == "" {
		return nil, nil
	}
	result, err := readHostProjectList()
	if err != nil {
		return nil, err
	}
	for _, row := range result["projects"].([]map[string]any) {
		if row["id"] != project {
			continue
		}
		root, _ := row["workspace_root"].(string)
		check := verbs.CheckProjectVision(root)
		return &check, nil
	}
	return nil, fmt.Errorf("Project %s no longer exists", project)
}

// brokerDeliveryStart adds the host Vision check and the accepted commit of
// each Project to a start request, then runs the request in the helper or in
// this process.
func brokerDeliveryStart(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(id, "mc delivery"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	fs := newFlags("mc delivery start")
	request := fs.String("request", "", "")
	if err := parse(fs, args[2:]); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if *request == "" || fs.NArg() != 0 {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc delivery start --request -"))
	}
	var req verbs.StartRequest
	if err := readDeliveryInput(*request, stdin, &req); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	projects, err := readHostProjectList()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("inspect Project vision: %v", err))
	}
	// The host replaces each check and each accepted commit. A process that runs
	// the request keeps each value that it receives: the helper of a test build
	// cannot read host files.
	if req.Vision == nil || shouldDelegateToHelper() {
		req.Vision = map[string]verbs.ProjectVisionCheck{}
	}
	if req.AcceptedCommits == nil || shouldDelegateToHelper() {
		req.AcceptedCommits = map[string]string{}
	}
	roots := map[string]string{}
	for _, row := range projects["projects"].([]map[string]any) {
		project, _ := row["id"].(string)
		root, _ := row["workspace_root"].(string)
		if _, received := req.Vision[project]; project != "" && !received {
			req.Vision[project] = verbs.CheckProjectVision(root)
		}
		if _, received := req.AcceptedCommits[project]; project != "" && !received {
			roots[project] = root
		}
	}
	for project, commit := range acceptedCommitsAt(roots) {
		if commit != "" {
			req.AcceptedCommits[project] = commit
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	delegated := []string{"delivery", "start", "--request", "-"}
	if shouldDelegateToHelper() {
		if _, err := ensureRuntimeHelper(); err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("ensure warm helper: %v", err))
		}
		return delegate(delegated, bytes.NewReader(body), stdout, stderr)
	}
	return runLocal(delegated, bytes.NewReader(body), stdout, stderr)
}
