//go:build linux

package deployment

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	linuxTestAccount  = "provider:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	linuxTestFullHash = "Culler-00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
)

func newLinuxTestStore(t *testing.T) (*hostKeySecretStore, string) {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	machine := filepath.Join(root, "machine-id")
	if err := os.WriteFile(machine, []byte("1234567890abcdef1234567890abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &hostKeySecretStore{machineIDPath: machine, stateRoot: state}, root
}

func TestLinuxHostSecretRoundTripAndCiphertextBoundary(t *testing.T) {
	store, _ := newLinuxTestStore(t)
	value := []byte("synthetic-provider-token-0123456789")
	if err := store.Write(linuxTestFullHash, linuxTestAccount, value); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(linuxTestFullHash, linuxTestAccount, append(value, '!', '!')); err != nil {
		t.Fatal("a replacement write was refused")
	}
	stored, err := store.Read(linuxTestFullHash, linuxTestAccount)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(value)+"!!" {
		t.Fatalf("round trip changed the value: %q", stored)
	}
	records, err := filepath.Glob(filepath.Join(store.stateRoot, "blobs", "*", "*.enc"))
	if err != nil || len(records) != 1 {
		t.Fatalf("record files %v err %v", records, err)
	}
	body, err := os.ReadFile(records[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, value) {
		t.Fatal("the stored record carries the plaintext credential")
	}
	for _, size := range []int{0, maxHostSecretBytes + 1} {
		if err := store.Write(linuxTestFullHash, "provider:ff"+strings.Repeat("0", 62), make([]byte, size)); err == nil {
			t.Fatalf("write accepted a size %d credential", size)
		}
	}
}

func TestLinuxHostSecretRefusesMissingSaltAndNeverRegenerates(t *testing.T) {
	store, _ := newLinuxTestStore(t)
	value := []byte("salted-secret")
	if err := store.Write(linuxTestFullHash, linuxTestAccount, value); err != nil {
		t.Fatal(err)
	}
	salt := filepath.Join(store.stateRoot, "credential-salt")
	if err := os.Remove(salt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(linuxTestFullHash, linuxTestAccount); err == nil {
		t.Fatal("a lost salt still read a credential")
	}
	// A fresh salt must not resurrect the old record: writes may continue under
	// the new salt, reads of the old ciphertext stay refused.
	fake, err := os.OpenFile(salt, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fake.WriteString("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); err != nil {
		fake.Close()
		t.Fatal(err)
	}
	fake.Close()
	if _, err := store.Read(linuxTestFullHash, linuxTestAccount); err == nil {
		t.Fatal("a regenerated salt decrypted the previous credential")
	}
}

func TestLinuxHostSecretMachineIdentityChangeStrandsRecords(t *testing.T) {
	store, root := newLinuxTestStore(t)
	value := []byte("machine-bound-secret")
	if err := store.Write(linuxTestFullHash, linuxTestAccount, value); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.machineIDPath, []byte("ffffffffffffffffffffffffffffffff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(linuxTestFullHash, linuxTestAccount); err == nil {
		t.Fatal("a cloned machine identity still opened the credential")
	}
	_ = root
}

func TestLinuxHostSecretRecordTamperingIsRefused(t *testing.T) {
	store, _ := newLinuxTestStore(t)
	if err := store.Write(linuxTestFullHash, linuxTestAccount, []byte("authenticated-secret")); err != nil {
		t.Fatal(err)
	}
	records, err := filepath.Glob(filepath.Join(store.stateRoot, "blobs", "*", "*.enc"))
	if err != nil || len(records) != 1 {
		t.Fatalf("records %v err %v", records, err)
	}
	body, err := os.ReadFile(records[0])
	if err != nil {
		t.Fatal(err)
	}
	body[len(body)-1] ^= 0x01
	if err := os.WriteFile(records[0], body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(linuxTestFullHash, linuxTestAccount); err == nil {
		t.Fatal("a flipped ciphertext byte passed authentication")
	}
}

func TestLinuxHostSecretRejectsUnsafeNames(t *testing.T) {
	store, _ := newLinuxTestStore(t)
	accounts := []string{"has/slash", "..", "../escape", "provider:" + strings.Repeat("0", 64) + "/x", ""}
	for _, account := range accounts {
		if err := store.Write(linuxTestFullHash, account, []byte("x")); err == nil {
			t.Fatalf("account %q was accepted", account)
		}
		if _, err := store.Read(linuxTestFullHash, account); err == nil {
			t.Fatalf("account %q was read", account)
		}
	}
	for _, service := range []string{"other-prefix", "Culler-ZZZ", ""} {
		if err := store.Write(service, linuxTestAccount, []byte("x")); err == nil {
			t.Fatalf("service %q was accepted", service)
		}
	}
}

func TestLinuxHostSecretThroughTheSharedReferencePath(t *testing.T) {
	store, _ := newLinuxTestStore(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	previous := hostSecretBackend
	hostSecretBackend = store
	t.Cleanup(func() { hostSecretBackend = previous })
	value := []byte("through-the-wrapper-secret")
	reference, err := WriteHostSecret(home, "provider", value)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := ReadHostSecret(home, "provider", reference)
	if err != nil || !bytes.Equal(stored, value) {
		t.Fatalf("shared path read %q err %v", stored, err)
	}
	if err := DeleteHostSecret(home, "provider", reference); err != nil {
		t.Fatal(err)
	}
	if err := DeleteHostSecret(home, "provider", reference); err != nil {
		t.Fatalf("a second delete reported %v", err)
	}
	if _, err := ReadHostSecret(home, "provider", reference); err == nil {
		t.Fatal("the deleted credential still reads")
	}
}
