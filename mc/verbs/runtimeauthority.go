package verbs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/opencodecatalog"
	"mc/routing"
)

// runtimeAuthority is the single host-side projection of config.toml used by
// Doctor, Homie route capture, and dispatch attestation. It contains no secret
// values: only the validated enabled catalog, its route registry, and the
// canonical digest that fences reconfiguration across an in-flight dispatch.
type runtimeAuthority struct {
	config             operatorconfig.Config
	registry           routing.Registry
	bindings           map[string]routing.BindingSpec
	configuredBindings map[string]routing.BindingSpec
	digest             string
	allowFake          bool
}

func loadRuntimeAuthority(home string) (runtimeAuthority, error) {
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {

		return runtimeAuthority{}, err
	}
	digest, err := operatorconfig.RuntimeCatalogDigest(cfg.Runtimes)
	if err != nil {
		return runtimeAuthority{}, err
	}
	registry := routing.RegistryFromRuntimes(cfg.Runtimes)
	bindings := routing.EnabledBindings(cfg.Runtimes)
	configuredBindings := routing.ConfiguredBindings(cfg.Runtimes)
	_, allowFake := routing.ActiveRegistry()
	if allowFake {
		registry["fake"] = "fake"
		if spec, ok := routing.ActiveBinding("fake"); ok {
			bindings["fake"] = spec
			configuredBindings["fake"] = spec
		}
	}
	for key, spec := range bindings {
		if spec.Harness == "opencode" {
			connection := cfg.Runtimes.Credentials[spec.Credential]
			if opencodecatalog.ValidateProfile(connection.CatalogEntry, spec.Model, spec.Effort) != nil {
				delete(bindings, key)
				continue
			}
		}
		if spec.Harness == "fake" {
			continue
		}
		auth, err := deployment.ReadConnectionAuthorizationIdentity(home, spec.Credential)
		if err != nil {
			auth = "unavailable"
		}
		spec.AuthorizationID = auth
		spec.ProfileRevision = routing.ProfileRevision(spec)
		bindings[key] = spec
		configuredBindings[key] = spec
	}
	authorityBytes, _ := json.Marshal(struct {
		Config   string
		Bindings map[string]routing.BindingSpec
	}{digest, bindings})
	authorityHash := sha256.Sum256(authorityBytes)
	digest = hex.EncodeToString(authorityHash[:])
	return runtimeAuthority{
		config: cfg, registry: registry, bindings: bindings,
		configuredBindings: configuredBindings,
		digest:             digest, allowFake: allowFake,
	}, nil
}

// capability projects the enabled catalog into the closed prepare-side carrier,
// strictly ordered by binding id so the frame is byte-deterministic.
func (a runtimeAuthority) capability() PrivateRuntimeCapability {
	ids := make([]string, 0, len(a.bindings))
	for id := range a.bindings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	capability := PrivateRuntimeCapability{
		Digest:   a.digest,
		Bindings: make([]PrivateRuntimeBinding, 0, len(ids)),
	}
	for _, id := range ids {
		capability.Bindings = append(capability.Bindings, privateRuntimeBindingFromSpec(a.bindings[id]))
	}
	return capability
}

// UnavailableRuntimeCapability is the explicit "this host resolved no catalog"
// value. It is a real carrier rather than a zero struct so the closed frame
// always encodes an empty list instead of JSON null, and so every caller that
// cannot read config states that fact rather than omitting the field.
func UnavailableRuntimeCapability() PrivateRuntimeCapability {
	return PrivateRuntimeCapability{Bindings: []PrivateRuntimeBinding{}}
}

// ResolveRuntimeCapability is the ONE host crossing that reads config.toml for
// the prepare-side carrier. A deployment whose config is missing or invalid
// resolves the explicit unavailable capability rather than an error: the
// pipeline lane still owes its own config refusal at attest, and the Homie lane
// simply launches nothing until the operator repairs config.toml.
func ResolveRuntimeCapability(home string) PrivateRuntimeCapability {
	authority, err := loadRuntimeAuthority(home)
	if err != nil {
		return UnavailableRuntimeCapability()
	}
	return authority.capability()
}

// RuntimeCapabilityDrifted is the immediate post-prepare fence. A final effect
// that consumed the carried capability names the digest it resolved against;
// re-reading config.toml here is what keeps a wake from launching a binding the
// operator disabled while prepare held the lock. An unconsumed capability
// (empty digest) never drifts.
func RuntimeCapabilityDrifted(home, consumedDigest string) bool {
	if consumedDigest == "" {
		return false
	}
	return ResolveRuntimeCapability(home).Digest != consumedDigest
}

// resolveFrozenHomieBinding resolves a Homie session's frozen historical
// binding ("<harness>/<id>") against the carried enabled capability.
//
// It deliberately never consults routing.md: the session's route is frozen at
// start so that later resume is independent of routing drift. Config is a
// DIFFERENT authority — a binding the operator has since disabled or deleted is
// no longer a capability this deployment has at all, so the session cannot wake
// on it. Resolution failure is inert: the caller mutates nothing, and the
// session idles out through branch 6 like any other quiet session.
func resolveFrozenHomieBinding(capability PrivateRuntimeCapability, binding string) (PrivateRuntimeBinding, bool) {
	if !validLowercaseHex(capability.Digest, 64) {
		return PrivateRuntimeBinding{}, false
	}
	harness, id, ok := strings.Cut(binding, "/")
	if !ok || harness == "" || id == "" || strings.Contains(id, "/") {
		return PrivateRuntimeBinding{}, false
	}
	for _, spec := range capability.Bindings {
		if spec.ID == id && spec.Harness == harness {
			return spec, true
		}
	}
	return PrivateRuntimeBinding{}, false
}
