package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type AgentConnectionDisconnectRequest struct {
	ConnectionID    string `json:"connection_id"`
	AuthorizationID string `json:"authorization_id"`
	CredentialRef   string `json:"credential_ref,omitempty"`
	Confirm         bool   `json:"confirm"`
}

type AgentConnectionDisconnectResult struct {
	AgentProfileCatalog
	Disconnected    bool   `json:"disconnected"`
	ProviderRevoked bool   `json:"provider_revoked"`
	Message         string `json:"message"`
}

func AgentConnectionDisconnect(ctx context.Context, home string, request AgentConnectionDisconnectRequest) (AgentConnectionDisconnectResult, error) {
	return disconnectAgentConnection(ctx, home, request, revokeConnectionProviderAccess)
}

// AgentConnectionRemoveCredential disconnects the saved credential of a connection when one exists.
func AgentConnectionRemoveCredential(home, id string) error {
	return removeAgentConnectionCredential(home, id, revokeConnectionProviderAccess)
}

func removeAgentConnectionCredential(home, id string, revoke func(context.Context, string, string, string) error) error {
	authorization, err := ReadConnectionAuthorizationIdentity(home, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = disconnectAgentConnection(ctx, home, AgentConnectionDisconnectRequest{ConnectionID: id, AuthorizationID: authorization, Confirm: true}, revoke)
	return err
}

func disconnectAgentConnection(ctx context.Context, home string, request AgentConnectionDisconnectRequest, revoke func(context.Context, string, string, string) error) (AgentConnectionDisconnectResult, error) {
	var result AgentConnectionDisconnectResult
	if !request.Confirm || !validSHA256(request.AuthorizationID) || request.CredentialRef != "" && !validSHA256(request.CredentialRef) {
		return result, fmt.Errorf("Confirm disconnection of the current connection.")
	}
	release, err := AcquireConnectionOwner(ctx, home, request.ConnectionID)
	if err != nil {
		return result, err
	}
	defer release()
	err = withAgentConnectionLockContext(ctx, home, request.ConnectionID, func() error {
		path := filepath.Join(home, "refresh-grants", request.ConnectionID+".json")
		body, err := readSecretSource(path, "connection credential reference")
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer clear(body)
		var reference providerCredentialReference
		if decodeRuntimeAuthJSON(body, &reference) != nil || reference.Version != 1 || reference.Credential != request.ConnectionID || !validSHA256(reference.CredentialRef) {
			return fmt.Errorf("The connection requires host credential migration before disconnection.")
		}
		if reference.AuthorizationID != request.AuthorizationID || request.CredentialRef != "" && reference.CredentialRef != request.CredentialRef {
			return fmt.Errorf("The connection changed. Refresh its status before disconnection.")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := revoke(ctx, home, request.ConnectionID, reference.AuthorizationID); err != nil {
			return err
		}
		if err := DeleteHostSecret(home, "provider", reference.CredentialRef); err != nil {
			return err
		}
		identity := filepath.Join(home, "agent-profile-operations", "connection-"+request.ConnectionID+".identity")
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
	if err != nil {
		return result, err
	}
	result.AgentProfileCatalog, err = AgentProfileList(home)
	if err != nil {
		return result, err
	}
	result.ConnectionID = request.ConnectionID
	result.Disconnected = true
	result.ProviderRevoked = false
	result.Message = "The connection is disconnected from Culler. Provider revocation is unconfirmed. Your separate native sessions remain available."
	return result, nil
}

func revokeConnectionProviderAccess(ctx context.Context, home, connection, authorization string) error {
	if err := revokeConnectionAtSocket(ctx, home, connection, authorization); err != nil {
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
		if err := revokeConnectionAtSocket(ctx, record.SocketHome, connection, authorization); err != nil {
			return err
		}
	}
	return syncDirectory(root)
}

var proxyRegistryEntryPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.json$`)
var proxyTemporaryHomePattern = regexp.MustCompile(`^culler-proxy-[A-Za-z0-9]+$`)

func revokeConnectionAtSocket(ctx context.Context, home, connection, authorization string) error {
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
	body, err := residentAcceptanceExchangeContext(ctx, home, map[string]any{"version": 1, "op": "connection_revoke", "credential": connection, "authorization_id": authorization, "nonce": nonce})
	var response struct {
		Version int    `json:"version"`
		Status  string `json:"status"`
		Nonce   string `json:"nonce"`
		Revoked bool   `json:"revoked"`
	}
	if err != nil || json.Unmarshal(body, &response) != nil || response.Version != 1 || response.Status != "ok" || response.Nonce != nonce || !response.Revoked {
		return fmt.Errorf("Culler could not confirm that active provider requests stopped. Restore host provider access, then retry disconnection.")
	}
	return nil
}
