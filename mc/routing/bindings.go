package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	operatorconfig "mc/config"
)

// CredentialDelivery identifies the host service that receives model requests.
type CredentialDelivery string

const ProviderProxy CredentialDelivery = "proxy"

// BindingSpec is the runtime shape of one configured binding: the binding's
// own fields plus the facts its credential source selects. The credential
// source id names the grant the runtime-auth store keeps for the binding.
type BindingSpec struct {
	AuthorizationID        string
	ProfileRevision        string
	ID                     string
	Harness                string
	Model                  string
	Effort                 string `json:",omitempty"`
	Credential             string
	Channel                string
	AuthKind               string
	Delivery               CredentialDelivery
	ProviderCredentialKeys []string
	DeclaredStaticKey      string
	BaseURL                string
	TokenURL               string
	ClientID               string
}

// ProviderCredentialKeys lists the ambient environment names that a channel
// must never receive from the host.
func ProviderCredentialKeys(channel string) []string {
	switch channel {
	case "codex":
		return []string{"CODEX_ACCESS_TOKEN", "CODEX_API_KEY", "OPENAI_API_KEY"}
	case "claude":
		return []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}
	}
	return nil
}

func bindingSpec(id string, binding operatorconfig.RuntimeBinding, source operatorconfig.CredentialSource) BindingSpec {
	spec := BindingSpec{
		ID: id, Harness: binding.Harness, Model: binding.Model, Effort: binding.Effort,
		Credential: binding.Credential, Channel: source.Channel(),
		AuthKind: source.Kind, Delivery: CredentialDelivery(source.Delivery()),
		BaseURL: binding.BaseURL, TokenURL: source.TokenURL, ClientID: source.ClientID,
		ProviderCredentialKeys: ProviderCredentialKeys(source.Channel()),
	}
	return spec
}

// ConfiguredBindings projects every validated non-secret config binding into
// the runtime shape shared by routing, auth, supervision, and adapters.
func ConfiguredBindings(runtimes operatorconfig.Runtimes) map[string]BindingSpec {
	out := make(map[string]BindingSpec, len(runtimes.Bindings))
	for id, binding := range runtimes.Bindings {
		out[id] = bindingSpec(id, binding, runtimes.Credentials[binding.Credential])
	}
	return out
}

// EnabledBindings is the machine-global catalog that routing may reference.
func EnabledBindings(runtimes operatorconfig.Runtimes) map[string]BindingSpec {
	out := make(map[string]BindingSpec, len(runtimes.EnabledBindings))
	for _, id := range runtimes.EnabledBindings {
		if binding, ok := runtimes.Bindings[id]; ok {
			out[id] = bindingSpec(id, binding, runtimes.Credentials[binding.Credential])
		}
	}
	return out
}

func cloneBindingSpec(spec BindingSpec) BindingSpec {
	spec.ProviderCredentialKeys = append([]string(nil), spec.ProviderCredentialKeys...)
	return spec
}

// ProductionBinding resolves one binding of the shipped reference catalog.
// The operator configuration, not this catalog, is the authority for a
// deployment; consumers that serve a deployment read config.toml.
func ProductionBinding(id string) (BindingSpec, bool) {
	spec, ok := ConfiguredBindings(operatorconfig.Default().Runtimes)[id]
	return cloneBindingSpec(spec), ok
}

// ProductionBindings is the shipped reference catalog.
func ProductionBindings() map[string]BindingSpec {
	return ConfiguredBindings(operatorconfig.Default().Runtimes)
}

// ProfileRevision identifies the configuration that controls one model request.
func ProfileRevision(spec BindingSpec) string {
	spec.AuthorizationID = ""
	spec.ProfileRevision = ""
	body, _ := json.Marshal(spec)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
