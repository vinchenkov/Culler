package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	operatorconfig "mc/config"
)

type AgentConnectionDeleteRequest struct {
	ConnectionID    string `json:"connection_id"`
	AuthorizationID string `json:"authorization_id"`
	Confirm         bool   `json:"confirm"`
}

type AgentConnectionDeleteResult struct {
	AgentProfileCatalog
	Deleted         bool   `json:"deleted"`
	ProviderRevoked bool   `json:"provider_revoked"`
	Message         string `json:"message"`
}

type connectionRevoker func(ctx context.Context, home, connection string) error

// AgentConnectionDelete removes a connection, its saved credential, and its agent profiles.
func AgentConnectionDelete(ctx context.Context, home string, request AgentConnectionDeleteRequest) (AgentConnectionDeleteResult, error) {
	return deleteAgentConnection(ctx, home, request, revokeConnectionProviderAccess)
}

// AgentConnectionRemoveCredential removes the saved credential of a connection when one exists.
func AgentConnectionRemoveCredential(home, id string) error {
	return removeAgentConnectionCredential(home, id, revokeConnectionProviderAccess)
}

func removeAgentConnectionCredential(home, id string, revoke connectionRevoker) error {
	authorization, err := ReadConnectionAuthorizationIdentity(home, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return removeConnectionCredential(ctx, home, id, authorization, revoke)
}

func deleteAgentConnection(ctx context.Context, home string, request AgentConnectionDeleteRequest, revoke connectionRevoker) (AgentConnectionDeleteResult, error) {
	var result AgentConnectionDeleteResult
	if !request.Confirm || !validSHA256(request.AuthorizationID) {
		return result, fmt.Errorf("Confirm deletion of the current connection.")
	}
	path := filepath.Join(home, "config.toml")
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		return result, err
	}
	if err := deletableConnection(home, cfg, request.ConnectionID); err != nil {
		return result, err
	}
	if err := removeConnectionCredential(ctx, home, request.ConnectionID, request.AuthorizationID, revoke); err != nil {
		return result, err
	}
	if _, err := operatorconfig.UpdateAtomic(path, func(cfg *operatorconfig.Config) error {
		if err := deletableConnection(home, *cfg, request.ConnectionID); err != nil {
			return err
		}
		removeConnectionConfiguration(cfg, request.ConnectionID)
		return cfg.Validate()
	}); err != nil {
		return result, err
	}
	result.AgentProfileCatalog, err = AgentProfileList(home)
	if err != nil {
		return result, err
	}
	result.Deleted = true
	result.Message = "The connection is deleted. Provider revocation is unconfirmed. Your separate native sessions remain available."
	return result, nil
}

// deletableConnection refuses an unknown connection and a connection with an agent profile that a role uses.
func deletableConnection(home string, cfg operatorconfig.Config, id string) error {
	if _, ok := cfg.Runtimes.Credentials[id]; !ok {
		return fmt.Errorf("The connection is unavailable. Refresh the connections.")
	}
	roles, err := agentProfileRoles(home, cfg)
	if err != nil {
		return fmt.Errorf("The role assignments are unreadable, so Culler cannot confirm that no role uses this connection.")
	}
	assigned := []string{}
	for profile, binding := range cfg.Runtimes.Bindings {
		if binding.Credential == id {
			for _, role := range roles[profile] {
				assigned = append(assigned, strings.ToUpper(role[:1])+role[1:])
			}
		}
	}
	if len(assigned) == 0 {
		return nil
	}
	sort.Strings(assigned)
	return fmt.Errorf("Roles use this connection: %s. Assign these roles to another agent profile, then delete the connection.", strings.Join(slices.Compact(assigned), ", "))
}

// removeConnectionConfiguration removes a connection and its agent profiles. An entry of the
// configuration template returns to its template definition, because setup signs in through it.
func removeConnectionConfiguration(cfg *operatorconfig.Config, id string) {
	template := operatorconfig.Default().Runtimes
	for profile, binding := range cfg.Runtimes.Bindings {
		if binding.Credential != id {
			continue
		}
		cfg.Runtimes.EnabledBindings = slices.DeleteFunc(cfg.Runtimes.EnabledBindings, func(enabled string) bool { return enabled == profile })
		reference, ok := template.Bindings[profile]
		if _, defined := cfg.Runtimes.Credentials[reference.Credential]; ok && (defined || reference.Credential == id) {
			cfg.Runtimes.Bindings[profile] = reference
		} else {
			delete(cfg.Runtimes.Bindings, profile)
		}
	}
	if reference, ok := template.Credentials[id]; ok {
		cfg.Runtimes.Credentials[id] = reference
	} else {
		delete(cfg.Runtimes.Credentials, id)
	}
}

// removeConnectionCredential stops the active requests of the current authorization, then deletes its saved credential.
func removeConnectionCredential(ctx context.Context, home, id, authorization string, revoke connectionRevoker) error {
	release, err := AcquireConnectionOwner(ctx, home, id)
	if err != nil {
		return err
	}
	defer release()
	return withAgentConnectionLockContext(ctx, home, id, func() error {
		path := filepath.Join(home, "refresh-grants", id+".json")
		body, err := readSecretSource(path, "connection credential reference")
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer clear(body)
		var reference providerCredentialReference
		if decodeRuntimeAuthJSON(body, &reference) != nil || reference.Version != 1 || reference.Credential != id || !validSHA256(reference.CredentialRef) {
			return fmt.Errorf("The connection requires host credential migration before Culler can remove its credential.")
		}
		if reference.AuthorizationID != authorization {
			return fmt.Errorf("The connection changed. Refresh the connections, then retry.")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := revoke(ctx, home, id); err != nil {
			return err
		}
		if err := DeleteHostSecret(home, "provider", reference.CredentialRef); err != nil {
			return err
		}
		identity := filepath.Join(home, "agent-profile-operations", "connection-"+id+".identity")
		if err := os.Remove(identity); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := syncDirectory(filepath.Dir(identity)); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(path))
	})
}

func revokeConnectionProviderAccess(ctx context.Context, home, connection string) error {
	if err := revokeConnectionAtSocket(ctx, home, connection); err != nil {
		return err
	}
	root := filepath.Join(home, "health", "provider-proxies")
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := requireOwnerOnlyDirectory(root, "host proxy registry"); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	temporary, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !proxyRegistryEntryPattern.MatchString(entry.Name()) {
			return fmt.Errorf("The host proxy registry contains an invalid entry.")
		}
		path := filepath.Join(root, entry.Name())
		body, err := readSecretSource(path, "host proxy registry entry")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var record struct {
			SocketHome string `json:"socket_home"`
		}
		if decodeRuntimeAuthJSON(body, &record) != nil || filepath.Dir(record.SocketHome) != temporary || !proxyTemporaryHomePattern.MatchString(filepath.Base(record.SocketHome)) {
			return fmt.Errorf("The host proxy registry destination is invalid.")
		}
		if _, err := os.Lstat(record.SocketHome); os.IsNotExist(err) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		} else if err != nil {
			return err
		}
		if err := requireOwnerOnlyDirectory(record.SocketHome, "temporary host proxy directory"); err != nil {
			return err
		}
		canonical, err := filepath.EvalSymlinks(record.SocketHome)
		if err != nil || canonical != record.SocketHome {
			return fmt.Errorf("The host proxy registry destination changed.")
		}
		if _, err := os.Lstat(filepath.Join(record.SocketHome, "health", "provider-acceptance.sock")); os.IsNotExist(err) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		} else if err != nil {
			return err
		}
		if err := revokeConnectionAtSocket(ctx, record.SocketHome, connection); err != nil {
			return err
		}
	}
	return syncDirectory(root)
}

var proxyRegistryEntryPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.json$`)
var proxyTemporaryHomePattern = regexp.MustCompile(`^culler-proxy-[A-Za-z0-9]+$`)

func revokeConnectionAtSocket(ctx context.Context, home, connection string) error {
	path := filepath.Join(home, "health", "provider-acceptance.sock")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := requireOwnerOnlyDirectory(filepath.Dir(path), "host proxy socket directory"); err != nil {
		return err
	}
	nonce, err := residentAcceptanceNonce()
	if err != nil {
		return err
	}
	body, err := residentAcceptanceExchangeContext(ctx, home, map[string]any{"version": 1, "op": "connection_revoke", "credential": connection, "nonce": nonce})
	var response struct {
		Version int    `json:"version"`
		Status  string `json:"status"`
		Nonce   string `json:"nonce"`
		Revoked bool   `json:"revoked"`
	}
	if err != nil || json.Unmarshal(body, &response) != nil || response.Version != 1 || response.Status != "ok" || response.Nonce != nonce || !response.Revoked {
		return fmt.Errorf("Culler could not confirm that active provider requests stopped. Restore host provider access, then retry.")
	}
	return nil
}
