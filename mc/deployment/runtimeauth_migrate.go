package deployment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// legacyRuntimeGrant identifies a sign-in grant by its former binding identifier.
type legacyRuntimeGrant struct {
	Binding      string `json:"binding"`
	Channel      string `json:"channel"`
	TokenURL     string `json:"token_url,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// MigrateRuntimeGrantStore rewrites every grant that a binding id keys into
// the grant of the binding's credential source. The new grant takes its
// authority from config.toml. It returns the credential source ids it wrote,
// sorted. A store without a legacy grant stays unchanged.
func MigrateRuntimeGrantStore(home string) ([]string, error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return nil, err
	}
	store := filepath.Join(canonicalHome, "refresh-grants")
	entries, err := os.ReadDir(store)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type legacyEntry struct {
		name  string
		grant legacyRuntimeGrant
	}
	var pending []legacyEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(store, entry.Name()))
		if err != nil {
			return nil, err
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(body, &keys); err != nil {
			return nil, fmt.Errorf("runtime grant %s: %w", entry.Name(), err)
		}
		if _, legacy := keys["binding"]; !legacy {
			continue
		}
		if _, current := keys["credential"]; current {
			return nil, fmt.Errorf("runtime grant %s carries both a binding and a credential", entry.Name())
		}
		var grant legacyRuntimeGrant
		if err := decodeRuntimeAuthJSON(body, &grant); err != nil {
			return nil, fmt.Errorf("runtime grant %s: %w", entry.Name(), err)
		}
		pending = append(pending, legacyEntry{name: entry.Name(), grant: grant})
	}
	if len(pending) == 0 {
		return nil, nil
	}
	catalog, err := LoadRuntimeCatalog(canonicalHome)
	if err != nil {
		return nil, err
	}
	var migrated []string
	for _, item := range pending {
		spec, ok := catalog.Binding(item.grant.Binding)
		if !ok {
			return nil, fmt.Errorf("runtime grant %s names binding %q, which config.toml does not carry", item.name, item.grant.Binding)
		}
		source, ok := catalog.Credentials[spec.Credential]
		if !ok {
			return nil, fmt.Errorf("binding %q names credential source %q, which config.toml does not carry", spec.ID, spec.Credential)
		}
		if item.grant.Channel != source.Channel() {
			return nil, fmt.Errorf("runtime grant %s has channel %q; credential source %q has channel %q",
				item.name, item.grant.Channel, spec.Credential, source.Channel())
		}
		if !agentSubscriptionKind(source.Kind) {
			return nil, fmt.Errorf("The connection type is unsupported.")
		}
		if item.grant.RefreshToken == "" {
			return nil, fmt.Errorf("runtime grant %s carries no refresh token", item.name)
		}
		grant := oauthRuntimeGrant{Credential: spec.Credential, Channel: source.Channel(),
			TokenURL: source.TokenURL, ClientID: source.ClientID, RefreshToken: item.grant.RefreshToken,
			AccountID: item.grant.AccountID, Scope: item.grant.Scope}
		target := filepath.Join(store, spec.Credential+".json")
		if item.name != spec.Credential+".json" {
			if _, err := os.Lstat(target); err == nil {
				return nil, fmt.Errorf("runtime grant %s cannot move to %s: the file exists", item.name, spec.Credential+".json")
			}
		}
		if err := replaceRuntimeGrant(target, grant); err != nil {
			return nil, fmt.Errorf("rewrite runtime grant %s: %w", item.name, err)
		}
		if item.name != spec.Credential+".json" {
			if err := os.Remove(filepath.Join(store, item.name)); err != nil {
				return nil, err
			}
		}
		migrated = append(migrated, spec.Credential)
	}
	sort.Strings(migrated)
	if err := syncDirectory(store); err != nil {
		return nil, err
	}
	return migrated, nil
}
