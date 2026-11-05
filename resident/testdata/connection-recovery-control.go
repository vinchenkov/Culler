// This fixture runs connection control functions against an isolated database.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mc/domain"
	"mc/verbs"
)

const recoveryConnection = "test-connection"
const recoveryAuthorization = "synthetic-authorization"

func main() {
	value, err := runRecoveryControl()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		panic(err)
	}
}

func runRecoveryControl() (any, error) {
	home, spine := os.Getenv("MC_HOME"), os.Getenv("MC_SPINE")
	if home == "" || spine != filepath.Join(home, "spine.db") || len(os.Args) < 2 {
		return nil, errors.New("The fixture requires its isolated home and database.")
	}
	if os.Args[1] == "init" {
		workspace := filepath.Join(home, "workspace")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			return nil, err
		}
		if _, err := verbs.Init(verbs.InitArgs{Spine: spine, Project: "recovery-test", WorkspaceRoot: workspace}); err != nil {
			return nil, err
		}
	}
	db, err := verbs.OpenSpine(spine)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	switch os.Args[1] {
	case "init":
		check := verbs.ConnectionCheck{ConnectionID: recoveryConnection, AuthorizationID: recoveryAuthorization,
			Operation: "token_refresh", CheckID: domain.DiagnosticID()}
		if _, err := verbs.ConnectionCheckStart(db, nil, check); err != nil {
			return nil, err
		}
		if len(os.Args) > 2 && os.Args[2] == "unknown" {
			return verbs.ConnectionCheckCancel(db, nil, check)
		}
		expires := time.Now().UTC().Add(time.Hour)
		if len(os.Args) > 2 && os.Args[2] == "expired" {
			expires = time.Now().UTC().Add(-time.Hour)
		}
		return verbs.ConnectionObserve(db, nil, verbs.ConnectionObservation{ConnectionCheck: check,
			EventID: domain.DiagnosticID(), Outcome: "ready", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
			ExpiresAt: expires.Format(time.RFC3339Nano)})
	case "state":
		var state, retry, deadline, network, firstFailure string
		var count, sequence int
		err := db.QueryRow(`SELECT h.state,h.failure_count,h.check_sequence,COALESCE(h.next_retry_at,''),
 COALESCE(h.request_deadline,''),COALESCE(d.network_error,''),COALESCE(h.first_failure_at,'')
 FROM connection_health h LEFT JOIN diagnostic_events d ON d.id=h.latest_diagnostic_id
 WHERE h.connection_id=? AND h.operation='token_refresh' AND h.profile_id=''`, recoveryConnection).
			Scan(&state, &count, &sequence, &retry, &deadline, &network, &firstFailure)
		return map[string]any{"state": state, "failure_count": count, "check_sequence": sequence,
			"next_retry_at": retry, "request_deadline": deadline, "network_error": network, "first_failure_at": firstFailure}, err
	case "due":
		// Advance the fixture's retry deadline without waiting for the real delay.
		result, err := db.Exec(`UPDATE connection_health SET next_retry_at=? WHERE connection_id=? AND operation='token_refresh' AND state='retry_wait'`,
			time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano), recoveryConnection)
		if err != nil {
			return nil, err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return nil, errors.New("The fixture has no connection retry to advance.")
		}
		return map[string]any{"due": true}, nil
	case "admission":
		_, _, err := verbs.ConnectionAdmission(context.Background(), db, recoveryConnection, "test-profile", time.Now())
		return map[string]any{"allowed": err == nil}, nil
	case "system":
		return verbs.SystemStatus(db, nil, verbs.SystemStatusRequest{Connections: []verbs.ConnectionRequirement{{
			ID: recoveryConnection, Name: "Synthetic account", Kind: "codex-oauth", AuthorizationID: recoveryAuthorization,
			Operation: "token_refresh", Roles: []string{"worker"},
		}}})
	case "connection":
		if len(os.Args) != 3 {
			return nil, errors.New("The fixture requires a connection action.")
		}
		switch os.Args[2] {
		case "check-start", "check-cancel":
			var request verbs.ConnectionCheck
			if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
				return nil, err
			}
			if request.ConnectionID != recoveryConnection {
				return nil, errors.New("The fixture connection differs.")
			}
			request.AuthorizationID, request.Operation = recoveryAuthorization, "token_refresh"
			if os.Args[2] == "check-start" {
				return verbs.ConnectionCheckStart(db, nil, request)
			}
			return verbs.ConnectionCheckCancel(db, nil, request)
		case "observe":
			var request verbs.ConnectionObservation
			if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
				return nil, err
			}
			return verbs.ConnectionObserve(db, nil, request)
		case "proxy-observe":
			var request verbs.ProviderAccessObservation
			if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
				return nil, err
			}
			return verbs.ProviderAccessObserve(db, nil, request)
		}
	}
	return nil, errors.New("The fixture command is unavailable.")
}
