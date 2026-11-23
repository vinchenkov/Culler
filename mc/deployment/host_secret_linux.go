//go:build linux

package deployment

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The Linux host credential store keeps ciphertext in the operator's state
// directory, outside the deployment home. The encryption key derives from the
// machine identity plus a per-user salt that is created once and never
// regenerated. A restore that brings back the home without the state directory
// therefore yields unreadable entries: the operator re-authorizes. This mirrors
// the darwin backend, whose entries live outside the home in the Keychain.
type hostKeySecretStore struct {
	machineIDPath  string // empty selects /etc/machine-id
	fallbackIDPath string // empty selects /var/lib/dbus/machine-id
	stateRoot      string // empty selects the XDG state directory
}

const (
	hostKeySaltBytes    = 32
	hostKeyNonceBytes   = 12
	hostKeyRecordHeader = "HS1"
)

var (
	hostKeyServicePattern = regexp.MustCompile(`^Culler-[0-9a-f]{64}$`)
	hostKeyAccountPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

func platformHostSecretStore() hostSecretStore { return &hostKeySecretStore{} }

func (s *hostKeySecretStore) credentialRoot() (string, error) {
	root := s.stateRoot
	if root == "" {
		state := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
		if state == "" || !filepath.IsAbs(state) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			state = filepath.Join(home, ".local", "state")
		}
		root = filepath.Join(state, "culler-host-credentials")
	}
	if err := ensureOwnerTree(root, "host credential state"); err != nil {
		return "", err
	}
	return root, nil
}

// ensureOwnerTree creates missing parents (a fresh user state directory has
// none) and then applies the same owner-only 0700 inspection as the shared
// helpers. A pre-existing group-writable directory still fails closed.
func ensureOwnerTree(path, label string) error {
	if err := os.MkdirAll(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create %s: %w", label, err)
	}
	return requireOwnerOnlyDirectory(path, label)
}

func (s *hostKeySecretStore) saltPath(root string) string {
	return filepath.Join(root, "credential-salt")
}

func (s *hostKeySecretStore) blobPath(root, service, account string) string {
	return filepath.Join(root, "blobs", service, account+".enc")
}

func (s *hostKeySecretStore) names(service, account string) error {
	if !hostKeyServicePattern.MatchString(service) || !hostKeyAccountPattern.MatchString(account) || strings.Contains(account, "..") {
		return ErrHostCredentialUnavailable
	}
	return nil
}

func (s *hostKeySecretStore) machineID() (string, error) {
	candidates := []string{s.machineIDPath, s.fallbackIDPath}
	if candidates[0] == "" {
		candidates[0] = "/etc/machine-id"
	}
	if candidates[1] == "" {
		candidates[1] = "/var/lib/dbus/machine-id"
	}
	for _, path := range candidates {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		id := strings.TrimSpace(string(body))
		clear(body)
		if len(id) >= 16 && len(id) <= 128 {
			return id, nil
		}
	}
	return "", ErrHostCredentialUnavailable
}

// readSalt requires an existing salt. It never creates one: regenerating a lost
// salt would leave every stored entry permanently undecryptable while writes
// kept reporting success, so a missing salt must surface as a refusal until the
// operator re-authorizes through the wizard.
func (s *hostKeySecretStore) readSalt(root string) ([]byte, error) {
	return readSecretSource(s.saltPath(root), "host credential salt")
}

// createSaltOnce creates the salt on the first stored credential and leaves an
// existing one untouched. A racing creator wins; the loser reads the same salt.
func (s *hostKeySecretStore) createSaltOnce(root string) error {
	path := s.saltPath(root)
	value := make([]byte, hostKeySaltBytes)
	if _, err := rand.Read(value); err != nil {
		return err
	}
	encoded := []byte(base64.RawStdEncoding.EncodeToString(value))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	return file.Close()
}

func (s *hostKeySecretStore) deriveKey(machineID string, salt []byte, nonce []byte, service, account string) ([]byte, error) {
	return hkdf.Key(sha256.New, []byte(machineID+string(salt)), nonce, "s-horizon-host-secret-v1\x00"+service+"\x00"+account, 32)
}

func (s *hostKeySecretStore) Read(service, account string) ([]byte, error) {
	if err := s.names(service, account); err != nil {
		return nil, err
	}
	root, err := s.credentialRoot()
	if err != nil {
		return nil, err
	}
	salt, err := s.readSalt(root)
	if err != nil {
		return nil, err
	}
	machineID, err := s.machineID()
	if err != nil {
		return nil, err
	}
	record, err := readSecretSource(s.blobPath(root, service, account), "host credential record")
	if err != nil {
		return nil, err
	}
	defer clear(record)
	headerSize := len(hostKeyRecordHeader) + hostKeyNonceBytes
	if len(record) < headerSize+1 || !strings.HasPrefix(string(record), hostKeyRecordHeader) {
		return nil, ErrHostCredentialUnavailable
	}
	nonce := record[len(hostKeyRecordHeader):headerSize]
	key, err := s.deriveKey(machineID, salt, nonce, service, account)
	if err != nil {
		return nil, ErrHostCredentialUnavailable
	}
	value, err := openHostKeyRecord(key, nonce, record[headerSize:], service, account)
	clear(key)
	if err != nil {
		return nil, ErrHostCredentialUnavailable
	}
	return value, nil
}

func (s *hostKeySecretStore) Write(service, account string, value []byte) error {
	if err := s.names(service, account); err != nil {
		return err
	}
	if len(value) == 0 || len(value) > maxHostSecretBytes {
		return ErrHostCredentialUnavailable
	}
	root, err := s.credentialRoot()
	if err != nil {
		return err
	}
	machineID, err := s.machineID()
	if err != nil {
		return err
	}
	if err := s.createSaltOnce(root); err != nil {
		return err
	}
	salt, err := s.readSalt(root)
	if err != nil {
		return err
	}
	defer clear(salt)
	nonce := make([]byte, hostKeyNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	key, err := s.deriveKey(machineID, salt, nonce, service, account)
	if err != nil {
		return err
	}
	defer clear(key)
	sealed, err := sealHostKeyRecord(key, nonce, value, service, account)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.blobPath(root, service, account))
	if err := ensureOwnerTree(dir, "host credential records"); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".record-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(append(append([]byte(hostKeyRecordHeader), nonce...), sealed...)); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.blobPath(root, service, account)); err != nil {
		return err
	}
	return syncDirectory(dir)
}

// Delete removes the record and reports a missing record as os.ErrNotExist so
// the shared cleanup path can accept it. The salt outlives every record: it is
// shared, and removing it would strand credentials another verb still owns.
func (s *hostKeySecretStore) Delete(service, account string) error {
	if err := s.names(service, account); err != nil {
		return err
	}
	root, err := s.credentialRoot()
	if err != nil {
		return err
	}
	if err := os.Remove(s.blobPath(root, service, account)); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(s.blobPath(root, service, account)))
}

func sealHostKeyRecord(key, nonce, value []byte, service, account string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, nonce, value, hostKeyRecordAad(service, account)), nil
}

func openHostKeyRecord(key, nonce, sealed []byte, service, account string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, sealed, hostKeyRecordAad(service, account))
}

func hostKeyRecordAad(service, account string) []byte {
	return []byte(service + "\x00" + account)
}
