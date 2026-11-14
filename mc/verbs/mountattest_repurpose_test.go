package verbs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"mc/boundary"
	"mc/deployment"
)

func TestResolveGatewaySecretRootsProtectsAbsentAndPresentCredentialDirectories(t *testing.T) {
	root := t.TempDir()
	home := maMkdir(t, root, "deployment")
	operator := maMkdir(t, root, "operator")
	temporary := maMkdir(t, root, "temporary")
	t.Setenv("TMPDIR", temporary)
	native := deployment.NativeCredentialSourceRoot()
	legacy := filepath.Join(home, "refresh-grants")
	for _, present := range []bool{false, true} {
		if present {
			for _, path := range []string{native, legacy} {
				if err := os.MkdirAll(filepath.Join(path, "child"), 0700); err != nil {
					t.Fatal(err)
				}
			}
		}
		roots, err := resolveGatewaySecretRoots(home)
		if err != nil || len(roots) != 2 {
			t.Fatalf("The credential roots are incomplete: %+v %v", roots, err)
		}
		jurisdiction, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{Home: operator, GatewaySecrets: roots}, os.Getuid())
		if err != nil {
			t.Fatal(err)
		}
		paths := []string{home, temporary}
		if present {
			paths = append(paths, native, filepath.Join(native, "child"), legacy, filepath.Join(legacy, "child"))
		}
		for _, path := range paths {
			source, err := boundary.ResolveSource(path)
			if err != nil {
				t.Fatal(err)
			}
			var rejected *boundary.MountError
			if err := jurisdiction.Rejects(source); !errors.As(err, &rejected) || rejected.Code != boundary.CodeDeniedRoot {
				t.Fatalf("The credential directory accepted a mount: %s: %v", path, err)
			}
		}
	}
}
