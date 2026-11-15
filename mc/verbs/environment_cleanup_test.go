package verbs

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"
)

func TestEnvironmentCleanupUsesExactDeploymentScope(t *testing.T) {
	home := "/private/tmp/culler-fixture/home"
	digest := sha256.Sum256([]byte(home))
	namespace := fmt.Sprintf("%x", digest[:8])
	cache := "mc-cache-" + namespace + "-0123456789abcdef01234567"
	names := []string{cache, cache + "extra", "mc-cache-0000000000000000-0123456789abcdef01234567", "mc-cache-" + namespace + "-project"}
	if got := ProjectCacheVolumes(names, []string{home}); !reflect.DeepEqual(got, []string{cache}) {
		t.Fatalf("selected caches: %v", got)
	}
	for reference, accepted := range map[string]bool{
		"culler-project-" + namespace + "-project:operation-1": true,
		"culler-project-" + namespace + "-project:latest":      false,
		"culler-project-0000000000000000-project:operation-1":  false,
		"culler-project-base:0123456789abcdef":                 false,
		"mc-project:latest":                                    false,
	} {
		if ProjectImageReference(reference, []string{home}) != accepted {
			t.Fatalf("unexpected image ownership for %s", reference)
		}
	}
}
