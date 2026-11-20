package deployment

// The systemd supervision layout renders through the shared systemd renderers
// (supervision_systemd.go); installation and control operations arrive with
// stage ②b (systemd controller) and refuse loudly until then.

var supervisionFiles = []string{
	"units/agentsview.service",
	"units/dashboard.service",
	"units/resident.service",
	"agentsview.toml",
	"dashboard.json",
	"resident.json",
}

func precheckSupervisionHome(canonicalHome string) error { return nil }

func renderUnitFiles(in unitRenderInput) (map[string][]byte, error) {
	return systemdBundledUnits(in)
}

func platformAgentsViewFiles(canonicalHome string, names Names, bind, token string) (map[string][]byte, error) {
	return agentsViewSystemdBundle(canonicalHome, names, bind, token)
}
