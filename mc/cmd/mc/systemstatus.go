package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"crypto/sha256"
	"encoding/hex"
	"mc/config"
	"mc/deployment"
	"mc/domain"
	"mc/routing"
	"mc/verbs"
	"path/filepath"
)

func decodeDiagnosticRequest(r io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(r, 16385))
	if err != nil || len(data) > 16384 {
		return verbs.Usagef("The diagnostic request exceeds its size limit.")
	}
	scan := json.NewDecoder(bytes.NewReader(data))
	token, err := scan.Token()
	if err != nil || token != json.Delim('{') {
		return verbs.Usagef("The diagnostic request requires an object.")
	}
	keys := map[string]bool{}
	for scan.More() {
		key, err := scan.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok || keys[name] {
			return verbs.Usagef("The diagnostic request repeats a field.")
		}
		keys[name] = true
		var raw json.RawMessage
		if err := scan.Decode(&raw); err != nil {
			return err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return verbs.Usagef("The diagnostic request is invalid.")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return verbs.Usagef("The diagnostic request contains extra data.")
	}
	return nil
}

func systemSpineCall(action string, request any) (any, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if !shouldDelegateToHelper() {
		return commandSystemSpine(action, bytes.NewReader(body))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", helperContainerName(), "mc", "__system-state", action)
	cmd.Stdin = bytes.NewReader(body)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		var refusal struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(out.Bytes(), &refusal) == nil && refusal.Error.Message != "" {
			return nil, verbs.Domainf("%s", refusal.Error.Message)
		}
		return nil, verbs.Usagef("System state is unavailable.")
	}
	var value any
	if err := json.Unmarshal(out.Bytes(), &value); err != nil {
		return nil, verbs.Usagef("The system status response is invalid.")
	}
	return value, nil
}

func systemRequirements() (verbs.SystemStatusRequest, error) {
	home, err := configuredCanonicalHome()
	if err != nil {
		return verbs.SystemStatusRequest{}, err
	}
	catalog, err := deployment.AgentProfileSystemCatalog(home)
	if err != nil {
		return verbs.SystemStatusRequest{}, err
	}
	cfg, err := config.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return verbs.SystemStatusRequest{}, err
	}
	revision, err := config.RuntimeCatalogDigest(cfg.Runtimes)
	if err != nil {
		return verbs.SystemStatusRequest{}, err
	}
	request := verbs.SystemStatusRequest{Revision: revision, Connections: []verbs.ConnectionRequirement{}}
	for _, connection := range catalog.Connections {
		operation := "connection_access"
		if strings.HasSuffix(connection.Kind, "oauth") {
			operation = "token_refresh"
		}
		auth := "none"
		if connection.Kind != "none" {
			auth, err = deployment.ReadConnectionAuthorizationIdentity(home, connection.ID)
			if err != nil {
				auth = "unavailable"
			}
		}
		roles := []string{}
		profiles := map[string]string{}
		profileRoles := map[string][]string{}
		profileModels := map[string]string{}
		bindings := routing.ConfiguredBindings(cfg.Runtimes)
		for _, profile := range catalog.Profiles {
			if profile.ConnectionID == connection.ID {
				roles = append(roles, profile.Roles...)
				profiles[profile.ID] = routing.ProfileRevision(bindings[profile.ID])
				profileRoles[profile.ID] = profile.Roles
				profileModels[profile.ID] = profile.Model
			}
		}
		request.Connections = append(request.Connections, verbs.ConnectionRequirement{ID: connection.ID, Name: connection.Name, Kind: connection.Kind, AuthorizationID: auth, Operation: operation, Profiles: profiles, ProfileRoles: profileRoles, ProfileModels: profileModels, Roles: roles})
	}
	return request, nil
}

func currentSystemStatus() (any, error) {
	home, err := configuredCanonicalHome()
	if err != nil {
		return nil, err
	}
	// The AgentsView probe runs beside the spine read.
	type viewerFacts struct {
		viewers    verbs.Viewers
		conditions []verbs.SystemCondition
	}
	viewerDone := make(chan viewerFacts, 1)
	go func() {
		viewers, conditions := verbs.ViewerStatus(home, time.Now(), deployment.PingAgentsView)
		viewerDone <- viewerFacts{viewers, conditions}
	}()
	withViewers := func(response map[string]any) (any, error) {
		facts := <-viewerDone
		response["viewers"] = facts.viewers
		conditions, _ := response["conditions"].([]any)
		if conditions == nil {
			conditions = []any{}
		}
		for _, condition := range facts.conditions {
			conditions = append(conditions, condition)
		}
		response["conditions"] = conditions
		return response, nil
	}
	availability := verbs.ResidentAvailability(home, time.Now())
	unavailable := map[string]any{"complete": false, "observed_at": time.Now().UTC().Format(time.RFC3339Nano), "conditions": []any{}, "connections": []any{}, "availability": availability}
	for attempt := 0; attempt < 2; attempt++ {
		before, err := systemRequirements()
		if err != nil {
			return withViewers(unavailable)
		}
		result, err := systemSpineCall("status", before)
		if err != nil {
			return withViewers(unavailable)
		}
		after, err := systemRequirements()
		if err != nil {
			return withViewers(unavailable)
		}
		b, _ := json.Marshal(before)
		a, _ := json.Marshal(after)
		if !bytes.Equal(a, b) {
			continue
		}
		body, _ := json.Marshal(result)
		var response map[string]any
		if json.Unmarshal(body, &response) != nil {
			return withViewers(unavailable)
		}
		availability = verbs.ResidentAvailability(home, time.Now())
		response["availability"] = availability
		response["complete"] = availability.Available && availability.CanCommit
		return withViewers(response)
	}
	return withViewers(unavailable)
}

func commandSystem(args []string, stdin io.Reader) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "system status"); err != nil {
		return nil, err
	}
	if len(args) == 1 && args[0] == "probe" {
		return systemSpineCall("probe", map[string]any{})
	}
	if len(args) == 1 && args[0] == "status" {
		return currentSystemStatus()
	}
	if len(args) > 0 && args[0] == "diagnostics" {
		fs := newFlags("system diagnostics")
		project := fs.String("project", "", "Project")
		connection := fs.String("connection", "", "connection")
		run := fs.String("run", "", "run")
		attempt := fs.String("attempt", "", "reply attempt")
		after := fs.Int64("after", 0, "history cursor")
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		return systemSpineCall("diagnostics", map[string]any{"connection": *connection, "run": *run, "attempt": *attempt, "after": *after, "project": *project})
	}
	if len(args) > 0 && args[0] == "resume-infrastructure" {
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return nil, verbs.Usagef("usage: mc system resume-infrastructure <project> --request <id>")
		}
		fs := newFlags("resume Project infrastructure")
		request := fs.String("request", "", "request identity")
		if err := parse(fs, args[2:]); err != nil {
			return nil, err
		}
		return systemSpineCall("resume-infrastructure", map[string]any{"project": args[1], "check_id": *request})
	}
	if len(args) > 0 && args[0] == "resolve-proposals" {
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return nil, verbs.Usagef("usage: mc system resolve-proposals <project> --decision <id> --reason <resolution>")
		}
		fs := newFlags("resolve proposal decision")
		decision := fs.Int64("decision", 0, "shared proposal decision")
		reason := fs.String("reason", "", "decision resolution")
		if err := parse(fs, args[2:]); err != nil {
			return nil, err
		}
		return systemSpineCall("resolve-proposals", map[string]any{"project": args[1], "decision": *decision, "reason": *reason})
	}

	return nil, verbs.Usagef("The system action is invalid.")
}

func commandConnection(args []string, stdin io.Reader) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "connection check"); err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, verbs.Usagef("A connection action is required.")
	}
	if args[0] == "retry" {
		var a verbs.ConnectionCheck
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		required, err := systemRequirements()
		if err != nil {
			return nil, err
		}
		for _, connection := range required.Connections {
			if connection.ID == a.ConnectionID {
				a.AuthorizationID = connection.AuthorizationID
				a.Operation = connection.Operation
				return systemSpineCall("retry", a)
			}
		}
		return nil, verbs.Domainf("The connection is unavailable.")
	}

	if args[0] == "observe" {
		var a verbs.ConnectionObservation
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return systemSpineCall("observe", a)
	}
	if args[0] == "proxy-lifetimes" {
		var a struct {
			Launches []verbs.ProviderLaunchIdentity `json:"launches"`
		}
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return systemSpineCall("proxy-lifetimes", a)
	}
	if args[0] == "proxy-observe" {
		var a verbs.ProviderAccessObservation
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		a.CurrentAuthorizationRevision, a.CurrentCredentialRef, a.CurrentCredentialType = "", "", ""
		if a.Connection != "" {
			home, err := configuredCanonicalHome()
			if err != nil {
				return nil, err
			}
			current, err := deployment.ReadProviderCredentialIdentity(home, a.Connection)
			if err == nil {
				a.CurrentAuthorizationRevision, a.CurrentCredentialRef = current.AuthorizationID, current.CredentialRef
				a.CurrentCredentialType = current.Type
			}
		}
		return systemSpineCall("proxy-observe", a)
	}
	if args[0] == "check-cancel" {
		var a verbs.ConnectionCheck
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return systemSpineCall("check-cancel", a)
	}
	if args[0] == "check-start" {
		var a verbs.ConnectionCheck
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		required, err := systemRequirements()
		if err != nil {
			return nil, err
		}
		for _, connection := range required.Connections {
			if connection.ID == a.ConnectionID {
				a.AuthorizationID = connection.AuthorizationID
				if connection.Kind != "none" {
					home, err := configuredCanonicalHome()
					if err != nil {
						return nil, err
					}
					identity, err := deployment.AgentConnectionAuthorizationIdentity(home, connection.ID)
					if err == nil && identity != "" {
						a.AuthorizationID = identity
					}
				}
				if a.ProfileID == "" {
					a.Operation = connection.Operation
				}
				if a.CheckID == "" {
					a.CheckID = domain.DiagnosticID()
				}
				return systemSpineCall("check-start", a)
			}
		}
		return nil, verbs.Domainf("The connection is absent from the configuration.")
	}
	return nil, verbs.Usagef("The connection action is invalid.")
}

type homieLaunchRequest struct {
	Session         string `json:"session"`
	Launch          string `json:"launch"`
	Connection      string `json:"connection"`
	Authorization   string `json:"authorization"`
	Profile         string `json:"profile"`
	ProfileRevision string `json:"profile_revision"`
}

func commandSystemSpine(action string, stdin io.Reader) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "system state"); err != nil {
		return nil, err
	}
	switch action {
	case "proxy-lifetimes":
		var a struct {
			Launches []verbs.ProviderLaunchIdentity `json:"launches"`
		}
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.ProviderAccessLifetimes(db, id, a.Launches) })
	case "proxy-observe":
		var a verbs.ProviderAccessObservation
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.ProviderAccessObserve(db, id, a) })
	case "launch-check":
		var a struct {
			Run          string                    `json:"run"`
			Requirements verbs.SystemStatusRequest `json:"requirements"`
		}
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.RunLaunchCheck(db, id, a.Run, a.Requirements) })
	case "homie-launch-check":
		var a homieLaunchRequest
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.HomieLaunchCheck(db, id, a.Session, a.Launch, a.Connection, a.Authorization, a.Profile, a.ProfileRevision)
		})
	case "probe":
		return withSpine(func(db *sql.DB) (any, error) {
			conn, err := db.Conn(context.Background())
			if err != nil {
				return nil, err
			}
			defer conn.Close()
			if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
				return nil, err
			}
			_, err = conn.ExecContext(context.Background(), "ROLLBACK")
			return map[string]any{"can_commit": err == nil}, err
		})
	case "verified":
		var a verbs.ConnectionObservation
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.ConnectionVerified(db, id, a) })

	case "status":
		var a verbs.SystemStatusRequest
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.SystemStatus(db, id, a) })
	case "retry":
		var a verbs.ConnectionCheck
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.ConnectionRetry(db, id, a) })

	case "check-cancel":
		var a verbs.ConnectionCheck
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.ConnectionCheckCancel(db, id, a) })
	case "check-start":
		var a verbs.ConnectionCheck
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.ConnectionCheckStart(db, id, a) })
	case "observe":
		var a verbs.ConnectionObservation
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.ConnectionObserve(db, id, a) })
	case "diagnostics":
		var a struct {
			Project    string `json:"project"`
			Connection string `json:"connection"`
			Run        string `json:"run"`
			Attempt    string `json:"attempt"`
			After      int64  `json:"after"`
		}
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) {
			return verbs.DiagnosticHistory(db, id, a.Connection, a.Run, a.Attempt, a.After, a.Project)
		})
	case "resolve-proposals":
		var a struct {
			Project  string `json:"project"`
			Decision int64  `json:"decision"`
			Reason   string `json:"reason"`
		}
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.ProposalDecisionResolve(db, id, a.Project, a.Decision, a.Reason)
		})
	case "resume-infrastructure":
		var a struct {
			Project         string `json:"project"`
			Connection      string `json:"connection"`
			Profile         string `json:"profile"`
			CheckID         string `json:"check_id"`
			AuthorizationID string `json:"authorization_id"`
			ProfileRevision string `json:"profile_revision"`
		}
		if err := decodeDiagnosticRequest(stdin, &a); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.ProjectInfrastructureResume(db, id, a.Project, a.Connection, a.Profile, verbs.ConnectionCheck{CheckID: a.CheckID, AuthorizationID: a.AuthorizationID, ProfileRevision: a.ProfileRevision})
		})
	}
	return nil, fmt.Errorf("The system state action is invalid.")
}

func prepareModelVerification(connectionID, authorizationID string) (func() error, error) {
	required, err := systemRequirements()
	if err != nil {
		return nil, err
	}
	for _, connection := range required.Connections {
		if connection.ID != connectionID {
			continue
		}
		if connection.AuthorizationID != authorizationID {
			return nil, verbs.Domainf("The connection authorization changed. Retry the model test.")
		}
		if connection.Operation != "connection_access" || connection.Kind == "none" {
			return func() error { return nil }, nil
		}
		check := verbs.ConnectionCheck{ConnectionID: connectionID, AuthorizationID: authorizationID,
			Operation: connection.Operation, CheckID: domain.DiagnosticID()}
		if _, err := systemSpineCall("check-start", check); err != nil {
			return nil, err
		}
		return func() error {
			_, err := systemSpineCall("check-cancel", check)
			return err
		}, nil
	}
	return nil, verbs.Domainf("The connection is absent from the configuration.")
}

func recordModelVerification(home, operation string) error {
	proof, auth, err := deployment.AgentProfileDiagnosticResult(home, operation)
	if err != nil || proof == nil {
		return err
	}
	if proof.Status != "succeeded" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	release, err := deployment.AcquireConnectionOwner(ctx, home, proof.ConnectionID)
	if err != nil {
		return err
	}
	defer release()
	proof, auth, err = deployment.AgentProfileDiagnosticResult(home, operation)
	if err != nil || proof == nil || proof.Status != "succeeded" {
		return err
	}
	required, err := systemRequirements()
	if err != nil {
		return err
	}
	binding, profileRevision := "", ""
	if proof.Profile != nil {
		tested, resolved, err := deployment.ResolveAgentProfile(home, *proof.Profile)
		if err != nil {
			return err
		}
		binding, profileRevision = resolved, routing.ProfileRevision(tested.Bindings[resolved])
	}
	if auth == "" {
		auth = "unavailable"
	}
	for _, connection := range required.Connections {
		if connection.ID != proof.ConnectionID {
			continue
		}
		if connection.AuthorizationID != auth {
			return nil
		}
		scopes := []verbs.ConnectionCheck{}
		if proof.Kind == "authorization" {
			scopes = append(scopes, verbs.ConnectionCheck{ConnectionID: connection.ID, AuthorizationID: auth, Operation: connection.Operation})
		}
		if connection.Operation != "token_refresh" && proof.Status == "succeeded" {
			scopes = append(scopes, verbs.ConnectionCheck{ConnectionID: connection.ID, AuthorizationID: auth, Operation: connection.Operation})
		}
		if proof.Profile != nil {
			scopes = append(scopes, verbs.ConnectionCheck{ConnectionID: connection.ID, AuthorizationID: auth, Operation: "model_access", ProfileID: binding, ProfileRevision: profileRevision})
		}
		for _, scope := range scopes {
			sum := sha256.Sum256([]byte(operation + scope.Operation + scope.ProfileID))
			event := hex.EncodeToString(sum[:16])
			check := sha256.Sum256([]byte(operation))
			scope.CheckID = hex.EncodeToString(check[:16])
			_, err := systemSpineCall("verified", verbs.ConnectionObservation{ConnectionCheck: scope, EventID: event, Outcome: "ready", ObservedAt: proof.FinishedAt})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func connectionAccessState(required verbs.SystemStatusRequest, connection string) (string, error) {
	result, err := systemSpineCall("status", required)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	var status verbs.SystemStatusResult
	if err := json.Unmarshal(body, &status); err != nil {
		return "", err
	}
	for _, condition := range status.Connections {
		if condition.ConnectionID == connection && condition.ProfileID == "" {
			return condition.State, nil
		}
	}
	return "", nil
}

func composeSystemDoctor(result any, started string) any {
	body, err := json.Marshal(result)
	if err != nil {
		return result
	}
	var report map[string]any
	if json.Unmarshal(body, &report) != nil {
		return result
	}
	report["started_at"] = started
	report["completed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	report["deployment_ok"] = report["ok"]
	report["deployment_complete"] = true
	if findings, ok := report["findings"].([]any); ok {
		for _, value := range findings {
			if finding, ok := value.(map[string]any); ok {
				finding["checked_at"] = report["completed_at"]
				detail, _ := finding["detail"].(string)
				if strings.Contains(detail, "not checked:") || (finding["status"] == "deferred" && finding["check"] != "runtime-auth") {
					report["deployment_complete"] = false
					report["deployment_ok"] = false
					report["ok"] = false
				}
			}
		}
	}
	operational, err := currentSystemStatus()
	if err != nil {
		report["operational"] = map[string]any{"complete": false}
		report["ok"] = false
		return report
	}
	report["operational"] = operational
	snapshot, ok := operational.(map[string]any)
	if !ok || snapshot["complete"] != true {
		report["ok"] = false
	} else if conditions, ok := snapshot["conditions"].([]any); !ok || len(conditions) > 0 {
		report["ok"] = false
	}
	if findings, ok := report["findings"].([]any); ok {
		for _, value := range findings {
			finding, ok := value.(map[string]any)
			if !ok || finding["check"] != "runtime-auth" {
				continue
			}
			finding["status"] = "deferred"
			finding["detail"] = "Connection access is unverified. The optional model test remains separate."
			if snapshot != nil {
				connections, _ := snapshot["connections"].([]any)
				verified, observed := true, false
				for _, value := range connections {
					connection, ok := value.(map[string]any)
					if !ok {
						verified = false
						continue
					}
					roles, _ := connection["roles"].([]any)
					if len(roles) == 0 && connection["state"] == "unknown" && (connection["diagnostic_id"] == nil || connection["diagnostic_id"] == float64(0)) {
						continue
					}
					observed = true
					if connection["state"] != "ready" {
						verified = false
						if connection["state"] == "unknown" {
							continue
						}
						finding["status"] = "fail"
						finding["detail"] = "A connection requires attention. Open System health for its cause and repair action."
					}
				}
				if verified && observed {
					finding["status"] = "ok"
					finding["detail"] = "Current connection access is verified. Optional model tests retain their own results."
				}
			}
		}
	}

	report["completed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	return report
}
