package distribution

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	operatorconfig "mc/config"
)

type UpdatePolicy struct {
	MinimumVersion string              `json:"minimum_version"`
	Formats        map[string]string   `json:"formats"`
	Accepted       map[string][]string `json:"accepted"`
}

//go:embed update_policy.json
var updatePolicyDefaults []byte

func testReleasesEnabled() bool {
	var policy struct {
		TestReleases bool `json:"test_releases"`
	}
	return json.Unmarshal(updatePolicyDefaults, &policy) == nil && policy.TestReleases
}

var updateFormatNames = []string{"spine", "configuration", "credentials", "claude", "codex", "opencode", "opencode-websearch", "openwiki", "agentsview"}

func (p *UpdatePolicy) validate(version, schema string) error {
	if p == nil || !ValidVersion(version) || !ValidVersion(p.MinimumVersion) {
		return errors.New("The public update policy has an invalid version.")
	}
	if CompareVersions(version, p.MinimumVersion) < 0 {
		return errors.New("The minimum update version exceeds this release.")
	}
	if p.Formats["spine"] != schema || len(p.Formats) != len(updateFormatNames) || len(p.Accepted) != len(updateFormatNames) {
		return errors.New("The release has incomplete persistent format identities.")
	}
	for _, name := range updateFormatNames {
		format := p.Formats[name]
		sources := p.Accepted[name]
		if format == "" || len(format) > 256 || !slices.Contains(sources, format) {
			return fmt.Errorf("The release lacks its %s format or accepted source.", name)
		}
		seen := map[string]bool{}
		for _, source := range sources {
			if source == "" || len(source) > 256 || seen[source] {
				return fmt.Errorf("The %s source formats are invalid.", name)
			}
			seen[source] = true
		}
	}
	return nil
}

func ValidateUpdateSource(candidate, previous Record) error {
	if err := candidate.Update.validate(candidate.Version, candidate.Schema); err != nil {
		return err
	}
	if candidate.TestRelease && testReleasesEnabled() {
		return nil
	}
	if previous.Update == nil || previous.Version == "" {
		return errors.New("This installation predates the public compatibility baseline. Its formats require an explicit import before a public update.")
	}
	if err := previous.Update.validate(previous.Version, previous.Schema); err != nil {
		return err
	}
	if CompareVersions(previous.Version, candidate.Update.MinimumVersion) < 0 {
		return fmt.Errorf("This update requires Culler %s or later. Install the required intermediate release first.", candidate.Update.MinimumVersion)
	}
	if previous.Sequence > candidate.Sequence || CompareVersions(previous.Version, candidate.Version) > 0 {
		return errors.New("The installed public release is newer. Ordinary downgrades are unsupported.")
	}
	if previous.Commit == candidate.Commit {
		return nil
	}
	if CompareVersions(candidate.Version, previous.Version) <= 0 {
		return errors.New("A different public release requires a newer public version.")
	}
	for _, name := range updateFormatNames {
		source := previous.Update.Formats[name]
		if !slices.Contains(candidate.Update.Accepted[name], source) {
			return fmt.Errorf("The candidate cannot use the saved %s format. Keep the installed release until a tested update path is available.", name)
		}
	}
	// The baseline has no spine or configuration converter.
	for _, name := range []string{"spine", "configuration", "credentials"} {
		if candidate.Update.Formats[name] != previous.Update.Formats[name] {
			return fmt.Errorf("The %s format needs a tested migration before this update can proceed.", name)
		}
	}
	return nil
}

func BuildUpdatePolicy(root, schema string) (string, *UpdatePolicy, error) {
	versionBytes, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "", nil, err
	}
	version := strings.TrimSuffix(string(versionBytes), "\n")
	if !ValidVersion(version) {
		return "", nil, errors.New("The VERSION file requires one public version.")
	}
	var defaults struct {
		MinimumVersion string              `json:"minimum_version"`
		Accepted       map[string][]string `json:"accepted"`
	}
	if err := json.Unmarshal(updatePolicyDefaults, &defaults); err != nil {
		return "", nil, err
	}
	read := func(name string) (map[string]any, error) {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		var value map[string]any
		err = json.Unmarshal(body, &value)
		return value, err
	}
	harness, err := read("runner/agent-runner/package.json")
	if err != nil {
		return "", nil, err
	}
	deps, ok := harness["dependencies"].(map[string]any)
	if !ok {
		return "", nil, errors.New("The harness dependency pins are absent.")
	}
	optional, err := read("release/opencode/pins.json")
	if err != nil {
		return "", nil, err
	}
	wiki, err := read("runner/openwiki/package.json")
	if err != nil {
		return "", nil, err
	}
	wikiDeps, ok := wiki["dependencies"].(map[string]any)
	if !ok {
		return "", nil, errors.New("The OpenWiki dependency pin is absent.")
	}
	viewer, err := read("mc/deployment/agentsview.json")
	if err != nil {
		return "", nil, err
	}
	text := func(value any) string { s, _ := value.(string); return s }
	formats := map[string]string{"spine": schema, "configuration": strconv.Itoa(operatorconfig.CurrentVersion), "credentials": "keychain-reference-1", "claude": text(deps["@anthropic-ai/claude-agent-sdk"]), "codex": text(deps["@openai/codex"]), "opencode": text(optional["opencode"]), "opencode-websearch": text(optional["websearch"]), "openwiki": text(wikiDeps["openwiki"]), "agentsview": text(viewer["version"])}
	wikiPatch := sha256.New()
	for _, path := range []string{"runner/openwiki/patch-upstream.mjs", "runner/openwiki/project-documents.mjs"} {
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return "", nil, err
		}
		wikiPatch.Write([]byte(path + "\x00"))
		wikiPatch.Write(body)
	}
	formats["openwiki"] += "+culler." + hex.EncodeToString(wikiPatch.Sum(nil))
	policy := &UpdatePolicy{MinimumVersion: defaults.MinimumVersion, Formats: formats, Accepted: map[string][]string{}}
	for name, value := range formats {
		policy.Accepted[name] = append([]string{value}, defaults.Accepted[name]...)
	}
	if err := policy.validate(version, schema); err != nil {
		return "", nil, err
	}
	return version, policy, nil
}

type DependencyPin struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func DependencyInventory(root string) ([]DependencyPin, error) {
	paths := []string{
		"VERSION", "mc/distribution/update_policy.json", ".mise.toml", "mc/go.mod", "mc/go.sum", "mc/deployment/hostbun.json", "mc/deployment/agentsview.json", "mc/opencodecatalog/catalog.json", "mc/opencodecatalog/facts/package.json", "mc/opencodecatalog/facts/server.mjs",
		"dashboard/package.json", "dashboard/bun.lock", "dashboard/pierre/package.json", "dashboard/pierre/bun.lock", "resident/package.json",
		"runner/collie-runner/package.json", "runner/agent-runner/package.json", "runner/agent-runner/bun.lock",
		"runner/opencode/package.json", "runner/opencode/package-lock.json", "runner/opencode/control/package.json",
		"release/opencode/pins.json", "release/opencode/build.sh",
		"runner/openwiki/package.json", "runner/openwiki/package-lock.json", "runner/openwiki/patch-upstream.mjs", "runner/openwiki/project-documents.mjs",
		"runner/image/project-tools/package.json", "runner/image/project-tools/package-lock.json",
		"runner/image/runtime-tools/package.json", "runner/image/runtime-tools/package-lock.json",
		"runner/image/Dockerfile", "runner/image/runtime.Dockerfile", "runner/image/project-manifest.sh", "runner/image/runtime-manifest.sh",
	}
	out := make([]DependencyPin, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		out = append(out, DependencyPin{Path: path, SHA256: hex.EncodeToString(sum[:])})
	}
	return out, nil
}
