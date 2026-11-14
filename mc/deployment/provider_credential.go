package deployment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	operatorconfig "mc/config"
)

// ProviderCredentialRecord belongs only to host memory and the Keychain.
type ProviderCredentialRecord struct {
	Version            int    `json:"version,omitempty"`
	Type               string `json:"type,omitempty"`
	Credential         string `json:"credential"`
	Channel            string `json:"channel"`
	APIKey             string `json:"api_key,omitempty"`
	TokenURL           string `json:"token_url,omitempty"`
	ClientID           string `json:"client_id,omitempty"`
	AccessToken        string `json:"access_token,omitempty"`
	IDToken            string `json:"id_token,omitempty"`
	RefreshToken       string `json:"refresh_token,omitempty"`
	AccountID          string `json:"account_id,omitempty"`
	Scope              string `json:"scope,omitempty"`
	ExpiresAtMS        int64  `json:"expires_at_ms,omitempty"`
	RefreshExpiresAtMS int64  `json:"refresh_expires_at_ms,omitempty"`
}

// ProviderCredentialAccess binds a secret and its two revisions in one read.
type ProviderCredentialAccess struct {
	Record          ProviderCredentialRecord `json:"record"`
	AuthorizationID string                   `json:"authorization_id"`
	CredentialRef   string                   `json:"credential_ref"`
}

type ProviderCredentialRenewal struct {
	ConnectionID    string                   `json:"connection_id"`
	AuthorizationID string                   `json:"authorization_id"`
	CredentialRef   string                   `json:"credential_ref"`
	Record          ProviderCredentialRecord `json:"record"`
}

type providerCredentialReference struct {
	Version         int    `json:"version"`
	Credential      string `json:"credential"`
	Channel         string `json:"channel"`
	Type            string `json:"type"`
	CredentialRef   string `json:"credential_ref"`
	AuthorizationID string `json:"authorization_id"`
}

func normalizeProviderCredential(id string, value any) (ProviderCredentialRecord, error) {
	var record ProviderCredentialRecord
	body, err := json.Marshal(value)
	if err != nil {
		return record, err
	}
	defer clear(body)
	if decodeRuntimeAuthJSON(body, &record) != nil || record.Credential != id || (record.Channel != "codex" && record.Channel != "claude" && record.Channel != "opencode") {
		return record, fmt.Errorf("The connection credential is invalid.")
	}
	if _, err := agentProfileID(id, ""); err != nil {
		return record, err
	}
	if record.Version != 0 && record.Version != 1 {
		return record, fmt.Errorf("The connection credential version is unsupported.")
	}
	if record.Type == "" {
		if record.APIKey != "" {
			record.Type = "api_key"
		} else {
			record.Type = "account"
		}
	}
	switch record.Type {
	case "api_key":
		if strings.TrimSpace(record.APIKey) != record.APIKey || record.APIKey == "" || len(record.APIKey) > 16384 || strings.ContainsAny(record.APIKey, "\x00\r\n\t ") || record.RefreshToken != "" || record.AccessToken != "" || record.IDToken != "" || record.TokenURL != "" || record.ClientID != "" || record.ExpiresAtMS != 0 || record.RefreshExpiresAtMS != 0 || record.Scope != "" || record.AccountID != "" {
			return record, fmt.Errorf("The API key credential is invalid.")
		}
	case "account":
		if record.Channel == "opencode" || record.RefreshToken == "" || record.APIKey != "" || record.ExpiresAtMS < 0 || record.RefreshExpiresAtMS < 0 {
			return record, fmt.Errorf("The account credential is invalid.")
		}
	default:
		return record, fmt.Errorf("The connection credential type is unsupported.")
	}
	record.Version = 1
	return record, nil
}

func readProviderReference(home, id string) (providerCredentialReference, []byte, error) {
	var reference providerCredentialReference
	body, err := readSecretSource(filepath.Join(home, "refresh-grants", id+".json"), "connection credential reference")
	if err != nil {
		return reference, nil, err
	}
	var probe struct {
		CredentialRef string `json:"credential_ref"`
	}
	if json.Unmarshal(body, &probe) != nil {
		clear(body)
		return reference, nil, fmt.Errorf("The connection credential reference is invalid.")
	}
	if probe.CredentialRef == "" {
		return reference, body, nil
	}
	if decodeRuntimeAuthJSON(body, &reference) != nil || reference.Version != 1 || reference.Credential != id || !validSHA256(reference.CredentialRef) || !validSHA256(reference.AuthorizationID) {
		clear(body)
		return reference, nil, fmt.Errorf("The connection credential reference is invalid.")
	}
	clear(body)
	body, err = ReadHostSecret(home, "provider", reference.CredentialRef)
	return reference, body, err
}

func readConnectionCredentialBody(home, id string) ([]byte, error) {
	_, body, err := readProviderReference(home, id)
	return body, err
}

func writeProviderReference(home string, reference providerCredentialReference) error {
	root := filepath.Join(home, "refresh-grants")
	if err := ensureOwnerDirectory(root, "connection credential references"); err != nil {
		return err
	}
	body, err := json.Marshal(reference)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(root, ".connection-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(root, reference.Credential+".json"))
	}
	if err == nil {
		err = syncDirectory(root)
	}
	return err
}

func storeProviderCredential(home, id, authorizationID string, value any) error {
	record, err := normalizeProviderCredential(id, value)
	if err != nil {
		return err
	}
	if !validSHA256(authorizationID) {
		return fmt.Errorf("The connection authorization identity is invalid.")
	}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	defer clear(body)
	reference, err := WriteHostSecret(home, "provider", body)
	if err != nil {
		return err
	}
	previous, previousBody, _ := readProviderReference(home, id)
	clear(previousBody)
	metadata := providerCredentialReference{Version: 1, Credential: id, Channel: record.Channel, Type: record.Type, CredentialRef: reference, AuthorizationID: authorizationID}
	if err := writeProviderReference(home, metadata); err != nil {
		// A failed directory sync can follow publication. Both entries remain recoverable.
		return err
	}
	if previous.CredentialRef != "" {
		_ = DeleteHostSecret(home, "provider", previous.CredentialRef)
	}
	return nil
}

// ReadProviderCredential returns one current secret with its authorization and credential references.
func ReadProviderCredential(home, id string) (ProviderCredentialAccess, error) {
	var result ProviderCredentialAccess
	err := withAgentConnectionLock(home, id, func() error {
		reference, body, err := readProviderReference(home, id)
		if err != nil {
			return err
		}
		defer clear(body)
		record, err := normalizeProviderCredential(id, json.RawMessage(body))
		if err != nil {
			return err
		}
		if reference.CredentialRef == "" {
			identity, err := agentConnectionIdentityLocked(home, id)
			if err != nil {
				return err
			}
			if err := storeProviderCredential(home, id, identity, record); err != nil {
				return err
			}
			var publishedBody []byte
			reference, publishedBody, err = readProviderReference(home, id)
			clear(publishedBody)
			if err != nil {
				return err
			}
		}
		cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
		if err != nil {
			return err
		}
		source, ok := cfg.Runtimes.Credentials[id]
		if !ok || source.Channel() != record.Channel || agentAPIKeyKind(source.Kind) != (record.Type == "api_key") || (record.Type == "account" && (record.TokenURL != source.TokenURL || record.ClientID != source.ClientID)) {
			return fmt.Errorf("The credential differs from its connection configuration.")
		}
		result = ProviderCredentialAccess{Record: record, AuthorizationID: reference.AuthorizationID, CredentialRef: reference.CredentialRef}
		return nil
	})
	return result, err
}

// RenewProviderCredential refuses results from an obsolete secret or authorization.
func RenewProviderCredential(home string, request ProviderCredentialRenewal) error {
	return withAgentConnectionLock(home, request.ConnectionID, func() error {
		reference, body, err := readProviderReference(home, request.ConnectionID)
		if err != nil {
			return err
		}
		defer clear(body)
		if reference.CredentialRef != request.CredentialRef || reference.AuthorizationID != request.AuthorizationID || reference.CredentialRef == "" {
			return fmt.Errorf("The connection changed during renewal. Reload its credentials.")
		}
		previous, err := normalizeProviderCredential(request.ConnectionID, json.RawMessage(body))
		if err != nil {
			return err
		}
		next, err := normalizeProviderCredential(request.ConnectionID, request.Record)
		if err != nil {
			return err
		}
		if previous.Type != "account" || next.Type != "account" || previous.Channel != next.Channel || previous.AccountID != next.AccountID || previous.TokenURL != next.TokenURL || previous.ClientID != next.ClientID {
			return fmt.Errorf("The credential renewal changes the connection identity.")
		}
		return storeProviderCredential(home, request.ConnectionID, reference.AuthorizationID, next)
	})
}

// MigrateProviderCredentialStore replaces legacy files only after a verified Keychain copy exists.
func MigrateProviderCredentialStore(home string) ([]string, error) {
	root := filepath.Join(home, "refresh-grants")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireOwnerOnlyDirectory(root, "connection credential references"); err != nil {
		return nil, err
	}
	migrated := []string{}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if entry.IsDir() || id+".json" != entry.Name() {
			return nil, fmt.Errorf("The credential store contains an unsupported entry.")
		}
		changed := false
		err := withAgentConnectionLock(home, id, func() error {
			reference, body, err := readProviderReference(home, id)
			if err != nil {
				return err
			}
			defer clear(body)
			if reference.CredentialRef != "" {
				return nil
			}
			record, err := normalizeProviderCredential(id, json.RawMessage(body))
			if err != nil {
				return err
			}
			identity, err := agentConnectionIdentityLocked(home, id)
			if err != nil {
				return err
			}
			if err := storeProviderCredential(home, id, identity, record); err != nil {
				return err
			}
			changed = true
			return nil
		})
		if err != nil {
			return migrated, err
		}
		if changed {
			migrated = append(migrated, id)
		}
	}
	return migrated, nil
}

func protectStagedProviderCredentials(home, stage string, credentials []string) (bool, error) {
	unchanged := true
	for _, id := range credentials {
		body, err := readSecretSource(filepath.Join(stage, id+".json"), "staged credential")
		if err != nil {
			return false, err
		}
		record, err := normalizeProviderCredential(id, json.RawMessage(body))
		clear(body)
		if err != nil {
			return false, err
		}
		previous, previousBody, previousErr := readProviderReference(home, id)
		previousRecord, normalizeErr := normalizeProviderCredential(id, json.RawMessage(previousBody))
		clear(previousBody)
		if previousErr == nil && normalizeErr == nil && previousRecord == record && previous.CredentialRef != "" {
			if err := replaceRuntimeGrant(filepath.Join(stage, id+".json"), previous); err != nil {
				return false, err
			}
			continue
		}
		unchanged = false
		identity, err := newLiveProviderAttemptID()
		if err != nil {
			return false, err
		}
		body, err = json.Marshal(record)
		if err != nil {
			return false, err
		}
		reference, err := WriteHostSecret(home, "provider", body)
		clear(body)
		if err != nil {
			return false, err
		}
		metadata := providerCredentialReference{Version: 1, Credential: id, Channel: record.Channel, Type: record.Type, CredentialRef: reference, AuthorizationID: identity}
		if err := replaceRuntimeGrant(filepath.Join(stage, id+".json"), metadata); err != nil {
			return false, err
		}
	}
	return unchanged, nil
}

// ProviderCredentialIdentity contains no provider credential.
type ProviderCredentialIdentity struct {
	AuthorizationID string `json:"authorization_id"`
	CredentialRef   string `json:"credential_ref"`
	Type            string `json:"type"`
}

// ReadProviderCredentialIdentity reads both revisions without Keychain access.
func ReadProviderCredentialIdentity(home, id string) (ProviderCredentialIdentity, error) {
	var result ProviderCredentialIdentity
	err := withAgentConnectionLock(home, id, func() error {
		body, err := readSecretSource(filepath.Join(home, "refresh-grants", id+".json"), "connection credential reference")
		if err != nil {
			return err
		}
		defer clear(body)
		var reference providerCredentialReference
		if decodeRuntimeAuthJSON(body, &reference) != nil || reference.Version != 1 || reference.Credential != id || !validSHA256(reference.CredentialRef) || !validSHA256(reference.AuthorizationID) || reference.Type != "api_key" && reference.Type != "account" {
			return fmt.Errorf("The connection requires host credential migration.")
		}
		result = ProviderCredentialIdentity{AuthorizationID: reference.AuthorizationID, CredentialRef: reference.CredentialRef, Type: reference.Type}
		return nil
	})
	return result, err
}
