// Package deployment derives the runtime object identity shared by onboarding,
// the Darwin broker, doctor, and the resident. Names are a function of the
// canonical MC_HOME only, so two deployments cannot address each other's
// helper or spine volume and symlink aliases cannot split one deployment.
package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

const nameDomain = "mc-deployment-v1\x00"

type Names struct {
	Suffix        string
	Volume        string
	RuntimeVolume string
	Helper        string
}

// RuntimeNames derives the 12-lowercase-hex helper name and the
// per-deployment spine volume from an already-canonical absolute MC_HOME.
func RuntimeNames(canonicalHome string) (Names, error) {
	if canonicalHome == "" || !filepath.IsAbs(canonicalHome) || filepath.Clean(canonicalHome) != canonicalHome {
		return Names{}, fmt.Errorf("canonical MC_HOME must be a clean absolute path")
	}
	sum := sha256.Sum256([]byte(nameDomain + canonicalHome))
	suffix := hex.EncodeToString(sum[:6])
	return Names{
		Suffix:        suffix,
		Volume:        "mc-spine-" + suffix,
		RuntimeVolume: "mc-runtime-" + suffix,
		Helper:        "mc-helper-" + suffix,
	}, nil
}

// SmokeWorkspaceBase is the deployment-keyed smoke Project namespace.
// It is deliberately a sibling of MC_HOME: the ordinary mount jurisdiction
// rejects every MC_HOME descendant, including a correctly isolated smoke
// workspace. The path remains deterministic so the private onboarding frame
// can prove the host-derived root rather than accepting an arbitrary source.
func SmokeWorkspaceBase(canonicalHome string) (string, error) {
	if _, err := RuntimeNames(canonicalHome); err != nil {
		return "", err
	}
	parent := filepath.Dir(canonicalHome)
	if parent == canonicalHome {
		return "", fmt.Errorf("MC_HOME root has no safe smoke-workspace sibling")
	}
	return filepath.Join(parent, filepath.Base(canonicalHome)+"-smoke-workspaces"), nil
}

// CanonicalHome resolves symlinks through the nearest existing ancestor while
// preserving a not-yet-created suffix. This is the same physical identity the
// git-working-tree fence must validate before RuntimeNames is called.
func CanonicalHome(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("MC_HOME must be an absolute path")
	}
	path = filepath.Clean(path)
	cur := path
	suffix := []string{}
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", fmt.Errorf("resolve MC_HOME %q: %w", path, err)
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect MC_HOME %q: %w", path, err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("resolve MC_HOME %q: no existing ancestor", path)
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}
