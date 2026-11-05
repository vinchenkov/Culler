package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"mc/evalcase"
	"mc/verbs"
)

func artifactExpiryCommand(args []string) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "artifact expiry"); err != nil {
		return nil, err
	}
	if len(args) == 1 && args[0] == "plan" {
		return withSpine(func(db *sql.DB) (any, error) { return verbs.ArtifactExpiryPlan(db, id, time.Now()) })
	}
	if len(args) == 2 && args[0] == "complete" {
		var plan verbs.ArtifactExpiry
		if err := json.Unmarshal([]byte(args[1]), &plan); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.ArtifactExpiryComplete(db, id, plan.TaskID, plan.Token, time.Now())
		})
	}
	return nil, verbs.Usagef("artifact expiry requires a plan or a completion record")
}

func artifactExpiryFrame(args []string, value any) error {
	var stdout, stderr bytes.Buffer
	var code int
	if shouldDelegateToHelper() {
		if _, err := ensureRuntimeHelper(); err != nil {
			return err
		}
		code = delegate(args, nil, &stdout, &stderr)
	} else {
		code = runLocal(args, nil, &stdout, &stderr)
	}
	if code != 0 {
		return fmt.Errorf("artifact expiry failed: %s", stderr.String())
	}
	return json.Unmarshal(stdout.Bytes(), value)
}

func pruneTaskArtifacts() (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "artifact expiry"); err != nil {
		return nil, err
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return nil, err
	}
	roots, err := verbs.ArtifactHostRoots(home)
	if err != nil {
		return nil, err
	}
	var plan verbs.ArtifactExpiry
	if err := artifactExpiryFrame([]string{"__artifact-expiry", "plan"}, &plan); err != nil {
		return nil, err
	}
	if plan.TaskID == 0 {
		return map[string]any{"expired": false}, nil
	}
	cases, err := evaluationFrame("expired", evaluationRequest{Task: plan.TaskID})
	if err != nil {
		return nil, err
	}
	store := evalcase.Store{Root: filepath.Join(home, "evals", "cases")}
	for _, record := range evalRows(cases, "cases") {
		if err = store.PruneUnscored(evalText(record["case_id"])); err != nil {
			return nil, err
		}
	}
	if err := verbs.RemoveExpiredArtifacts(roots, plan); err != nil {
		return nil, err
	}
	body, err := json.Marshal(verbs.ArtifactExpiry{TaskID: plan.TaskID, Token: plan.Token})
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := artifactExpiryFrame([]string{"__artifact-expiry", "complete", string(body)}, &result); err != nil {
		return nil, err
	}
	return result, nil
}
