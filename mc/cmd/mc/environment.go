package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"time"

	"mc/deployment"
	"mc/verbs"
)

func commandEnvironmentImageCheck(stdin io.Reader) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err = verbs.RequireHostScope(id, "Project image compatibility"); err != nil {
		return nil, err
	}
	req, err := readEnvironmentRequest(stdin)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err = deployment.CheckProjectImage(ctx, req.BaseDigest, req.ImageDigest); err != nil {
		return nil, err
	}
	return map[string]any{"checked": true, "base_digest": req.BaseDigest, "image_digest": req.ImageDigest}, nil
}

func readEnvironmentRequest(stdin io.Reader) (verbs.EnvironmentRequest, error) {
	var req verbs.EnvironmentRequest
	decoder := json.NewDecoder(io.LimitReader(stdin, 3*1024*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, verbs.Usagef("The environment request is invalid: %v", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return req, verbs.Usagef("The environment request contains extra data.")
	}
	return req, nil
}

func commandEnvironment(args []string, stdin io.Reader) (any, error) {
	if len(args) == 3 && args[1] == "--request" && args[2] == "-" {
		args = args[:1]
	}
	if len(args) != 1 {
		return nil, verbs.Usagef("The environment action requires one JSON request.")
	}
	req, err := readEnvironmentRequest(stdin)
	if err != nil {
		return nil, err
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if args[0] == "work" && req.RuntimeBinding.ID == "" {
		if err := verbs.RequireHostScope(id, "Environment execution"); err != nil {
			return nil, err
		}
		req.RuntimeBinding, err = verbs.ResolveEnvironmentRuntime()
		if err != nil {
			return nil, err
		}
	}
	return withSpine(func(db *sql.DB) (any, error) { return verbs.ProjectEnvironment(db, id, args[0], req) })
}

func brokerEnvironmentWork(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err = verbs.RequireHostScope(id, "Environment execution"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	req, err := readEnvironmentRequest(stdin)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	req.RuntimeBinding, err = verbs.ResolveEnvironmentRuntime()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return delegate(args, bytes.NewReader(body), stdout, stderr)
}

func brokerEnvironmentSave(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err = verbs.RequireHostScope(id, "Environment edits"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	req, err := readEnvironmentRequest(stdin)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if req.Configuration != nil && req.Configuration.SourceCommit == "" {
		view, err := setupCommandInput(map[string]string{"project_id": req.ProjectID}, "environment", "get")
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if view["selected"] != nil {
			listed, err := setupCommand("project", "list")
			if err != nil {
				return writeVerbError(stdout, stderr, err)
			}
			for _, entry := range listed["projects"].([]any) {
				row := entry.(map[string]any)
				if row["id"] == req.ProjectID {
					root, _ := row["workspace_root"].(string)
					req.Configuration.SourceCommit = acceptedCommitAt(root)
					break
				}
			}
			if req.Configuration.SourceCommit == "" {
				return writeVerbError(stdout, stderr, verbs.Domainf("The Project's accepted source commit is unavailable."))
			}
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if shouldDelegateToHelper() {
		return delegate(args, bytes.NewReader(body), stdout, stderr)
	}
	return runLocal(args, bytes.NewReader(body), stdout, stderr)
}

func brokerEnvironmentRetry(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err = verbs.RequireHostScope(id, "Environment repair"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	req, err := readEnvironmentRequest(stdin)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	view, err := setupCommandInput(map[string]string{"project_id": req.ProjectID}, "environment", "get")
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	state, _ := view["environment"].(map[string]any)
	selected, _ := view["selected"].(map[string]any)
	configuration, _ := selected["configuration"].(map[string]any)
	if state["image_missing"] == true && configuration["dockerfile"] != nil {
		listed, err := setupCommand("project", "list")
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		req.SourceCommit = ""
		for _, entry := range listed["projects"].([]any) {
			row := entry.(map[string]any)
			if row["id"] == req.ProjectID {
				root, _ := row["workspace_root"].(string)
				req.SourceCommit = acceptedCommitAt(root)
				break
			}
		}
		if req.SourceCommit == "" {
			return writeVerbError(stdout, stderr, verbs.Domainf("The Project's accepted source commit is unavailable."))
		}
		req.RuntimeBinding, err = verbs.ResolveEnvironmentRuntime()
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if shouldDelegateToHelper() {
		return delegate(args, bytes.NewReader(body), stdout, stderr)
	}
	return runLocal(args, bytes.NewReader(body), stdout, stderr)
}
