//go:build test_fake_routing

package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func init() {
	if root := os.Getenv("CULLER_TEST_HOST_SECRET_DIR"); root != "" {
		hostSecretBackend = &fixtureHostSecretStore{root: root}
	}
}

type fixtureHostSecretStore struct{ root string }

func (store *fixtureHostSecretStore) path(service, account string) (string, error) {
	if !filepath.IsAbs(store.root) {
		return "", ErrHostCredentialUnavailable
	}
	if err := ensureOwnerDirectory(store.root, "synthetic credential directory"); err != nil {
		return "", err
	}
	root, err := CanonicalHome(store.root)
	if err != nil {
		return "", err
	}
	if home := os.Getenv("MC_HOME"); home != "" {
		canonical, err := CanonicalHome(home)
		if err != nil {
			return "", err
		}
		relative, err := filepath.Rel(canonical, root)
		if err != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) {
			return "", ErrHostCredentialUnavailable
		}
	}
	sum := sha256.Sum256([]byte(service + "/" + account))
	return filepath.Join(root, hex.EncodeToString(sum[:])+".json"), nil
}

func syntheticHostSecret(account string, body []byte) bool {
	switch {
	case strings.HasPrefix(account, "provider:"):
		var record ProviderCredentialRecord
		if decodeRuntimeAuthJSON(body, &record) != nil {
			return false
		}
		normalized, err := normalizeProviderCredential(record.Credential, json.RawMessage(body))
		if err != nil {
			return false
		}
		for _, secret := range []string{normalized.APIKey, normalized.AccessToken, normalized.IDToken, normalized.RefreshToken} {
			if secret != "" && !strings.HasPrefix(secret, "synthetic-") {
				return false
			}
		}
		return true
	case strings.HasPrefix(account, "proxy-tls:"):
		var bundle providerTLSBundle
		return decodeRuntimeAuthJSON(body, &bundle) == nil && validateProviderTLS(bundle, time.Now()) == nil
	default:
		return false
	}
}

func (store *fixtureHostSecretStore) Read(service, account string) ([]byte, error) {
	path, err := store.path(service, account)
	if err != nil {
		return nil, err
	}
	body, err := readSecretSource(path, "synthetic credential")
	if err != nil || !syntheticHostSecret(account, body) {
		clear(body)
		return nil, ErrHostCredentialUnavailable
	}
	return body, nil
}
func (store *fixtureHostSecretStore) Write(service, account string, body []byte) error {
	if len(body) > maxHostSecretBytes || !syntheticHostSecret(account, body) {
		return fmt.Errorf("The test credential store accepts synthetic credentials only.")
	}
	path, err := store.path(service, account)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
func (store *fixtureHostSecretStore) Delete(service, account string) error {
	path, err := store.path(service, account)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func HostCredentialCommandsAvailable() bool {
	return runtime.GOOS == "darwin" || os.Getenv("CULLER_TEST_HOST_SECRET_DIR") != ""
}
