package deployment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Darwin-only supervision gates: the macOS TCC boundary has no Linux analog,
// so its tests live behind the implicit darwin build constraint of the
// filename, exactly like the renderer they cover.

func TestLaunchdTCCGateRejectsProtectedUserFolders(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "operator")
	for _, name := range []string{"Documents", "Desktop", "Downloads"} {
		root := filepath.Join(userHome, name)
		for _, candidate := range []string{root, filepath.Join(root, "culler")} {
			err := validateLaunchdTCCPath(candidate, []string{
				filepath.Join(userHome, "Documents"),
				filepath.Join(userHome, "Desktop"),
				filepath.Join(userHome, "Downloads"),
			})
			if err == nil || !strings.Contains(err.Error(), "TCC-protected") {
				t.Fatalf("%s candidate %q error = %v", name, candidate, err)
			}
		}
	}
	safe := filepath.Join(userHome, "Library", "Application Support", "culler")
	if err := validateLaunchdTCCPath(safe, []string{
		filepath.Join(userHome, "Documents"),
		filepath.Join(userHome, "Desktop"),
		filepath.Join(userHome, "Downloads"),
	}); err != nil {
		t.Fatalf("safe launchd home refused: %v", err)
	}
}

func TestPrepareSupervisionRefusesTCCProtectedHomeBeforePublication(t *testing.T) {
	operatorHome := t.TempDir()
	t.Setenv("HOME", operatorHome)
	protected := filepath.Join(operatorHome, "Documents")
	if err := os.Mkdir(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	home, spec := supervisionFixture(t)
	target := filepath.Join(protected, "mc-home")
	if err := os.Rename(home, target); err != nil {
		t.Fatal(err)
	}
	names, err := RuntimeNames(target)
	if err != nil {
		t.Fatal(err)
	}
	spec.SpineVolume = names.Volume
	if _, _, err := PrepareSupervision(target, spec, func(string) (bool, error) {
		return false, nil
	}); err == nil || !strings.Contains(err.Error(), "TCC-protected") {
		t.Fatalf("protected supervision error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(target, "supervision")); !os.IsNotExist(err) {
		t.Fatalf("TCC refusal published supervision: %v", err)
	}
}
