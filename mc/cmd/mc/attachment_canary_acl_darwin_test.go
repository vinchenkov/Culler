//go:build darwin && !test_fake_routing

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachmentCanaryScratchRefusesGrantingNativeACL(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	health := filepath.Join(home, "health")
	if err := os.Mkdir(health, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := trustAttachmentCanaryDirs(home, health); err != nil {
		t.Fatalf("clean canary ancestry = %v", err)
	}
	if out, err := exec.Command(
		"chmod", "+a", "everyone allow list,search", health,
	).CombinedOutput(); err != nil {
		t.Fatalf("add granting ACL: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("chmod", "-N", health).Run()
	})
	err := trustAttachmentCanaryDirs(home, health)
	if err == nil || !strings.Contains(err.Error(), "ACL") {
		t.Fatalf("granting canary scratch ACL = %v, want refusal", err)
	}
}
