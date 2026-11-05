package deployment

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const providerProxyHostname = "host.docker.internal"

// ProviderProxyTLS contains private material for the host process only.
type ProviderProxyTLS struct {
	CA   string `json:"ca"`
	Cert string `json:"cert"`
	Key  string `json:"key"`
}

type providerTLSBundle struct {
	ProviderProxyTLS
	CAKey string `json:"ca_key"`
}

type providerTLSReference struct {
	Version   int    `json:"version"`
	Reference string `json:"reference"`
	CADigest  string `json:"ca_digest"`
}

type providerTLSStore struct {
	read   func(string) ([]byte, error)
	write  func([]byte) (string, error)
	delete func(string) error
}

// EnsureProviderProxyTLS preserves the installation authority across server certificate renewals.
func EnsureProviderProxyTLS(home string, now time.Time) (ProviderProxyTLS, error) {
	var result ProviderProxyTLS
	err := withAgentConnectionLock(home, "provider-proxy-tls", func() error {
		var err error
		result, err = ensureProviderProxyTLS(home, now, providerTLSStore{
			read:   func(ref string) ([]byte, error) { return ReadHostSecret(home, "proxy-tls", ref) },
			write:  func(value []byte) (string, error) { return WriteHostSecret(home, "proxy-tls", value) },
			delete: func(ref string) error { return DeleteHostSecret(home, "proxy-tls", ref) },
		})
		return err
	})
	return result, err
}

func ensureProviderProxyTLS(home string, now time.Time, store providerTLSStore) (ProviderProxyTLS, error) {
	root := filepath.Join(home, "provider-proxy")
	if err := ensureOwnerDirectory(root, "proxy certificate directory"); err != nil {
		return ProviderProxyTLS{}, fmt.Errorf("The proxy certificate directory is unavailable.")
	}
	path := filepath.Join(root, "tls.json")
	var bundle providerTLSBundle
	changed := false
	previousReference := ""
	data, err := readSecretSource(path, "proxy certificate reference")
	if err == nil {
		var ref providerTLSReference
		if json.Unmarshal(data, &ref) != nil || ref.Version != 1 || ref.Reference == "" {
			return ProviderProxyTLS{}, fmt.Errorf("The proxy certificate reference is invalid.")
		}
		previousReference = ref.Reference
		secret, err := store.read(ref.Reference)
		if err != nil || json.Unmarshal(secret, &bundle) != nil || certificateDigest(bundle.CA) != ref.CADigest {
			return ProviderProxyTLS{}, fmt.Errorf("The host credential store cannot supply the proxy certificate.")
		}
		if err := validateProviderTLS(bundle, now); err != nil {
			return ProviderProxyTLS{}, err
		}
		cert, _ := parseProxyCertificate(bundle.Cert)
		ca, _ := parseProxyCertificate(bundle.CA)
		if !now.Add(90 * 24 * time.Hour).Before(ca.NotAfter) {
			bundle, err = newProviderTLS(now)
			if err != nil {
				return ProviderProxyTLS{}, err
			}
			changed = true
		} else if !now.Add(30 * 24 * time.Hour).Before(cert.NotAfter) {
			bundle, err = renewProviderTLS(bundle, now)
			if err != nil {
				return ProviderProxyTLS{}, err
			}
			changed = true
		}
	} else if errors.Is(err, os.ErrNotExist) {
		bundle, err = newProviderTLS(now)
		if err != nil {
			return ProviderProxyTLS{}, err
		}
		changed = true
	} else {
		return ProviderProxyTLS{}, fmt.Errorf("The proxy certificate reference is unavailable.")
	}
	if changed {
		secret, _ := json.Marshal(bundle)
		ref, err := store.write(secret)
		if err != nil {
			return ProviderProxyTLS{}, fmt.Errorf("The host credential store cannot save the proxy certificate.")
		}
		confirmed, err := store.read(ref)
		if err != nil || string(confirmed) != string(secret) {
			return ProviderProxyTLS{}, fmt.Errorf("The host credential store did not retain the proxy certificate.")
		}
		metadata, _ := json.Marshal(providerTLSReference{Version: 1, Reference: ref, CADigest: certificateDigest(bundle.CA)})
		stage, err := os.CreateTemp(root, ".tls-*")
		if err != nil {
			return ProviderProxyTLS{}, err
		}
		defer os.Remove(stage.Name())
		_, err = stage.Write(metadata)
		if err == nil {
			err = stage.Sync()
		}
		closeErr := stage.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(stage.Name(), path)
		}
		if err == nil {
			err = syncDirectory(root)
		}
		if err != nil {
			return ProviderProxyTLS{}, fmt.Errorf("The proxy certificate reference could not become durable.")
		}
		if previousReference != "" && store.delete != nil {
			_ = store.delete(previousReference)
		}
	}
	return bundle.ProviderProxyTLS, nil
}

func certificateDigest(cert string) string {
	sum := sha256.Sum256([]byte(cert))
	return hex.EncodeToString(sum[:])
}

func proxySerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func encodeProxyKey(key *ecdsa.PrivateKey) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

func parseProxyCertificate(value string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("The proxy certificate is invalid.")
	}
	return x509.ParseCertificate(block.Bytes)
}

func newProviderTLS(now time.Time) (providerTLSBundle, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return providerTLSBundle{}, err
	}
	serial, err := proxySerial()
	if err != nil {
		return providerTLSBundle{}, err
	}
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Culler provider access"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		return providerTLSBundle{}, err
	}
	keyPEM, err := encodeProxyKey(key)
	if err != nil {
		return providerTLSBundle{}, err
	}
	return renewProviderTLS(providerTLSBundle{ProviderProxyTLS: ProviderProxyTLS{
		CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
	}, CAKey: keyPEM}, now)
}

func renewProviderTLS(bundle providerTLSBundle, now time.Time) (providerTLSBundle, error) {
	ca, err := parseProxyCertificate(bundle.CA)
	if err != nil || !now.Add(90*24*time.Hour).Before(ca.NotAfter) {
		return bundle, fmt.Errorf("The proxy certificate authority requires replacement.")
	}
	block, _ := pem.Decode([]byte(bundle.CAKey))
	if block == nil {
		return bundle, fmt.Errorf("The proxy certificate authority key is invalid.")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return bundle, fmt.Errorf("The proxy certificate authority key is invalid.")
	}
	caKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return bundle, fmt.Errorf("The proxy certificate authority key is invalid.")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return bundle, err
	}
	serial, err := proxySerial()
	if err != nil {
		return bundle, err
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: providerProxyHostname},
		DNSNames: []string{providerProxyHostname, "localhost"}, NotBefore: now.Add(-5 * time.Minute),
		NotAfter: now.Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
	if err != nil {
		return bundle, err
	}
	bundle.Cert = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	bundle.Key, err = encodeProxyKey(key)
	return bundle, err
}

func validateProviderTLS(bundle providerTLSBundle, now time.Time) error {
	ca, err := parseProxyCertificate(bundle.CA)
	if err != nil || !ca.IsCA || now.Before(ca.NotBefore) {
		return fmt.Errorf("The proxy certificate authority is unavailable.")
	}
	cert, err := parseProxyCertificate(bundle.Cert)
	if err != nil || cert.CheckSignatureFrom(ca) != nil || cert.VerifyHostname(providerProxyHostname) != nil {
		return fmt.Errorf("The proxy certificate does not match its authority or host name.")
	}
	if _, err := tls.X509KeyPair([]byte(bundle.Cert), []byte(bundle.Key)); err != nil {
		return fmt.Errorf("The proxy certificate key is invalid.")
	}
	if _, err := tls.X509KeyPair([]byte(bundle.CA), []byte(bundle.CAKey)); err != nil {
		return fmt.Errorf("The proxy certificate authority key is invalid.")
	}
	return nil
}
