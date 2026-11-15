package boundary_test

import (
	"strings"
	"testing"

	"mc/boundary"
)

func TestBlockPolicyContainsCompleteShippedFloor(t *testing.T) {
	policy, err := boundary.NewBlockPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}

	components := []string{
		".ssh", ".aws", ".azure", ".config", ".docker", ".gnupg", ".kube", ".codex", ".claude",
		".env", ".netrc", ".npmrc", ".pypirc", ".git-credentials",
		"credentials", "secrets", "keychains", "kubeconfig",
	}
	globExamples := []string{
		".env.local",
		"production-credentials-backup.json",
		"auth.json",
		"id_rsa.pub", "id_dsa_old", "id_ecdsa-cert", "id_ed25519_backup",
		"private_key.pem.enc", "private-key-old", "my_private_key_backup", "my-private-key-backup",
		"prod_service_account_key.json", "prod-service-account-key.json",
		"certificate.pem", "secret.key", "identity.p12", "identity.pfx", "identity.ppk",
		"trust.jks", "trust.keystore", "vault.kdbx", "login.keychain-db",
	}

	for _, component := range append(components, globExamples...) {
		t.Run(component, func(t *testing.T) {
			if !policy.Rejects("/safe/"+component+"/child", "/safe/ordinary") {
				t.Fatalf("shipped policy did not reject component %q", component)
			}
		})
	}
}

func TestBlockPolicyMatchesBothSpellingsASCIIInsensitive(t *testing.T) {
	policy, err := boundary.NewBlockPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		raw      string
		resolved string
		want     bool
	}{
		{name: "raw spelling", raw: "/safe/.ssh/link", resolved: "/safe/ordinary", want: true},
		{name: "resolved spelling", raw: "/safe/ordinary", resolved: "/private/.aws/root", want: true},
		{name: "ASCII case insensitive component", raw: "/safe/.SsH", resolved: "/safe/ordinary", want: true},
		{name: "ASCII case insensitive glob", raw: "/safe/LOGIN.KEY", resolved: "/safe/ordinary", want: true},
		{name: "ordinary address", raw: "/safe/project", resolved: "/private/safe/project", want: false},
		{name: "does not scan contents", raw: "/safe/project", resolved: "/safe/project", want: false},
		{name: "component near miss", raw: "/safe/.envx", resolved: "/safe/ordinary", want: false},
		{name: "exact basename anchored", raw: "/safe/auth.json.bak", resolved: "/safe/ordinary", want: false},
		{name: "suffix glob anchored", raw: "/safe/secret.pem.bak", resolved: "/safe/ordinary", want: false},
		{name: "credentials glob anchored", raw: "/safe/credentials.json.bak", resolved: "/safe/ordinary", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := policy.Rejects(tt.raw, tt.resolved); got != tt.want {
				t.Fatalf("Rejects(%q, %q) = %v, want %v", tt.raw, tt.resolved, got, tt.want)
			}
		})
	}
}

func TestZeroValueBlockPolicyStillEnforcesShippedFloor(t *testing.T) {
	var policy boundary.BlockPolicy
	if !policy.Rejects("/safe/.ssh", "/safe/ordinary") {
		t.Fatal("zero-value BlockPolicy omitted the shipped floor")
	}
}

func TestBlockPolicyExtensionsAreAdditive(t *testing.T) {
	policy, err := boundary.NewBlockPolicy([]boundary.BlockPattern{
		{Kind: boundary.PatternComponent, Pattern: ".terraform.d"},
		{Kind: boundary.PatternBasenameGlob, Pattern: "*.agekey"},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/safe/.terraform.d/plugin",
		"/safe/operator.agekey",
		"/safe/.aws/config", // shipped floor remains present
	} {
		if !policy.Rejects(path, "/safe/ordinary") {
			t.Errorf("extended policy did not reject %q", path)
		}
	}
}

// Operator additions match ASCII-case-insensitively in both directions: an
// uppercase spelling on the address side and on the pattern side.
func TestBlockPolicyExtensionsMatchASCIIInsensitive(t *testing.T) {
	policy, err := boundary.NewBlockPolicy([]boundary.BlockPattern{
		{Kind: boundary.PatternComponent, Pattern: ".terraform.d"},
		{Kind: boundary.PatternBasenameGlob, Pattern: "*.agekey"},
		{Kind: boundary.PatternComponent, Pattern: "SECRETSTUFF"},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/safe/.TERRAFORM.D/plugin",
		"/safe/OPERATOR.AGEKEY",
		"/safe/secretstuff/x",
		"/safe/SecretStuff/x",
	} {
		if !policy.Rejects(path, "/safe/ordinary") {
			t.Errorf("extended policy did not reject %q case-insensitively", path)
		}
	}

	if policy.Rejects("/safe/terraform/plugin", "/safe/ordinary") {
		t.Error("extended policy rejected a near-miss component")
	}
}

func TestBlockPolicyRejectsInvalidExtensions(t *testing.T) {
	tests := []struct {
		name    string
		pattern boundary.BlockPattern
	}{
		{name: "unknown kind", pattern: boundary.BlockPattern{Kind: "regex", Pattern: "secret"}},
		{name: "empty component", pattern: boundary.BlockPattern{Kind: boundary.PatternComponent}},
		{name: "non ASCII component", pattern: boundary.BlockPattern{Kind: boundary.PatternComponent, Pattern: "sécret"}},
		{name: "oversize component", pattern: boundary.BlockPattern{Kind: boundary.PatternComponent, Pattern: strings.Repeat("a", 256)}},
		{name: "component slash", pattern: boundary.BlockPattern{Kind: boundary.PatternComponent, Pattern: "secret/key"}},
		{name: "component backslash", pattern: boundary.BlockPattern{Kind: boundary.PatternComponent, Pattern: `secret\key`}},
		{name: "component star", pattern: boundary.BlockPattern{Kind: boundary.PatternComponent, Pattern: "secret*"}},
		{name: "component question", pattern: boundary.BlockPattern{Kind: boundary.PatternComponent, Pattern: "secret?"}},
		{name: "component class", pattern: boundary.BlockPattern{Kind: boundary.PatternComponent, Pattern: "secret[0]"}},
		{name: "empty glob", pattern: boundary.BlockPattern{Kind: boundary.PatternBasenameGlob}},
		{name: "uppercase glob", pattern: boundary.BlockPattern{Kind: boundary.PatternBasenameGlob, Pattern: "*.KEY"}},
		{name: "non ASCII glob", pattern: boundary.BlockPattern{Kind: boundary.PatternBasenameGlob, Pattern: "*sécret*"}},
		{name: "oversize glob", pattern: boundary.BlockPattern{Kind: boundary.PatternBasenameGlob, Pattern: strings.Repeat("a", 256)}},
		{name: "glob slash", pattern: boundary.BlockPattern{Kind: boundary.PatternBasenameGlob, Pattern: "secret/*"}},
		{name: "glob backslash", pattern: boundary.BlockPattern{Kind: boundary.PatternBasenameGlob, Pattern: `secret\*`}},
		{name: "glob question", pattern: boundary.BlockPattern{Kind: boundary.PatternBasenameGlob, Pattern: "secret?"}},
		{name: "glob class", pattern: boundary.BlockPattern{Kind: boundary.PatternBasenameGlob, Pattern: "secret[0]"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := boundary.NewBlockPolicy([]boundary.BlockPattern{tt.pattern}); err == nil {
				t.Fatal("NewBlockPolicy() error = nil, want rejection")
			}
		})
	}

	t.Run("more than 128 additions", func(t *testing.T) {
		additions := make([]boundary.BlockPattern, 129)
		for i := range additions {
			additions[i] = boundary.BlockPattern{Kind: boundary.PatternComponent, Pattern: "safe"}
		}
		if _, err := boundary.NewBlockPolicy(additions); err == nil {
			t.Fatal("NewBlockPolicy() accepted 129 additions")
		}
	})
}

func TestBlockPolicyAllowsClosedGlobSyntax(t *testing.T) {
	policy, err := boundary.NewBlockPolicy([]boundary.BlockPattern{
		{Kind: boundary.PatternBasenameGlob, Pattern: "*"},
	})
	if err != nil {
		t.Fatalf("literal-plus-star grammar rejected star-only deny: %v", err)
	}
	if !policy.Rejects("/anything", "/safe") {
		t.Fatal("star-only additive deny did not match")
	}
}

func TestBlockPolicyAcceptsExactExtensionBounds(t *testing.T) {
	pattern := strings.Repeat("a", 255)
	additions := make([]boundary.BlockPattern, 128)
	for i := range additions {
		additions[i] = boundary.BlockPattern{Kind: boundary.PatternComponent, Pattern: pattern}
	}
	policy, err := boundary.NewBlockPolicy(additions)
	if err != nil {
		t.Fatalf("exact extension bounds rejected: %v", err)
	}
	if !policy.Rejects("/safe/"+pattern, "/safe/ordinary") {
		t.Fatal("maximum-size extension did not match")
	}
}
