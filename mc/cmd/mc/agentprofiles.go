package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	"mc/config"
	"mc/deployment"
	"mc/routing"
	"mc/verbs"
)

func commandAgentProfile(args []string, stdin io.Reader) (result any, err error) {
	defer func() {
		var usage *verbs.UsageError
		var domain *verbs.DomainError
		if err != nil && !errors.As(err, &usage) && !errors.As(err, &domain) {
			err = verbs.Domainf("%s", err)
		}
	}()
	identity, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(identity, "agent profile configuration"); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, verbs.Usagef("An agent profile action is required.")
	}
	if (args[0] == "credential-read" || args[0] == "credential-renew" || args[0] == "proxy-tls" || args[0] == "credential-migrate" || args[0] == "connection-disconnect") && !deployment.HostCredentialCommandsAvailable() {
		return nil, verbs.Domainf("Host credentials require the macOS host.")
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return nil, err
	}
	if args[0] == "operation-run" {
		flags := newFlags("agent profile operation")
		workerHome := flags.String("home", "", "the operator home")
		id := flags.String("id", "", "the operation identifier")
		if err := parse(flags, args[1:]); err != nil {
			return nil, err
		}
		if *workerHome != home || *id == "" || flags.NArg() != 0 {
			return nil, verbs.Usagef("The operation target is invalid.")
		}
		verified := func(connection string) (bool, error) {
			required, err := systemRequirements()
			if err != nil {
				return false, err
			}
			state, err := connectionAccessState(required, connection)
			return state == "ready", err
		}
		image, err := configuredProductionImage()
		if err != nil {
			return nil, err
		}
		if err := deployment.RunAgentProfileOperation(home, *id, image, verified, prepareModelVerification); err != nil {
			return nil, verbs.Domainf("%s", err)
		}
		if err := recordModelVerification(home, *id); err != nil {
			return nil, err
		}
		return deployment.GetAgentProfileOperation(home, *id)
	}
	if len(args) != 1 {
		return nil, verbs.Usagef("The agent profile action has unexpected arguments.")
	}
	decode := func(value any) error {
		body, err := io.ReadAll(io.LimitReader(stdin, 64*1024+1))
		if err != nil || len(body) > 64*1024 {
			return verbs.Usagef("The agent profile request is invalid.")
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil {
			return verbs.Usagef("The agent profile request is invalid.")
		}
		if args[0] == "connection-save" || args[0] == "save" {
			for field := range fields {
				if strings.EqualFold(field, "base_url") || strings.EqualFold(field, "token") || (args[0] == "connection-save" && strings.EqualFold(field, "kind")) {
					return verbs.Usagef("The agent profile request contains an unsupported connection field.")
				}
			}
		}
		if args[0] == "operation-start" {
			for field, raw := range fields {
				if !strings.EqualFold(field, "profile") {
					continue
				}
				var profileFields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &profileFields); err != nil {
					return verbs.Usagef("The agent profile request is invalid.")
				}
				for profileField := range profileFields {
					if strings.EqualFold(profileField, "base_url") {
						return verbs.Usagef("The agent profile request contains an unsupported connection field.")
					}
				}
			}
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(value); err != nil {
			return verbs.Usagef("The agent profile request is invalid.")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return verbs.Usagef("The agent profile request contains extra data.")
		}
		return nil
	}
	switch args[0] {
	case "credential-migrate":
		migrated, err := deployment.MigrateProviderCredentialStore(home)
		if err != nil {
			return nil, err
		}
		return map[string]any{"migrated": migrated}, nil
	case "credential-read":
		var request struct {
			ConnectionID string `json:"connection_id"`
		}
		if err := decode(&request); err != nil {
			return nil, err
		}
		return deployment.ReadProviderCredential(home, request.ConnectionID)
	case "credential-renew":
		var request deployment.ProviderCredentialRenewal
		if err := decode(&request); err != nil {
			return nil, err
		}
		if err := deployment.RenewProviderCredential(home, request); err != nil {
			return nil, err
		}
		return map[string]any{"updated": true}, nil
	case "proxy-tls":
		return deployment.EnsureProviderProxyTLS(home, time.Now())
	case "test-runtime-authorities":
		_, enabled := routing.ActiveRegistry()
		return map[string]any{"enabled": enabled}, nil
	case "grant-rotate":
		var request deployment.AgentConnectionRotation
		if err := decode(&request); err != nil {
			return nil, err
		}
		if err := deployment.RotateAgentConnectionGrant(home, request); err != nil {
			return nil, err
		}
		return map[string]any{"updated": true}, nil
	case "list":
		return deployment.AgentProfileList(home)
	case "models":
		var request deployment.AgentModelListRequest
		if err := decode(&request); err != nil {
			return nil, err
		}
		return deployment.AgentConnectionModels(home, request)
	case "runtime-catalog":
		cfg, err := config.Load(filepath.Join(home, "config.toml"))
		if err != nil {
			return nil, verbs.Domainf("The runtime configuration is unavailable.")
		}
		deployment.RefreshRetainedOpenCodeModels(home, cfg)
		catalog := map[string]deployment.ResidentRuntimeBinding{}
		for id, spec := range routing.ConfiguredBindings(cfg.Runtimes) {
			catalog[id] = deployment.ResidentRuntimeBinding{Harness: spec.Harness, Credential: spec.Credential,
				Kind: cfg.Runtimes.Credentials[spec.Credential].Kind, InferenceURL: cfg.Runtimes.Credentials[spec.Credential].InferenceURL, Model: spec.Model,
				Channel:  spec.Channel,
				TokenURL: spec.TokenURL, ClientID: spec.ClientID}
		}
		for id, row := range catalog {
			source := cfg.Runtimes.Credentials[row.Credential]
			if source.Kind == config.CredentialKindOpenCodeAPIKey {
				if provider, ok := deployment.OpenCodeProvider(home, source.CatalogEntry); ok {
					row.OpenCode = &provider
				}
				catalog[id] = row
			}
		}
		connections := map[string]deployment.ResidentRuntimeBinding{}

		for id, source := range cfg.Runtimes.Credentials {
			connections[id] = deployment.ResidentRuntimeBinding{Harness: source.Harness(), Credential: id, Channel: source.Channel(), Kind: source.Kind, InferenceURL: source.InferenceURL, TokenURL: source.TokenURL, ClientID: source.ClientID}
			if source.Kind == config.CredentialKindOpenCodeAPIKey {
				row := connections[id]
				if provider, ok := deployment.OpenCodeProvider(home, source.CatalogEntry); ok {
					row.OpenCode = &provider
				}
				connections[id] = row
			}
			for _, profileID := range cfg.Runtimes.EnabledBindings {
				if cfg.Runtimes.Bindings[profileID].Credential == id {
					row := connections[id]
					row.Required = true
					connections[id] = row
					break
				}
			}

		}
		return map[string]any{"bindings": catalog, "connections": connections}, nil
	case "save":
		var request deployment.AgentProfileRequest
		if err := decode(&request); err != nil {
			return nil, err
		}
		return deployment.AgentProfileSave(home, request)
	case "connection-save":
		var request deployment.AgentConnectionRequest
		if err := decode(&request); err != nil {
			return nil, err
		}
		return deployment.AgentConnectionSave(home, request)
	case "connection-disconnect":
		var request deployment.AgentConnectionDisconnectRequest
		if err := decode(&request); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		return deployment.AgentConnectionDisconnect(ctx, home, request)
	case "operation-start":
		var request deployment.AgentProfileOperationRequest
		if err := decode(&request); err != nil {
			return nil, err
		}
		return deployment.StartAgentProfileOperation(home, request)
	case "operation-status", "operation-cancel":
		var request struct {
			OperationID string `json:"operation_id"`
		}
		if err := decode(&request); err != nil {
			return nil, err
		}
		if args[0] == "operation-cancel" {
			return deployment.CancelAgentProfileOperation(home, request.OperationID)
		}
		status, err := deployment.GetAgentProfileOperation(home, request.OperationID)
		if err != nil {
			return nil, err
		}
		if err := recordModelVerification(home, request.OperationID); err != nil {
			return nil, err
		}
		return status, nil
	}
	return nil, verbs.Usagef("The agent profile action is unknown.")
}
