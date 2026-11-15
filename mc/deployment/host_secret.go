package deployment

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

const maxHostSecretBytes = 256 << 10

var ErrHostCredentialUnavailable = errors.New("The host credential store is unavailable. Unlock the login Keychain, then retry.")

type hostSecretStore interface {
	Read(service, account string) ([]byte, error)
	Write(service, account string, value []byte) error
	Delete(service, account string) error
}

var hostSecretBackend hostSecretStore = platformHostSecretStore()

func hostSecretIdentity(home, kind, reference string) (string, string, error) {
	canonical, err := CanonicalHome(home)
	if err != nil {
		return "", "", err
	}
	if err := requireOwnerOnlyDirectory(canonical, "host credential home"); err != nil {
		return "", "", err
	}
	if _, err := agentProfileID(kind, ""); err != nil || !validSHA256(reference) {
		return "", "", fmt.Errorf("The host credential reference is invalid.")
	}
	sum := sha256.Sum256([]byte(canonical))
	return "Culler-" + hex.EncodeToString(sum[:]), kind + ":" + reference, nil
}

// WriteHostSecret creates an immutable Keychain entry and verifies its contents.
func WriteHostSecret(home, kind string, value []byte) (string, error) {
	if len(value) == 0 || len(value) > maxHostSecretBytes {
		return "", fmt.Errorf("The host credential has an invalid size.")
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	reference := hex.EncodeToString(nonce[:])
	service, account, err := hostSecretIdentity(home, kind, reference)
	if err != nil {
		return "", err
	}
	if err := retainHostSecretReference(home, kind, reference, len(value)); err != nil {
		return "", err
	}
	if err := hostSecretBackend.Write(service, account, value); err != nil {
		_ = DeleteHostSecret(home, kind, reference)
		return "", ErrHostCredentialUnavailable
	}
	stored, err := hostSecretBackend.Read(service, account)
	defer clear(stored)
	if err != nil || sha256.Sum256(stored) != sha256.Sum256(value) {
		_ = DeleteHostSecret(home, kind, reference)
		return "", ErrHostCredentialUnavailable
	}
	return reference, nil
}

// ReadHostSecret reads one explicit Keychain entry without a fallback store.
func ReadHostSecret(home, kind, reference string) ([]byte, error) {
	service, account, err := hostSecretIdentity(home, kind, reference)
	if err != nil {
		return nil, err
	}
	body, err := hostSecretBackend.Read(service, account)
	if err != nil || len(body) == 0 || len(body) > maxHostSecretBytes {
		clear(body)
		return nil, ErrHostCredentialUnavailable
	}
	return body, nil
}

// DeleteHostSecret removes one referenced entry from the installation's Keychain service.
func DeleteHostSecret(home, kind, reference string) error {
	service, account, err := hostSecretIdentity(home, kind, reference)
	if err != nil {
		return err
	}
	var deletion error
	if backend, ok := hostSecretBackend.(interface {
		DeleteParts(string, string, int) error
	}); ok {
		var ref hostSecretReference
		body, err := os.ReadFile(hostSecretReferencePath(home, kind, reference))
		if err == nil && json.Unmarshal(body, &ref) == nil && ref.Size > 0 && ref.Size <= maxHostSecretBytes {
			deletion = backend.DeleteParts(service, account, ref.Size)
		} else {
			deletion = hostSecretBackend.Delete(service, account)
		}
	} else {
		deletion = hostSecretBackend.Delete(service, account)
	}
	if deletion != nil && !os.IsNotExist(deletion) {
		return ErrHostCredentialUnavailable
	}
	return deleteHostSecretReference(home, kind, reference)
}
