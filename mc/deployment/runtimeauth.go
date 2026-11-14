package deployment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"

	operatorconfig "mc/config"
	"mc/routing"
)

const maxRuntimeAuthSourceBytes = 1 << 20

// RuntimeAuthSources names the native credential files of one Culler sign-in.
type RuntimeAuthSources struct {
	Bindings              []string
	CodexAuthFile         string
	ClaudeCredentialsFile string
	Environment           []string
}

// RuntimeCatalog is the operator's binding catalog as the runtime-auth store
// resolves it: every configured binding and every credential source. The
// store keeps one grant per credential source id.
type RuntimeCatalog struct {
	Bindings    map[string]routing.BindingSpec
	Credentials map[string]operatorconfig.CredentialSource
}

// CatalogFromRuntimes projects one validated runtimes section.
func CatalogFromRuntimes(runtimes operatorconfig.Runtimes) RuntimeCatalog {
	credentials := make(map[string]operatorconfig.CredentialSource, len(runtimes.Credentials))
	for id, source := range runtimes.Credentials {
		credentials[id] = source
	}
	return RuntimeCatalog{Bindings: routing.ConfiguredBindings(runtimes), Credentials: credentials}
}

// LoadRuntimeCatalog reads the catalog from MC_HOME/config.toml.
func LoadRuntimeCatalog(home string) (RuntimeCatalog, error) {
	path := filepath.Join(home, "config.toml")
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		return RuntimeCatalog{}, fmt.Errorf("load operator config %q: %w", path, err)
	}
	return CatalogFromRuntimes(cfg.Runtimes), nil
}

// Binding resolves one configured binding.
func (c RuntimeCatalog) Binding(id string) (routing.BindingSpec, bool) {
	spec, ok := c.Bindings[id]
	return spec, ok
}

func (c RuntimeCatalog) validateBinding(id string, allowDefaultModel bool) error {
	spec, ok := c.Bindings[id]
	if !ok {
		return fmt.Errorf("unknown runtime binding %q", id)
	}
	source, ok := c.Credentials[spec.Credential]
	if !ok {
		return fmt.Errorf("runtime binding %q references undefined credential %q", id, spec.Credential)
	}
	if err := operatorconfig.ValidateCredentialSource(spec.Credential, source); err != nil {
		return err
	}
	profile := operatorconfig.RuntimeBinding{
		Harness: spec.Harness, Model: spec.Model, Effort: spec.Effort, Credential: spec.Credential, BaseURL: spec.BaseURL,
	}
	checked := profile
	if allowDefaultModel && checked.Model == "" && source.Kind == operatorconfig.CredentialKindCodexOAuth {
		// Setup validates the selected model after the first successful response.
		checked.Model = "default"
	}
	if err := operatorconfig.ValidateBindingShape(id, checked, source.Kind); err != nil {
		return err
	}
	expected := routing.ConfiguredBindings(operatorconfig.Runtimes{
		Credentials: map[string]operatorconfig.CredentialSource{spec.Credential: source},
		Bindings:    map[string]operatorconfig.RuntimeBinding{id: profile},
	})[id]
	spec.AuthorizationID, spec.ProfileRevision = "", ""
	if !reflect.DeepEqual(spec, expected) {
		return fmt.Errorf("runtime binding %q differs from its sign-in connection", id)
	}
	return nil
}

// credentialsOf resolves the sorted, duplicate-free credential source ids of
// the given bindings.
func (c RuntimeCatalog) credentialsOf(bindings []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, binding := range bindings {
		if err := c.validateBinding(binding, true); err != nil {
			return nil, err
		}
		spec := c.Bindings[binding]
		if !seen[spec.Credential] {
			seen[spec.Credential] = true
			out = append(out, spec.Credential)
		}
	}
	sort.Strings(out)
	return out, nil
}

// OAuthKindsOf reports which OAuth logins the given bindings need.
func (c RuntimeCatalog) OAuthKindsOf(bindings []string) (wantsCodex, wantsClaude bool, err error) {
	credentials, err := c.credentialsOf(bindings)
	if err != nil {
		return false, false, err
	}
	for _, id := range credentials {
		switch c.Credentials[id].Kind {
		case operatorconfig.CredentialKindCodexOAuth:
			wantsCodex = true
		case operatorconfig.CredentialKindClaudeOAuth:
			wantsClaude = true
		}
	}
	return wantsCodex, wantsClaude, nil
}

type RuntimeAuthVerifier interface {
	Verify(binding, stagedGrantDir string) error
}

type RuntimeAuthVerifyFunc func(binding, stagedGrantDir string) error

func (f RuntimeAuthVerifyFunc) Verify(binding, stagedGrantDir string) error {
	return f(binding, stagedGrantDir)
}

type codexAuthSource struct {
	AuthMode     string  `json:"auth_mode"`
	OpenAIAPIKey *string `json:"OPENAI_API_KEY,omitempty"`
	Tokens       struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh string `json:"last_refresh,omitempty"`
}

// claudeCredentialsSource holds the claudeAiOauth fields that Culler reads.
// Claude Code adds fields to this object, so the parse ignores the other fields.
type claudeCredentialsSource struct {
	ClaudeAIOAuth struct {
		AccessToken           string   `json:"accessToken"`
		RefreshToken          string   `json:"refreshToken"`
		ExpiresAt             int64    `json:"expiresAt"`
		RefreshTokenExpiresAt int64    `json:"refreshTokenExpiresAt"`
		Scopes                []string `json:"scopes"`
		ClientID              string   `json:"clientId"`
	} `json:"claudeAiOauth"`
}

// oauthRuntimeGrant is one OAuth credential source's stored grant, keyed by
// the credential source id.
type oauthRuntimeGrant = ProviderCredentialRecord

func statOwner(info os.FileInfo) (uint32, uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return st.Uid, uint64(st.Nlink), true
}

func requireOwnerOnlyDirectory(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s %q: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %q must not be a symlink", label, path)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s %q must be an owner-only directory with mode 0700", label, path)
	}
	uid, _, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() {
		return fmt.Errorf("%s %q must be owned by the current operator", label, path)
	}
	return nil
}

func readSecretSource(path, label string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("%s source file is required", label)
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s source %q: %w", label, path, err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s source %q must not be a symlink", label, path)
	}
	if !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%s source %q must be a regular file with mode 0600", label, path)
	}
	uid, links, ok := statOwner(before)
	if !ok || int(uid) != os.Getuid() || links != 1 {
		return nil, fmt.Errorf("%s source %q must be singly linked and owned by the current operator", label, path)
	}
	if before.Size() <= 0 || before.Size() > maxRuntimeAuthSourceBytes {
		return nil, fmt.Errorf("%s source %q has invalid size", label, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s source %q: %w", label, path, err)
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, fmt.Errorf("%s source %q changed during inspection", label, path)
	}
	body, err := io.ReadAll(io.LimitReader(f, maxRuntimeAuthSourceBytes+1))
	if err != nil || len(body) > maxRuntimeAuthSourceBytes {
		return nil, fmt.Errorf("read %s source %q: invalid bounded content", label, path)
	}
	return body, nil
}

func normalizeIsolatedSourcePath(home, path, label string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%s source file is required", label)
	}
	root := runtimeAuthSourceRoot(home)
	if err := requireOwnerOnlyDirectory(root, "runtime-auth source root"); err != nil {
		return "", err
	}
	parent, err := CanonicalHome(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("resolve %s source parent: %w", label, err)
	}
	normalized := filepath.Join(parent, filepath.Base(path))
	rel, err := filepath.Rel(root, normalized)
	if err != nil || rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%s source must live in the isolated host credential source directory", label)
	}
	for dir := filepath.Dir(normalized); dir != filepath.Dir(root); dir = filepath.Dir(dir) {
		if err := requireOwnerOnlyDirectory(dir, "runtime-auth source directory"); err != nil {
			return "", err
		}
		if dir == root {
			break
		}
	}
	return normalized, nil
}

func decodeRuntimeAuthJSON(body []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

// validateRuntimeBindings checks the selection against the catalog and
// returns the sorted binding ids.
func validateRuntimeBindings(catalog RuntimeCatalog, bindings []string) ([]string, error) {
	if len(bindings) == 0 {
		return nil, fmt.Errorf("at least one runtime binding is required")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		if _, ok := catalog.Bindings[binding]; !ok {
			return nil, fmt.Errorf("unknown runtime binding %q", binding)
		}
		if seen[binding] {
			return nil, fmt.Errorf("duplicate runtime binding %q", binding)
		}
		seen[binding] = true
		out = append(out, binding)
	}
	sort.Strings(out)
	if _, err := catalog.credentialsOf(out); err != nil {
		return nil, err
	}
	return out, nil
}

func validateRuntimeAuthEnvironment(environment []string) error {
	known := map[string]bool{
		"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true,
		"CLAUDE_CODE_OAUTH_TOKEN": true, "CODEX_ACCESS_TOKEN": true,
		"CODEX_API_KEY": true, "OPENAI_API_KEY": true,
	}
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || value == "" {
			continue
		}
		if known[name] {
			return fmt.Errorf("forbidden ambient runtime credential %s is set; sign in through Culler", name)
		}
	}
	return nil
}

func buildRuntimeGrant(credential string, source operatorconfig.CredentialSource, sources RuntimeAuthSources) (any, error) {
	var body []byte
	var err error
	switch source.Kind {
	case operatorconfig.CredentialKindCodexOAuth:
		body, err = readSecretSource(sources.CodexAuthFile, "Codex auth")
	case operatorconfig.CredentialKindClaudeOAuth:
		body, err = readSecretSource(sources.ClaudeCredentialsFile, "Claude credentials")
	default:
		return nil, fmt.Errorf("credential %q has unsupported kind %q", credential, source.Kind)
	}
	if err != nil {
		return nil, err
	}
	return buildRuntimeGrantBody(credential, source, body)
}

// claudeAIOAuthObject returns the subscription credential of a Claude Code
// credential store. The store can also hold secrets for other services, such
// as MCP server tokens. Culler uses none of them.
func claudeAIOAuthObject(body []byte) (json.RawMessage, error) {
	var envelope struct {
		ClaudeAIOAuth json.RawMessage `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse Claude credentials source: %w", err)
	}
	if len(envelope.ClaudeAIOAuth) == 0 {
		return nil, fmt.Errorf("Claude credentials source has no claudeAiOauth object")
	}
	return envelope.ClaudeAIOAuth, nil
}

// claudeSubscriptionCredential reduces a Claude Code credential store to its
// subscription credential, so a stage copy holds no other secret.
func claudeSubscriptionCredential(body []byte) ([]byte, error) {
	subscription, err := claudeAIOAuthObject(body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]json.RawMessage{"claudeAiOauth": subscription})
}

func buildRuntimeGrantBody(credential string, source operatorconfig.CredentialSource, body []byte) (any, error) {
	switch source.Kind {
	case operatorconfig.CredentialKindCodexOAuth:
		var parsed codexAuthSource
		// Native stores can add metadata. Only the required account fields enter the protected record.
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, &AuthorizationFailure{Code: "credential_invalid", ExitCode: -1}
		}
		if parsed.AuthMode != "" && parsed.AuthMode != "chatgpt" {
			return nil, &AuthorizationFailure{Code: "credential_mode", ExitCode: -1}
		}
		if parsed.OpenAIAPIKey != nil && *parsed.OpenAIAPIKey != "" {
			return nil, &AuthorizationFailure{Code: "credential_api_key", ExitCode: -1}
		}
		for _, field := range []struct{ name, value string }{
			{"access", parsed.Tokens.AccessToken}, {"id", parsed.Tokens.IDToken},
			{"refresh", parsed.Tokens.RefreshToken}, {"account", parsed.Tokens.AccountID},
		} {
			if strings.TrimSpace(field.value) == "" {
				return nil, &AuthorizationFailure{Code: "credential_" + field.name, ExitCode: -1}
			}
		}
		return oauthRuntimeGrant{Credential: credential, Channel: source.Channel(), TokenURL: source.TokenURL,
			ClientID: source.ClientID, RefreshToken: parsed.Tokens.RefreshToken, AccountID: parsed.Tokens.AccountID,
			AccessToken: parsed.Tokens.AccessToken, IDToken: parsed.Tokens.IDToken}, nil
	case operatorconfig.CredentialKindClaudeOAuth:
		subscription, err := claudeAIOAuthObject(body)
		if err != nil {
			return nil, err
		}
		var parsed claudeCredentialsSource
		if err := json.Unmarshal(subscription, &parsed.ClaudeAIOAuth); err != nil {
			return nil, fmt.Errorf("parse Claude credentials source: %w", err)
		}
		oauth := parsed.ClaudeAIOAuth
		if oauth.ClientID != "" && source.ClientID != "" && oauth.ClientID != source.ClientID {
			return nil, fmt.Errorf("Claude credentials source belongs to another OAuth client")
		}
		hasInference := false
		for _, scope := range oauth.Scopes {
			if scope == "user:inference" {
				hasInference = true
			}
		}
		if oauth.AccessToken == "" || oauth.RefreshToken == "" || oauth.ExpiresAt <= 0 || !hasInference {
			return nil, fmt.Errorf("Claude credentials source needs access/refresh/expiry evidence and user:inference scope")
		}
		return oauthRuntimeGrant{Credential: credential, Channel: source.Channel(), TokenURL: source.TokenURL,
			ClientID: source.ClientID, RefreshToken: oauth.RefreshToken, Scope: strings.Join(oauth.Scopes, " "),
			AccessToken: oauth.AccessToken, ExpiresAtMS: oauth.ExpiresAt, RefreshExpiresAtMS: oauth.RefreshTokenExpiresAt}, nil
	}
	return nil, fmt.Errorf("credential %q has unsupported kind %q", credential, source.Kind)
}

func writeRuntimeGrant(dir, credential string, grant any) error {
	body, err := json.MarshalIndent(grant, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	path := filepath.Join(dir, credential+".json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

// validateStagedRuntimeGrants checks that the directory holds exactly one
// well-formed grant per credential source, in sorted order.
func validateStagedRuntimeGrants(dir string, catalog RuntimeCatalog, credentials []string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != len(credentials) {
		return fmt.Errorf("staged grant set changed shape during live verification")
	}
	for i, credential := range credentials {
		entry := entries[i]
		if entry.Name() != credential+".json" || entry.IsDir() {
			return fmt.Errorf("staged grant set contains unexpected entry %q", entry.Name())
		}
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
			return fmt.Errorf("staged grant %s lost its mode-0600 regular-file boundary", credential)
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		var reference providerCredentialReference
		if json.Unmarshal(body, &reference) == nil && reference.CredentialRef != "" {
			clear(body)
			body, err = ReadHostSecret(filepath.Dir(dir), "provider", reference.CredentialRef)
			if err != nil {
				return err
			}
		}
		defer clear(body)
		source, ok := catalog.Credentials[credential]
		if !ok {
			return fmt.Errorf("staged grant %s names no configured credential source", credential)
		}
		if !agentConnectionKind(source.Kind) {
			return fmt.Errorf("The connection type is unsupported.")
		}
		if agentAPIKeyKind(source.Kind) {
			record, err := normalizeProviderCredential(credential, json.RawMessage(body))
			if err != nil || record.Type != "api_key" || record.Channel != source.Channel() {
				return fmt.Errorf("The API key credential differs from its connection.")
			}
			continue
		}
		var grant oauthRuntimeGrant
		if err := decodeRuntimeAuthJSON(body, &grant); err != nil || grant.Credential != credential ||
			grant.Channel != source.Channel() || grant.TokenURL != source.TokenURL || grant.ClientID != source.ClientID || grant.RefreshToken == "" {
			return fmt.Errorf("staged grant %s is no longer the closed OAuth shape", credential)
		}
		if source.Kind == operatorconfig.CredentialKindCodexOAuth && grant.AccountID == "" {
			return fmt.Errorf("staged Codex grant %s lost its account identity", credential)
		}
		if source.Kind == operatorconfig.CredentialKindClaudeOAuth && !strings.Contains(" "+grant.Scope+" ", " user:inference ") {
			return fmt.Errorf("staged Claude grant %s lost user:inference scope", credential)
		}
	}
	return nil
}

// ValidateRuntimeGrantStore is the token-free replay check used by the whole
// wizard. It validates the exact canonical grant set and forbidden host
// environment without minting or spending a provider turn.
func ValidateRuntimeGrantStore(home string, bindings, environment []string) error {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return err
	}
	if err := requireOwnerOnlyDirectory(canonicalHome, "MC_HOME"); err != nil {
		return err
	}
	catalog, err := LoadRuntimeCatalog(canonicalHome)
	if err != nil {
		return err
	}
	bindings, err = validateRuntimeBindings(catalog, bindings)
	if err != nil {
		return err
	}
	credentials, err := catalog.credentialsOf(bindings)
	if err != nil {
		return err
	}
	if err := validateRuntimeAuthEnvironment(environment); err != nil {
		return err
	}
	store := filepath.Join(canonicalHome, "refresh-grants")
	if err := requireOwnerOnlyDirectory(store, "runtime grant store"); err != nil {
		return err
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	all := []string{}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		source, ok := catalog.Credentials[id]
		if entry.IsDir() || entry.Name() != id+".json" || !ok || !agentConnectionKind(source.Kind) {
			return fmt.Errorf("The credential store contains an unknown connection.")
		}
		present[id] = true
		all = append(all, id)
	}
	for _, id := range credentials {
		if !present[id] {
			return fmt.Errorf("The enabled connection %s requires credentials.", id)
		}
	}
	sort.Strings(all)
	return validateStagedRuntimeGrants(store, catalog, all)
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func directoriesEqual(left, right string) (bool, error) {
	leftEntries, err := os.ReadDir(left)
	if err != nil {
		return false, err
	}
	rightEntries, err := os.ReadDir(right)
	if err != nil {
		return false, err
	}
	if len(leftEntries) != len(rightEntries) {
		return false, nil
	}
	for i := range leftEntries {
		if leftEntries[i].Name() != rightEntries[i].Name() || leftEntries[i].IsDir() || rightEntries[i].IsDir() {
			return false, nil
		}
		leftBody, err := os.ReadFile(filepath.Join(left, leftEntries[i].Name()))
		if err != nil {
			return false, err
		}
		rightBody, err := os.ReadFile(filepath.Join(right, rightEntries[i].Name()))
		if err != nil {
			return false, err
		}
		if !bytes.Equal(leftBody, rightBody) {
			return false, nil
		}
	}
	return true, nil
}

// ImportRuntimeAuth validates every selected provider-native source, builds
// the resident's closed grant union in a private directory, runs every live
// gate against those staged bytes, and only then publishes the whole store.
func ImportRuntimeAuth(home string, sources RuntimeAuthSources, verifier RuntimeAuthVerifier) (string, error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return "", fmt.Errorf("resolve runtime-auth MC_HOME: %w", err)
	}
	home = canonicalHome
	if err := requireOwnerOnlyDirectory(home, "MC_HOME"); err != nil {
		return "", err
	}
	catalog, err := LoadRuntimeCatalog(home)
	if err != nil {
		return "", err
	}
	bindings, err := validateRuntimeBindings(catalog, sources.Bindings)
	if err != nil {
		return "", err
	}
	if err := validateRuntimeAuthEnvironment(sources.Environment); err != nil {
		return "", err
	}
	if verifier == nil {
		return "", fmt.Errorf("runtime-auth live verifier is unavailable")
	}
	wantsCodex, wantsClaude, err := catalog.OAuthKindsOf(bindings)
	if err != nil {
		return "", err
	}
	if wantsCodex {
		sources.CodexAuthFile, err = normalizeIsolatedSourcePath(home, sources.CodexAuthFile, "Codex auth")
		if err != nil {
			return "", err
		}
	}
	if wantsClaude {
		sources.ClaudeCredentialsFile, err = normalizeIsolatedSourcePath(home, sources.ClaudeCredentialsFile, "Claude credentials")
		if err != nil {
			return "", err
		}
	}
	return importPreparedRuntimeAuth(home, catalog, bindings, verifier, func(credential string) (any, error) {
		return buildRuntimeGrant(credential, catalog.Credentials[credential], sources)
	})
}

func importPreparedRuntimeAuth(
	home string,
	catalog RuntimeCatalog,
	bindings []string,
	verifier RuntimeAuthVerifier,
	buildGrant func(credential string) (any, error),
) (string, error) {
	credentials, err := catalog.credentialsOf(bindings)
	if err != nil {
		return "", err
	}
	sourceRoot, err := prepareRuntimeAuthSourceRoot(home)
	if err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(sourceRoot, ".runtime-auth-import-")
	if err != nil {
		return "", fmt.Errorf("create runtime-auth staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	for _, credential := range credentials {
		grant, err := buildGrant(credential)
		if err != nil {
			return "", fmt.Errorf("credential %s: %w", credential, err)
		}
		if err := writeRuntimeGrant(stage, credential, grant); err != nil {
			return "", fmt.Errorf("stage credential %s: %w", credential, err)
		}
	}
	if err := syncDirectory(stage); err != nil {
		return "", fmt.Errorf("sync runtime-auth staging directory: %w", err)
	}
	if err := validateStagedRuntimeGrants(stage, catalog, credentials); err != nil {
		return "", fmt.Errorf("revalidate live-verified runtime grants: %w", err)
	}
	if err := syncDirectory(stage); err != nil {
		return "", fmt.Errorf("sync live-verified runtime grants: %w", err)
	}
	if _, err := protectStagedProviderCredentials(home, stage, credentials); err != nil {
		return "", err
	}
	metadataStage, err := os.MkdirTemp(home, ".runtime-auth-references-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(metadataStage)
	for _, credential := range credentials {
		body, err := os.ReadFile(filepath.Join(stage, credential+".json"))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(metadataStage, credential+".json"), body, 0600); err != nil {
			return "", err
		}
	}
	if err := syncDirectory(metadataStage); err != nil {
		return "", err
	}
	stage = metadataStage
	canonical := filepath.Join(home, "refresh-grants")
	verifySaved := func(status string) (string, error) {
		for _, binding := range bindings {
			if err := verifier.Verify(binding, canonical); err != nil {
				return "", fmt.Errorf("runtime binding %s model test: %w", binding, err)
			}
		}
		return status, nil
	}
	if err := requireOwnerOnlyDirectory(canonical, "runtime grant store"); err == nil {
		equal, err := directoriesEqual(stage, canonical)
		if err != nil {
			return "", fmt.Errorf("compare runtime grant store: %w", err)
		}
		if equal {
			return verifySaved("ok")
		}
		if err := atomicExchangeDirectories(stage, canonical); err != nil {
			return "", fmt.Errorf("atomically exchange runtime grant store: %w", err)
		}
		if err := syncDirectory(home); err != nil {
			return "", fmt.Errorf("sync MC_HOME after runtime grant exchange: %w", err)
		}
		deleteReplacedProviderReferences(home, stage, canonical)
		if err := os.RemoveAll(stage); err != nil {
			return "", fmt.Errorf("remove replaced runtime grant store: %w", err)
		}
		if err := syncDirectory(home); err != nil {
			return "", fmt.Errorf("sync MC_HOME after old grant removal: %w", err)
		}
		return verifySaved("done")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(stage, canonical); err != nil {
		return "", fmt.Errorf("publish runtime grant store: %w", err)
	}
	if err := syncDirectory(home); err != nil {
		return "", fmt.Errorf("sync MC_HOME after runtime grant publication: %w", err)
	}
	return verifySaved("done")
}
