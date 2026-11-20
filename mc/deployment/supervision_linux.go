package deployment

import "fmt"

// The systemd supervision bundle is implemented in A03 stage ②; until then
// every Linux path through the seam refuses loudly rather than silently
// writing launchd artifacts.

var supervisionFiles = []string{
	"units/agentsview.service",
	"units/dashboard.service",
	"units/resident.service",
	"agentsview.toml",
	"dashboard.json",
	"resident.json",
}

func precheckSupervisionHome(canonicalHome string) error { return nil }

func renderUnitFiles(_ unitRenderInput) (map[string][]byte, error) {
	return nil, fmt.Errorf("systemd supervision units are not implemented yet")
}

func platformAgentsViewFiles(_ string, _ Names, _, _ string) (map[string][]byte, error) {
	return nil, fmt.Errorf("systemd supervision units are not implemented yet")
}
