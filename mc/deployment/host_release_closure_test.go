package deployment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// hostReleaseFiles is hand-maintained, and on 2026-07-31 it had silently
// drifted seven modules behind the resident it ships: the installed release
// passed ValidateHostRelease and then died at startup with "Cannot find module
// './homie-runtime'". The manifest agreed with itself and with nothing else.
//
// This derives the real transitive import graph from the two host entry points
// and requires the manifest to contain every file in it. A module added to the
// resident without being shipped now fails here instead of ninety seconds into
// an operator-present launchd activation.
var hostReleaseImport = regexp.MustCompile(`(?:from|import)\s*["'](\.[^"']+)["']`)
var hostReleaseReadAsset = regexp.MustCompile(`readFileSync\(join\(import\.meta\.dir,\s*["'](\.[^"']+)["']\)\)`)

func hostReleaseEntryClosure(t *testing.T, repoRoot string) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	// app.js is the browser entry point: the dashboard server serves it and it
	// pulls the rest of the UI module graph with ES imports. On 2026-08-18 the
	// manifest had drifted again — nine UI modules behind the shell it shipped —
	// because the browser graph was hand-listed here instead of derived; the
	// deployed dashboard rendered its tabs and then 404'd every page module.
	stack := []string{"resident/src/main.ts", "resident/src/provider-foreground.ts", "dashboard/src/main.ts", "dashboard/src/ui/app.js"}
	for len(stack) > 0 {
		rel := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[rel] {
			continue
		}
		seen[rel] = true
		body, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("host entry graph reached an unreadable file %s: %v", rel, err)
		}
		for _, match := range hostReleaseReadAsset.FindAllStringSubmatch(string(body), -1) {
			asset := filepath.ToSlash(filepath.Join(filepath.Dir(rel), match[1]))
			if info, err := os.Stat(filepath.Join(repoRoot, asset)); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("host entry reads an absent asset %s", asset)
			}
			seen[asset] = true
		}
		for _, match := range hostReleaseImport.FindAllStringSubmatch(string(body), -1) {
			base := filepath.Join(filepath.Dir(rel), match[1])
			resolved := ""
			// Bun resolves an extensionless specifier; the manifest cannot, so
			// the suffix has to be recovered here rather than assumed.
			for _, candidate := range []string{base, base + ".ts", base + ".js", filepath.Join(base, "index.ts")} {
				if info, err := os.Stat(filepath.Join(repoRoot, candidate)); err == nil && info.Mode().IsRegular() {
					resolved = candidate
					break
				}
			}
			if resolved == "" {
				t.Fatalf("host entry graph has an unresolvable import %q in %s", match[1], rel)
			}
			stack = append(stack, filepath.ToSlash(resolved))
		}
	}
	return seen
}

func TestHostReleaseManifestCoversTheEntryImportGraph(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	shipped := map[string]bool{}
	for _, rel := range hostReleaseFiles {
		shipped[rel] = true
	}
	missing := []string{}
	for rel := range hostReleaseEntryClosure(t, repoRoot) {
		if !shipped[rel] {
			missing = append(missing, rel)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("the host release ships a resident that imports modules it omits; add to hostReleaseFiles: %s",
			strings.Join(missing, ", "))
	}
}

// A non-JS asset — a stylesheet, an HTML file, an image — is SERVED rather
// than imported, so the import closure above cannot see it and the manifest
// must name it by hand. This derives the served set from the dashboard
// server's own static registry, which makes the pair self-maintaining in both
// directions: an asset added to the registry but not the manifest 404s only in
// production (which happened on 2026-08-18), and an asset dropped from the
// registry but left in the manifest is dead weight the operator still trusts.
var servedStaticAsset = regexp.MustCompile(`path:\s*"(ui/[^"$]+)"`)

func dashboardServedAssets(t *testing.T, repoRoot string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot, "dashboard/src/server.ts"))
	if err != nil {
		t.Fatalf("the dashboard server's static registry is unreadable: %v", err)
	}
	served := map[string]bool{}
	for _, match := range servedStaticAsset.FindAllStringSubmatch(string(body), -1) {
		served["dashboard/src/"+match[1]] = true
	}
	if len(served) == 0 {
		t.Fatal("no served static asset found in dashboard/src/server.ts — the registry shape changed")
	}
	return served
}

// The wiki server serves the OpenWiki library copies that its provenance
// record names. The license file of each library ships with the copy.
func wikiServerAssets(t *testing.T, repoRoot string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot, "dashboard/src/openwiki/provenance.json"))
	if err != nil {
		t.Fatalf("the OpenWiki provenance record is unreadable: %v", err)
	}
	var record struct {
		Libraries []struct {
			File        string `json:"file"`
			LicenseFile string `json:"license_file"`
		} `json:"libraries"`
	}
	if err := json.Unmarshal(body, &record); err != nil || len(record.Libraries) == 0 {
		t.Fatalf("the OpenWiki provenance record lists no library: %v", err)
	}
	assets := map[string]bool{}
	for _, library := range record.Libraries {
		if library.File == "" || library.LicenseFile == "" {
			t.Fatalf("the OpenWiki provenance record has a library without a file or a license file: %+v", library)
		}
		assets["dashboard/src/openwiki/"+library.File] = true
		assets["dashboard/src/openwiki/"+library.LicenseFile] = true
	}
	return assets
}

// The dashboard serves the local Pierre modules and the license notices that
// their provenance record names.
func pierreAssets(t *testing.T, repoRoot string) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot, "dashboard/src/ui/pierre/provenance.json"))
	if err != nil {
		t.Fatalf("the Pierre provenance record is unreadable: %v", err)
	}
	var record struct {
		Files []struct {
			File string `json:"file"`
		} `json:"files"`
		Packages []struct {
			LicenseFile string `json:"license_file"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(body, &record); err != nil || len(record.Files) == 0 || len(record.Packages) == 0 {
		t.Fatalf("the Pierre provenance record lists no module or no package: %v", err)
	}
	assets := map[string]bool{}
	for _, file := range record.Files {
		assets["dashboard/src/ui/pierre/"+file.File] = true
	}
	for _, pkg := range record.Packages {
		if pkg.LicenseFile == "" {
			t.Fatal("the Pierre provenance record has a package without a license notice")
		}
		assets["dashboard/src/ui/pierre/"+pkg.LicenseFile] = true
	}
	return assets
}

// The converse: a manifest entry that no longer exists would be copied as a
// missing file, and one that nothing imports or serves is dead weight the
// operator still has to trust.
func TestHostReleaseManifestShipsNothingUnreachable(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	served := dashboardServedAssets(t, repoRoot)
	for rel := range wikiServerAssets(t, repoRoot) {
		served[rel] = true
	}
	for rel := range pierreAssets(t, repoRoot) {
		served[rel] = true
	}
	reachable := hostReleaseEntryClosure(t, repoRoot)
	for _, rel := range hostReleaseFiles {
		if info, err := os.Stat(filepath.Join(repoRoot, rel)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("host release manifest names a file the repository does not have: %s", rel)
		}
		if !reachable[rel] && !served[rel] && !strings.HasPrefix(rel, "third_party/") {
			t.Fatalf("host release ships %s, which no entry point imports and no server serves", rel)
		}
	}
	shipped := map[string]bool{}
	for _, rel := range hostReleaseFiles {
		shipped[rel] = true
	}
	for rel := range served {
		if !shipped[rel] {
			t.Fatalf("the dashboard or the wiki server serves %s but the host release does not ship it: it 404s in production only", rel)
		}
	}
}
