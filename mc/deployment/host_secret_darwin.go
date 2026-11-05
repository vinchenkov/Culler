//go:build darwin

package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// The explicit path serves isolated host tests. Production uses the login Keychain.
type keychainHostSecretStore struct {
	keychain    string
	environment []string
}

const keychainPartBytes = 1536

type keychainSecretManifest struct {
	Version int    `json:"version"`
	Parts   int    `json:"parts"`
	Size    int    `json:"size"`
	Digest  string `json:"digest"`
}

func platformHostSecretStore() hostSecretStore { return &keychainHostSecretStore{} }

func (s *keychainHostSecretStore) run(input string, args ...string) ([]byte, error) {
	return s.runSecurity(input, false, args...)
}
func (s *keychainHostSecretStore) runSecurity(input string, allowMissing bool, args ...string) ([]byte, error) {
	if err := requireSecurityTool(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, securityTool, args...)
	if s.environment != nil {
		cmd.Env = s.environment
	}
	cmd.Stdin = strings.NewReader(input)
	out := &boundedRuntimeAuthBuffer{limit: 2 * maxHostSecretBytes}
	errout := &boundedRuntimeAuthBuffer{limit: 64 * 1024}
	cmd.Stdout, cmd.Stderr = out, errout
	err := cmd.Run()
	defer clear(errout.body)
	diagnostics := strings.ReplaceAll(string(errout.body), "password has been deleted.", "")
	if allowMissing {
		diagnostics = strings.ReplaceAll(diagnostics, "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.", "")
		diagnostics = strings.ReplaceAll(diagnostics, "delete-generic-password: returned -25300", "")
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			err = nil
		}
	}
	if err != nil || out.overflow || errout.overflow || strings.TrimSpace(diagnostics) != "" {
		clear(out.body)
		return nil, ErrHostCredentialUnavailable
	}
	return out.body, nil
}

func (s *keychainHostSecretStore) args(args ...string) []string {
	if s.keychain != "" {
		args = append(args, s.keychain)
	}
	return args
}

func securityWord(value string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(value) + "\""
}

func securityLine(words []string) (string, error) {
	for i := range words {
		words[i] = securityWord(words[i])
	}
	line := strings.Join(words, " ") + "\n"
	// The security program reads interactive commands into a fixed-size line buffer.
	if len(line) >= 4096 {
		return "", ErrHostCredentialUnavailable
	}
	return line, nil
}

func (s *keychainHostSecretStore) manifest(service, account string) (keychainSecretManifest, error) {
	var manifest keychainSecretManifest
	out, err := s.run("", s.args("find-generic-password", "-s", service, "-a", account, "-w")...)
	defer clear(out)
	if err != nil {
		return manifest, err
	}
	body, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	defer clear(body)
	if err != nil || json.Unmarshal(body, &manifest) != nil || manifest.Version != 1 || manifest.Parts < 1 || manifest.Size < 1 || manifest.Size > maxHostSecretBytes || manifest.Parts != (manifest.Size+keychainPartBytes-1)/keychainPartBytes || !validSHA256(manifest.Digest) {
		return manifest, ErrHostCredentialUnavailable
	}
	return manifest, nil
}

func (s *keychainHostSecretStore) Read(service, account string) ([]byte, error) {
	manifest, err := s.manifest(service, account)
	if err != nil {
		return nil, err
	}
	var commands strings.Builder
	for part := 0; part < manifest.Parts; part++ {
		line, err := securityLine(s.args("find-generic-password", "-s", service, "-a", fmt.Sprintf("%s.part-%d", account, part), "-w"))
		if err != nil {
			return nil, err
		}
		commands.WriteString(line)
	}
	out, err := s.run(commands.String(), "-i")
	defer clear(out)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != manifest.Parts {
		return nil, ErrHostCredentialUnavailable
	}
	value := make([]byte, 0, manifest.Size)
	for _, line := range lines {
		part, err := base64.StdEncoding.DecodeString(strings.TrimSpace(line))
		if err != nil {
			clear(value)
			return nil, ErrHostCredentialUnavailable
		}
		value = append(value, part...)
		clear(part)
	}
	digest := sha256.Sum256(value)
	if len(value) != manifest.Size || hex.EncodeToString(digest[:]) != manifest.Digest {
		clear(value)
		return nil, ErrHostCredentialUnavailable
	}
	return value, nil
}

func (s *keychainHostSecretStore) Write(service, account string, value []byte) error {
	if len(value) == 0 || len(value) > maxHostSecretBytes {
		return ErrHostCredentialUnavailable
	}
	digest := sha256.Sum256(value)
	manifest := keychainSecretManifest{Version: 1, Parts: (len(value) + keychainPartBytes - 1) / keychainPartBytes, Size: len(value), Digest: hex.EncodeToString(digest[:])}
	var commands strings.Builder
	appendValue := func(name string, body []byte) error {
		line, err := securityLine(s.args("add-generic-password", "-s", service, "-a", name, "-T", securityTool, "-w", base64.StdEncoding.EncodeToString(body)))
		if err == nil {
			commands.WriteString(line)
		}
		return err
	}
	for part := 0; part < manifest.Parts; part++ {
		start, end := part*keychainPartBytes, (part+1)*keychainPartBytes
		if end > len(value) {
			end = len(value)
		}
		if err := appendValue(fmt.Sprintf("%s.part-%d", account, part), value[start:end]); err != nil {
			return err
		}
	}
	body, _ := json.Marshal(manifest)
	if err := appendValue(account, body); err != nil {
		return err
	}
	// The secret stays on stdin. The manifest becomes visible after every part exists.
	out, err := s.run(commands.String(), "-i")
	clear(out)
	return err
}

func (s *keychainHostSecretStore) Delete(service, account string) error {
	manifest, err := s.manifest(service, account)
	if err != nil {
		line, buildErr := securityLine(s.args("delete-generic-password", "-s", service, "-a", account))
		if buildErr != nil {
			return buildErr
		}
		out, err := s.runSecurity(line, true, "-i")
		clear(out)
		return err
	}
	return s.DeleteParts(service, account, manifest.Size)
}

func (s *keychainHostSecretStore) DeleteParts(service, account string, size int) error {
	parts := (size + keychainPartBytes - 1) / keychainPartBytes
	var commands strings.Builder
	for part := -1; part < parts; part++ {
		name := account
		if part >= 0 {
			name = fmt.Sprintf("%s.part-%d", account, part)
		}
		line, err := securityLine(s.args("delete-generic-password", "-s", service, "-a", name))
		if err != nil {
			return err
		}
		commands.WriteString(line)
	}
	out, err := s.runSecurity(commands.String(), true, "-i")
	clear(out)
	return err
}
