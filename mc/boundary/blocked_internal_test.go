package boundary

import (
	"reflect"
	"testing"
)

func TestShippedBlockedFloorExact(t *testing.T) {
	want := [...]BlockPattern{
		{Kind: PatternComponent, Pattern: ".ssh"},
		{Kind: PatternComponent, Pattern: ".aws"},
		{Kind: PatternComponent, Pattern: ".azure"},
		{Kind: PatternComponent, Pattern: ".config"},
		{Kind: PatternComponent, Pattern: ".docker"},
		{Kind: PatternComponent, Pattern: ".gnupg"},
		{Kind: PatternComponent, Pattern: ".kube"},
		{Kind: PatternComponent, Pattern: ".codex"},
		{Kind: PatternComponent, Pattern: ".claude"},
		{Kind: PatternComponent, Pattern: ".env"},
		{Kind: PatternComponent, Pattern: ".netrc"},
		{Kind: PatternComponent, Pattern: ".npmrc"},
		{Kind: PatternComponent, Pattern: ".pypirc"},
		{Kind: PatternComponent, Pattern: ".git-credentials"},
		{Kind: PatternComponent, Pattern: "credentials"},
		{Kind: PatternComponent, Pattern: "secrets"},
		{Kind: PatternComponent, Pattern: "keychains"},
		{Kind: PatternComponent, Pattern: "kubeconfig"},
		{Kind: PatternBasenameGlob, Pattern: ".env.*"},
		{Kind: PatternBasenameGlob, Pattern: "*credentials*.json"},
		{Kind: PatternBasenameGlob, Pattern: "auth.json"},
		{Kind: PatternBasenameGlob, Pattern: "id_rsa*"},
		{Kind: PatternBasenameGlob, Pattern: "id_dsa*"},
		{Kind: PatternBasenameGlob, Pattern: "id_ecdsa*"},
		{Kind: PatternBasenameGlob, Pattern: "id_ed25519*"},
		{Kind: PatternBasenameGlob, Pattern: "private_key*"},
		{Kind: PatternBasenameGlob, Pattern: "private-key*"},
		{Kind: PatternBasenameGlob, Pattern: "*private_key*"},
		{Kind: PatternBasenameGlob, Pattern: "*private-key*"},
		{Kind: PatternBasenameGlob, Pattern: "*service_account*.json"},
		{Kind: PatternBasenameGlob, Pattern: "*service-account*.json"},
		{Kind: PatternBasenameGlob, Pattern: "*.pem"},
		{Kind: PatternBasenameGlob, Pattern: "*.key"},
		{Kind: PatternBasenameGlob, Pattern: "*.p12"},
		{Kind: PatternBasenameGlob, Pattern: "*.pfx"},
		{Kind: PatternBasenameGlob, Pattern: "*.ppk"},
		{Kind: PatternBasenameGlob, Pattern: "*.jks"},
		{Kind: PatternBasenameGlob, Pattern: "*.keystore"},
		{Kind: PatternBasenameGlob, Pattern: "*.kdbx"},
		{Kind: PatternBasenameGlob, Pattern: "*.keychain-db"},
	}

	if !reflect.DeepEqual(shippedBlockedPatterns, want) {
		t.Fatalf("shipped blocked floor drifted:\n got: %#v\nwant: %#v", shippedBlockedPatterns, want)
	}
}
