// This fixture runs the Go control functions against an isolated test database.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"mc/verbs"
)

const project = "proxy-setup"
const operation = "proxy-operation"
const revision = "proxy-revision"

func main() {
	value, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		panic(err)
	}
}

func run() (any, error) {
	home, spine := os.Getenv("MC_HOME"), os.Getenv("MC_SPINE")
	if home == "" || spine != filepath.Join(home, "spine.db") || len(os.Args) < 2 {
		return nil, errors.New("The fixture requires its isolated home and database.")
	}
	if os.Args[1] == "init" {
		if len(os.Args) != 3 || (os.Args[2] != "setup" && os.Args[2] != "validate") {
			return nil, errors.New("The fixture requires a setup or validate operation.")
		}
		workspace := filepath.Join(home, "workspace")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			return nil, err
		}
		if _, err := verbs.Init(verbs.InitArgs{Spine: spine, Project: project, WorkspaceRoot: workspace}); err != nil {
			return nil, err
		}
		db, err := verbs.OpenSpine(spine)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		image := "sha256:" + strings.Repeat("a", 64)
		configuration, err := json.Marshal(verbs.EnvironmentConfiguration{BaseDigest: image})
		if err != nil {
			return nil, err
		}
		runtime, err := json.Marshal(verbs.PrivateRuntimeBinding{Harness: "codex", ID: "test-profile", Credential: "test-connection", Channel: "codex", Model: "small-model"})
		if err != nil {
			return nil, err
		}
		statements := []struct {
			query string
			args  []any
		}{
			{`INSERT INTO environment_revisions(id,project_id,editor,configuration,inputs_digest) VALUES(?,?,'fixture',?,?)`, []any{revision, project, string(configuration), "sha256:" + strings.Repeat("c", 64)}},
			{`INSERT INTO environment_operations(id,project_id,revision_id,kind,status,inputs_digest,image_digest,runtime_identity,runtime_binding) VALUES(?,?,?,?,'queued',?,?,'fixture-release',?)`, []any{operation, project, revision, os.Args[2], "sha256:" + strings.Repeat("c", 64), image, string(runtime)}},
			{`INSERT INTO project_environments(project_id,setup_id,selected_base_digest,draft_revision_id,phase,active_operation_id) VALUES(?,'fixture-setup',?,?,'agent',?)`, []any{project, image, revision, operation}},
		}
		for _, statement := range statements {
			if _, err := db.Exec(statement.query, statement.args...); err != nil {
				return nil, err
			}
		}
		return map[string]any{"operation_id": operation}, nil
	}
	db, err := verbs.OpenSpine(spine)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	switch os.Args[1] {
	case "__project-home":
		return map[string]any{"canonical": filepath.Join(home, "state", "projects", project, "home")}, nil
	case "state":
		var state string
		if err := db.QueryRow(`SELECT status FROM environment_operations WHERE id=?`, operation).Scan(&state); err != nil {
			return nil, err
		}
		return map[string]any{"status": state}, nil
	case "connection":
		if len(os.Args) != 3 || os.Args[2] != "proxy-lifetimes" {
			return nil, errors.New("The fixture supports only the provider lifetime command.")
		}
		var request struct {
			Launches []verbs.ProviderLaunchIdentity `json:"launches"`
		}
		if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
			return nil, err
		}
		return verbs.ProviderAccessLifetimes(db, nil, request.Launches)
	case "environment":
		if len(os.Args) != 3 {
			return nil, errors.New("The fixture requires an environment action.")
		}
		var request verbs.EnvironmentRequest
		if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil && err != io.EOF {
			return nil, err
		}
		var identity *verbs.RunIdentity
		if os.Args[2] == "heartbeat" {
			identity = &verbs.RunIdentity{Tier: "setup", Role: "setup", RunID: request.OperationID}
		}
		return verbs.ProjectEnvironment(db, identity, os.Args[2], request)
	default:
		return nil, errors.New("The fixture command is unavailable.")
	}
}
