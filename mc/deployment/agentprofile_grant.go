package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

func withAgentConnectionLock(home, id string, action func() error) error {
	return withAgentConnectionLockContext(nil, home, id, action)
}

func withAgentConnectionLockContext(ctx context.Context, home, id string, action func() error) error {
	if _, err := agentProfileID(id, ""); err != nil {
		return err
	}
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return err
	}
	lock, err := agentProfileOperationLockContext(ctx, root, "connection-"+id)
	if err != nil {
		return err
	}
	defer agentProfileUnlock(lock)
	return action()
}

// CompareAndPublishAgentConnectionGrant preserves a credential from a newer authorization.
func CompareAndPublishAgentConnectionGrant(home, id, expectedDigest string, grant any) error {
	return withAgentConnectionLock(home, id, func() error {
		body, err := readConnectionCredentialBody(home, id)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("The connection credential is unavailable.")
		}
		digest := ""
		if err == nil {
			sum := sha256.Sum256(body)
			digest = hex.EncodeToString(sum[:])
		}
		if digest != expectedDigest {
			return fmt.Errorf("The connection changed. Retry with its current credentials.")
		}
		return publishAgentConnectionAuthorization(home, id, grant)
	})
}

type AgentConnectionRotation struct {
	Expected json.RawMessage `json:"expected"`
	Grant    json.RawMessage `json:"grant"`
}

// RotateAgentConnectionGrant accepts only a renewal of the supplied current grant.
func RotateAgentConnectionGrant(home string, req AgentConnectionRotation) error {
	var previous, next oauthRuntimeGrant
	if decodeRuntimeAuthJSON(req.Expected, &previous) != nil || decodeRuntimeAuthJSON(req.Grant, &next) != nil {
		return fmt.Errorf("The credential renewal request is invalid.")
	}
	var err error
	previous, err = normalizeProviderCredential(previous.Credential, previous)
	if err != nil {
		return err
	}
	next, err = normalizeProviderCredential(next.Credential, next)
	if err != nil {
		return err
	}
	before, after := previous, next
	before.RefreshToken, after.RefreshToken = "", ""
	if before != after || previous.RefreshToken == "" || next.RefreshToken == "" {
		return fmt.Errorf("The credential renewal changes the connection identity.")
	}
	return withAgentConnectionLock(home, next.Credential, func() error {
		body, err := readConnectionCredentialBody(home, next.Credential)
		if err != nil {
			return fmt.Errorf("The connection credential is unavailable.")
		}
		var current oauthRuntimeGrant
		if decodeRuntimeAuthJSON(body, &current) != nil || !reflect.DeepEqual(current, previous) {
			return fmt.Errorf("The connection changed during renewal. Reload its credentials.")
		}
		return publishAgentConnectionRenewal(home, next.Credential, next)
	})
}

type agentConnectionIdentity struct {
	ID          string `json:"id"`
	GrantDigest string `json:"grant_digest"`
}

func grantBodyDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func writeAgentConnectionIdentity(home, id, identity, digest string) error {
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return err
	}
	body, err := json.Marshal(agentConnectionIdentity{ID: identity, GrantDigest: digest})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(root, ".connection-identity-")
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
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(root, "connection-"+id+".identity")); err != nil {
		return err
	}
	return syncDirectory(root)
}

func agentConnectionIdentityLocked(home, id string) (string, error) {
	grantPath := filepath.Join(home, "refresh-grants", id+".json")
	if _, err := os.Lstat(grantPath); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	body, err := readSecretSource(grantPath, "connection credential reference")
	if err != nil {
		return "", err
	}
	defer clear(body)
	var reference providerCredentialReference
	if json.Unmarshal(body, &reference) == nil && reference.Credential == id && validSHA256(reference.CredentialRef) && validSHA256(reference.AuthorizationID) {
		return reference.AuthorizationID, nil
	}
	digest := grantBodyDigest(body)
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "connection-"+id+".identity")
	if _, err := os.Lstat(path); err == nil {
		body, err := readSecretSource(path, "connection authorization identity")
		if err != nil {
			return "", err
		}
		var identity agentConnectionIdentity
		if json.Unmarshal(body, &identity) != nil || !validSHA256(identity.ID) || !validSHA256(identity.GrantDigest) {
			return "", fmt.Errorf("The connection authorization record is invalid. Reconnect the connection.")
		}
		if identity.GrantDigest == digest {
			return identity.ID, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	identity, err := newLiveProviderAttemptID()
	if err != nil {
		return "", err
	}
	if err = writeAgentConnectionIdentity(home, id, identity, digest); err != nil {
		return "", err
	}
	return identity, nil
}

// AgentConnectionAuthorizationIdentity remains stable across normal token renewal.
func AgentConnectionAuthorizationIdentity(home, id string) (string, error) {
	var identity string
	err := withAgentConnectionLock(home, id, func() error {
		var err error
		identity, err = agentConnectionIdentityLocked(home, id)
		return err
	})
	return identity, err
}

func publishAgentConnectionAuthorization(home, id string, grant any) error {
	identity, err := newLiveProviderAttemptID()
	if err != nil {
		return err
	}
	return storeProviderCredential(home, id, identity, grant)
}

func publishAgentConnectionRenewal(home, id string, grant any) error {
	identity, err := agentConnectionIdentityLocked(home, id)
	if err != nil {
		return err
	}
	if identity == "" {
		return fmt.Errorf("The connection has no credential to renew.")
	}
	return storeProviderCredential(home, id, identity, grant)
}

func compareAndPublishAgentConnectionAuthorization(home, id, expectedIdentity string, grant any) error {
	return withAgentConnectionLock(home, id, func() error {
		identity, err := agentConnectionIdentityLocked(home, id)
		if err != nil {
			return err
		}
		if identity != expectedIdentity {
			return fmt.Errorf("The connection authorization changed. Reconnect and retry.")
		}
		return publishAgentConnectionAuthorization(home, id, grant)
	})
}

// ReadConnectionAuthorizationIdentity reads the current identity without changing the credential store.
func ReadConnectionAuthorizationIdentity(home, id string) (string, error) {
	if _, err := agentProfileID(id, ""); err != nil {
		return "", err
	}
	metadata, err := readSecretSource(filepath.Join(home, "refresh-grants", id+".json"), "connection credential reference")
	if err == nil {
		defer clear(metadata)
		var reference providerCredentialReference
		if json.Unmarshal(metadata, &reference) == nil && reference.Credential == id && validSHA256(reference.AuthorizationID) && validSHA256(reference.CredentialRef) {
			return reference.AuthorizationID, nil
		}
	}
	body, err := os.ReadFile(filepath.Join(home, "agent-profile-operations", "connection-"+id+".identity"))
	if err != nil {
		return "", err
	}
	var identity agentConnectionIdentity
	if json.Unmarshal(body, &identity) != nil || !validSHA256(identity.ID) {
		return "", fmt.Errorf("The connection authorization identity is unavailable.")
	}
	return identity.ID, nil
}
