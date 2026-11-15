package deployment

import (
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func memoryProviderTLSStore() providerTLSStore {
	values := map[string][]byte{}
	return providerTLSStore{
		read: func(ref string) ([]byte, error) {
			value, ok := values[ref]
			if !ok {
				return nil, fmt.Errorf("unavailable")
			}
			return append([]byte(nil), value...), nil
		},
		write: func(value []byte) (string, error) {
			ref := fmt.Sprintf("test-%d", len(values))
			values[ref] = append([]byte(nil), value...)
			return ref, nil
		},
	}
}

func TestProviderTLSKeepsPrivateKeysInTheProtectedStore(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	store := memoryProviderTLSStore()
	first, err := ensureProviderProxyTLS(home, now, store)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(first.CA)) {
		t.Fatal("The public authority is invalid.")
	}
	cert, err := parseProxyCertificate(first.Cert)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: providerProxyHostname, CurrentTime: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: "untrusted.example", CurrentTime: now}); err == nil {
		t.Fatal("The certificate accepted another host name.")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: x509.NewCertPool(), DNSName: providerProxyHostname, CurrentTime: now}); err == nil {
		t.Fatal("The certificate accepted an unknown authority.")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: providerProxyHostname, CurrentTime: now.Add(91 * 24 * time.Hour)}); err == nil {
		t.Fatal("The expired certificate passed verification.")
	}
	err = filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err == nil && (strings.Contains(string(body), "PRIVATE KEY") || strings.Contains(string(body), first.Key)) {
			t.Fatal("A private key escaped the protected store.")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ensureProviderProxyTLS(home, now.Add(time.Hour), store)
	if err != nil || second != first {
		t.Fatalf("A restart replaced the certificate: %v", err)
	}
	renewed, err := ensureProviderProxyTLS(home, now.Add(61*24*time.Hour), store)
	if err != nil || renewed.CA != first.CA || renewed.Cert == first.Cert || renewed.Key == first.Key {
		t.Fatalf("Certificate renewal changed trust or retained the server key: %v", err)
	}
}

func TestProviderTLSStoreFailurePreservesTheReference(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	store := memoryProviderTLSStore()
	_, err := ensureProviderProxyTLS(home, now, store)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "provider-proxy", "tls.json")
	before, _ := os.ReadFile(path)
	broken := store
	broken.write = func([]byte) (string, error) { return "", fmt.Errorf("locked") }
	if _, err := ensureProviderProxyTLS(home, now.Add(61*24*time.Hour), broken); err == nil {
		t.Fatal("Certificate renewal ignored the store failure.")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("The failed save replaced the usable reference.")
	}
	broken = store
	broken.read = func(string) ([]byte, error) { return nil, fmt.Errorf("locked") }
	if _, err := ensureProviderProxyTLS(home, now, broken); err == nil {
		t.Fatal("The certificate check ignored a locked store.")
	}
	var ref providerTLSReference
	if json.Unmarshal(before, &ref) != nil || ref.Reference == "" {
		t.Fatal("The certificate reference is invalid.")
	}
}

func TestProviderTLSReplacesAnExpiringAuthorityAndRejectsUnsafeReferences(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	store := memoryProviderTLSStore()
	first, err := ensureProviderProxyTLS(home, now, store)
	if err != nil {
		t.Fatal(err)
	}
	next, err := ensureProviderProxyTLS(home, now.AddDate(10, 0, 1), store)
	if err != nil || next.CA == first.CA {
		t.Fatalf("The expired authority did not receive a replacement: %v", err)
	}
	cert, err := parseProxyCertificate(next.Cert)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(next.CA))
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: providerProxyHostname, CurrentTime: now.AddDate(10, 0, 1)}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "provider-proxy", "tls.json")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureProviderProxyTLS(home, now, store); err == nil {
		t.Fatal("A public certificate reference passed validation.")
	}
}
