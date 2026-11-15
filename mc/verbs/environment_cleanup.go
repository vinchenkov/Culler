package verbs

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
)

var projectCacheName = regexp.MustCompile(`^mc-cache-([a-f0-9]{16})-[a-f0-9]{24}$`)
var projectImageName = regexp.MustCompile(`^culler-project-([a-f0-9]{16})-[a-z0-9_-]+:[a-z0-9_.-]+$`)

func environmentCleanupNamespaces(homes []string) map[string]bool {
	namespaces := map[string]bool{}
	for _, home := range homes {
		digest := sha256.Sum256([]byte(home))
		namespaces[fmt.Sprintf("%x", digest[:8])] = true
	}
	return namespaces
}

// ProjectCacheVolumes selects caches scoped to the specified deployment homes.
func ProjectCacheVolumes(names, homes []string) []string {
	namespaces := environmentCleanupNamespaces(homes)
	var selected []string
	for _, name := range names {
		parts := projectCacheName.FindStringSubmatch(name)
		if len(parts) == 2 && namespaces[parts[1]] {
			selected = append(selected, name)
		}
	}
	return selected
}

// ProjectImageReference checks the scope before the caller checks Docker labels.
func ProjectImageReference(reference string, homes []string) bool {
	parts := projectImageName.FindStringSubmatch(reference)
	return len(parts) == 2 && environmentCleanupNamespaces(homes)[parts[1]] && !strings.HasSuffix(reference, ":latest")
}
