package deployment

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

var hostReleaseFiles = []string{
	"runner/shared/provider-token.ts",
	"runner/agent-runner/adapters/provider-access.ts",
	"mc/substrate/schema.sql",
	"mc/substrate/delivery.sql",
	"mc/substrate/environments.sql",
	"dashboard/src/api.ts",
	"dashboard/src/agentsview-pages.ts",
	"dashboard/src/agentsview-proxy.ts",
	"dashboard/src/hostfile.ts",
	"dashboard/src/main.ts",
	"dashboard/src/mc.ts",
	"dashboard/src/resident-nudge.ts",
	"dashboard/src/server.ts",
	"dashboard/src/wiki-server.ts",
	"dashboard/src/openwiki/force-graph.min.js",
	"dashboard/src/openwiki/marked.min.js",
	"dashboard/src/openwiki/purify.min.js",
	"dashboard/src/openwiki/mermaid.min.js",
	"dashboard/src/openwiki/LICENSE-force-graph",
	"dashboard/src/openwiki/LICENSE-marked",
	"dashboard/src/openwiki/LICENSE-dompurify",
	"dashboard/src/openwiki/LICENSE-mermaid",
	"dashboard/src/openwiki/provenance.json",
	"dashboard/src/ui/activity.js",
	"dashboard/src/ui/app.js",
	"dashboard/src/ui/board.js",
	"dashboard/src/ui/boardwrites.js",
	"dashboard/src/ui/changes.js",
	"dashboard/src/ui/changeview.js",
	"dashboard/src/ui/configs.js",
	"dashboard/src/ui/discord.js",
	"dashboard/src/ui/environment.js",
	"dashboard/src/ui/setup-journal.js",
	"dashboard/src/ui/fileview.js",
	"dashboard/src/ui/dom.js",
	"dashboard/src/ui/components.js",
	"dashboard/src/ui/requests.js",
	"dashboard/src/ui/errors.js",
	"dashboard/src/ui/flowmap.js",
	"dashboard/src/ui/agentprofiles.js",
	"dashboard/src/ui/projects.js",
	"dashboard/src/ui/projectdelete.js",
	"dashboard/src/ui/customization.js",
	"dashboard/src/ui/vision.js",
	"dashboard/src/ui/conversation.js",
	"dashboard/src/ui/commandbar.js",
	"dashboard/src/ui/diagnosticchat.js",
	"dashboard/src/ui/packetchat.js",
	"dashboard/src/ui/format.js",
	"dashboard/src/ui/homiefmt.js",
	"dashboard/src/ui/index.html",
	"dashboard/src/ui/logs.js",
	"dashboard/src/ui/markdown.js",
	"dashboard/src/ui/paths.js",
	"dashboard/src/ui/pipeline.js",
	"dashboard/src/ui/systemhealth.js",
	"dashboard/src/ui/review.js",
	"dashboard/src/ui/evaluation.js",
	"dashboard/src/ui/router.js",
	"dashboard/src/ui/stages.js",
	"dashboard/src/ui/style.css",
	"dashboard/src/ui/landscape.png",
	"dashboard/src/ui/landscape-dark.png",
	"dashboard/src/ui/taskdialog.js",
	"dashboard/src/ui/theme.js",
	"dashboard/src/ui/viewers.js",
	"dashboard/src/ui/topology.js",
	"dashboard/src/ui/fonts/fraunces.woff2",
	"dashboard/src/ui/fonts/figtree.woff2",
	"dashboard/src/ui/fonts/LICENSE-fraunces.txt",
	"dashboard/src/ui/fonts/LICENSE-figtree.txt",
	// The local Pierre modules and their license notices. dashboard/pierre/build.ts
	// writes each line from here to the end marker.
	"dashboard/src/ui/pierre/LICENSE-ccount-2.0.1.txt",
	"dashboard/src/ui/pierre/LICENSE-character-entities-html4-2.1.0.txt",
	"dashboard/src/ui/pierre/LICENSE-character-entities-legacy-3.0.0.txt",
	"dashboard/src/ui/pierre/LICENSE-comma-separated-tokens-2.0.3.txt",
	"dashboard/src/ui/pierre/LICENSE-diff-9.0.0.txt",
	"dashboard/src/ui/pierre/LICENSE-grammars.txt",
	"dashboard/src/ui/pierre/LICENSE-hast-util-to-html-9.0.5.txt",
	"dashboard/src/ui/pierre/LICENSE-hast-util-whitespace-3.0.0.txt",
	"dashboard/src/ui/pierre/LICENSE-html-void-elements-3.0.0.txt",
	"dashboard/src/ui/pierre/LICENSE-oniguruma-parser-0.12.2.txt",
	"dashboard/src/ui/pierre/LICENSE-oniguruma-to-es-4.3.6.txt",
	"dashboard/src/ui/pierre/LICENSE-pierre-diffs-1.5.1.txt",
	"dashboard/src/ui/pierre/LICENSE-pierre-theme-2.0.0.txt",
	"dashboard/src/ui/pierre/LICENSE-pierre-theming-1.0.1.txt",
	"dashboard/src/ui/pierre/LICENSE-pierre-trees-1.0.0-beta.6.txt",
	"dashboard/src/ui/pierre/LICENSE-preact-11.0.0-beta.0.txt",
	"dashboard/src/ui/pierre/LICENSE-property-information-7.2.0.txt",
	"dashboard/src/ui/pierre/LICENSE-regex-6.1.0.txt",
	"dashboard/src/ui/pierre/LICENSE-regex-recursion-6.0.2.txt",
	"dashboard/src/ui/pierre/LICENSE-regex-utilities-2.3.0.txt",
	"dashboard/src/ui/pierre/LICENSE-shiki-4.4.3.txt",
	"dashboard/src/ui/pierre/LICENSE-shikijs-core-4.4.3.txt",
	"dashboard/src/ui/pierre/LICENSE-shikijs-engine-javascript-4.4.3.txt",
	"dashboard/src/ui/pierre/LICENSE-shikijs-engine-oniguruma-4.4.3.txt",
	"dashboard/src/ui/pierre/LICENSE-shikijs-langs-4.4.3.txt",
	"dashboard/src/ui/pierre/LICENSE-shikijs-primitive-4.4.3.txt",
	"dashboard/src/ui/pierre/LICENSE-shikijs-transformers-4.4.3.txt",
	"dashboard/src/ui/pierre/LICENSE-shikijs-types-4.4.3.txt",
	"dashboard/src/ui/pierre/LICENSE-shikijs-vscode-textmate-10.0.2.txt",
	"dashboard/src/ui/pierre/LICENSE-space-separated-tokens-2.0.2.txt",
	"dashboard/src/ui/pierre/LICENSE-stringify-entities-4.0.4.txt",
	"dashboard/src/ui/pierre/LICENSE-zwitch-2.0.4.txt",
	"dashboard/src/ui/pierre/css-vwpt56ag.js",
	"dashboard/src/ui/pierre/diff-zb6nskg5.js",
	"dashboard/src/ui/pierre/docker-8csjjc5w.js",
	"dashboard/src/ui/pierre/dotenv-mq103d5z.js",
	"dashboard/src/ui/pierre/entry-1jwz692w.js",
	"dashboard/src/ui/pierre/entry-5zran53s.js",
	"dashboard/src/ui/pierre/entry-8y89r2n8.js",
	"dashboard/src/ui/pierre/go-6hjg9608.js",
	"dashboard/src/ui/pierre/html-51j4t2s6.js",
	"dashboard/src/ui/pierre/ini-6fshdk60.js",
	"dashboard/src/ui/pierre/javascript-m3e68tk1.js",
	"dashboard/src/ui/pierre/json-k67qz80s.js",
	"dashboard/src/ui/pierre/jsonc-j0bqfs8g.js",
	"dashboard/src/ui/pierre/jsx-nvxk2d20.js",
	"dashboard/src/ui/pierre/make-p33a40c0.js",
	"dashboard/src/ui/pierre/markdown-b1xasw8y.js",
	"dashboard/src/ui/pierre/pierre-dark-ha7xxyp4.js",
	"dashboard/src/ui/pierre/pierre-light-dn9s0jyb.js",
	"dashboard/src/ui/pierre/pierre.js",
	"dashboard/src/ui/pierre/provenance.json",
	"dashboard/src/ui/pierre/python-g092hyha.js",
	"dashboard/src/ui/pierre/rust-fhxepxp6.js",
	"dashboard/src/ui/pierre/shellscript-zffbhb7y.js",
	"dashboard/src/ui/pierre/shiki-wasm-snbws871.js",
	"dashboard/src/ui/pierre/sql-95yzedpw.js",
	"dashboard/src/ui/pierre/toml-xm9210fk.js",
	"dashboard/src/ui/pierre/tsx-j7xsjw90.js",
	"dashboard/src/ui/pierre/typescript-hxa5ykzr.js",
	"dashboard/src/ui/pierre/xml-72aznfw3.js",
	"dashboard/src/ui/pierre/yaml-4wesyp00.js",
	// The end of the local Pierre modules.
	"resident/src/agentsview.ts",
	"resident/src/attachment-health-guard.ts",
	"resident/src/attachment-health.ts",
	"resident/src/attachment-ingress.ts",
	"resident/src/credential-projector.ts",
	"resident/src/provider-proxy.ts",
	"resident/src/provider-credentials.ts",
	"resident/src/provider-lifetime.ts",
	"resident/src/provider-protocols.json",
	"resident/src/connection-owner.ts",
	"resident/src/agent-profile-runtime.ts",
	"resident/src/discord.ts",
	"resident/src/discord-config.ts",
	"resident/src/effects.ts",
	"resident/src/environment-lane.ts",
	"resident/src/environment-health.ts",
	"resident/src/repository.ts",
	"resident/src/project-containers.ts",
	"resident/src/repository-lane.ts",
	"resident/src/git-identity.ts",
	"resident/src/homie-envelope.ts",
	"resident/src/homie-runtime.ts",
	"resident/src/install-maintenance.ts",
	"resident/src/host-zone.ts",
	"resident/src/main.ts",
	"resident/src/nudge-socket.ts",
	"resident/src/pipeline-liveness.ts",
	"resident/src/provider-acceptance.ts",
	"resident/src/provider-foreground.ts",
	"resident/src/resident-control.ts",
	"resident/src/run-log-prune.ts",
	"resident/src/vision-save.ts",
	"resident/src/skill-store.ts",
	"resident/src/tick-loop.ts",
	"resident/src/trace-copy.ts",
	"resident/src/trace-lane.ts",
	"resident/src/setup-traces.ts",
	"resident/src/types.ts",
	"resident/src/wiki-lane.ts",
	// The shared structured logger. It lives under runner/ because runner/ is the
	// only subtree BOTH release manifests can address: this list is repo-root
	// relative, runnerReleaseFiles is runner/ relative and cannot express "..".
	// The host tier ships it because the resident and the dashboard import it.
	"runner/shared/log-sink.ts",
	"runner/shared/log.ts",
	"runner/shared/network-error.ts",
	"runner/shared/setup-error.ts",
	"runner/agent-runner/diagnostics.ts",
	"runner/shared/project-environment.ts",
	"runner/shared/project-cache.ts",
	// The license of the AgentsView executable that each deployment installs.
	"third_party/agentsview/LICENSE",
}

// HostReleaseFiles is the exact shipped set. It is exported so fixtures build
// their fake repositories from the manifest itself rather than from a second
// hand-copied list that can disagree with it.
func HostReleaseFiles() []string {
	return append([]string(nil), hostReleaseFiles...)
}

var hostReleaseDirs = []string{
	".",
	"mc",
	"mc/substrate",
	"dashboard",
	"dashboard/src",
	"dashboard/src/openwiki",
	"dashboard/src/ui",
	"dashboard/src/ui/fonts",
	"dashboard/src/ui/pierre",
	"resident",
	"resident/src",
	"runner",
	"runner/shared",
	"runner/agent-runner",
	"runner/agent-runner/adapters",
	"third_party",
	"third_party/agentsview",
}

// ValidateHostRelease closes the two native host programs over their exact
// transitive source/UI graph. Tests, lockfiles, and package metadata are not
// executable deployment inputs.
func ValidateHostRelease(root string) error {
	expectedDirs := make(map[string]bool, len(hostReleaseDirs))
	for _, rel := range hostReleaseDirs {
		expectedDirs[rel] = true
	}
	expectedFiles := make(map[string]bool, len(hostReleaseFiles))
	for _, rel := range hostReleaseFiles {
		expectedFiles[rel] = true
	}
	seenFiles := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		uid, links, ok := statOwner(info)
		if !ok || int(uid) != os.Getuid() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("installed host entry %q lost operator ownership or became a symlink", rel)
		}
		if entry.IsDir() {
			if !expectedDirs[rel] || info.Mode().Perm() != 0o700 {
				return fmt.Errorf("installed host release contains unexpected or non-owner-only directory %q", rel)
			}
			return nil
		}
		if !expectedFiles[rel] || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || links != 1 ||
			info.Size() <= 0 || info.Size() > maxRunnerReleaseFileBytes {
			return fmt.Errorf("installed host release contains unexpected or unsafe file %q", rel)
		}
		seenFiles[rel] = true
		return nil
	})
	if err != nil {
		return err
	}
	for _, rel := range hostReleaseFiles {
		if !seenFiles[rel] {
			return fmt.Errorf("installed host release is missing %q", rel)
		}
	}
	return nil
}

func hostReleasesEqual(left, right string) (bool, error) {
	for _, rel := range hostReleaseFiles {
		leftBody, err := os.ReadFile(filepath.Join(left, filepath.FromSlash(rel)))
		if err != nil {
			return false, err
		}
		rightBody, err := os.ReadFile(filepath.Join(right, filepath.FromSlash(rel)))
		if err != nil {
			return false, err
		}
		if !bytes.Equal(leftBody, rightBody) {
			return false, nil
		}
	}
	return true, nil
}

// InstallHostRelease publishes resident/dashboard source independently of the
// clone. It is a sibling of the runner payload so agent containers never gain
// visibility into native-host source.
func InstallHostRelease(home, repoRoot string) (string, error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return "", fmt.Errorf("resolve host-release MC_HOME: %w", err)
	}
	if err := requireOwnerOnlyDirectory(canonicalHome, "MC_HOME"); err != nil {
		return "", err
	}
	if repoRoot == "" || !filepath.IsAbs(repoRoot) {
		return "", fmt.Errorf("host release source must be an absolute repository root")
	}
	repoRoot = filepath.Clean(repoRoot)
	if err := requireOperatorSourceDirectory(repoRoot, "host release source root"); err != nil {
		return "", err
	}
	bodies := make(map[string][]byte, len(hostReleaseFiles))
	for _, rel := range hostReleaseFiles {
		body, err := readReleaseSource(repoRoot, rel)
		if err != nil {
			return "", err
		}
		bodies[rel] = body
	}
	releaseRoot := filepath.Join(canonicalHome, "release")
	if err := os.Mkdir(releaseRoot, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("create release root: %w", err)
	}
	if err := requireOwnerOnlyDirectory(releaseRoot, "release root"); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(releaseRoot, ".host-stage-")
	if err != nil {
		return "", fmt.Errorf("create host release stage: %w", err)
	}
	defer os.RemoveAll(stage)
	for _, rel := range hostReleaseDirs[1:] {
		if err := os.Mkdir(filepath.Join(stage, filepath.FromSlash(rel)), 0o700); err != nil {
			return "", fmt.Errorf("stage host directory %q: %w", rel, err)
		}
	}
	for _, rel := range hostReleaseFiles {
		if err := writeReleaseFile(stage, rel, bodies[rel]); err != nil {
			return "", fmt.Errorf("stage host file %q: %w", rel, err)
		}
	}
	for i := len(hostReleaseDirs) - 1; i >= 0; i-- {
		if err := syncDirectory(filepath.Join(stage, filepath.FromSlash(hostReleaseDirs[i]))); err != nil {
			return "", fmt.Errorf("sync staged host directory: %w", err)
		}
	}
	if err := ValidateHostRelease(stage); err != nil {
		return "", fmt.Errorf("validate staged host release: %w", err)
	}
	canonical := filepath.Join(releaseRoot, "host")
	info, statErr := os.Lstat(canonical)
	if statErr == nil {
		uid, _, ok := statOwner(info)
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !ok || int(uid) != os.Getuid() {
			return "", fmt.Errorf("installed host release %q must remain a real operator-owned directory", canonical)
		}
		if err := ValidateHostRelease(canonical); err == nil {
			equal, err := hostReleasesEqual(stage, canonical)
			if err != nil {
				return "", fmt.Errorf("compare installed host release: %w", err)
			}
			if equal {
				return "ok", nil
			}
		}
		if err := atomicExchangeDirectories(stage, canonical); err != nil {
			return "", fmt.Errorf("atomically exchange installed host release: %w", err)
		}
		if err := syncDirectory(releaseRoot); err != nil {
			return "", fmt.Errorf("sync release root after host exchange: %w", err)
		}
		if err := os.RemoveAll(stage); err != nil {
			return "", fmt.Errorf("remove replaced host release: %w", err)
		}
		if err := syncDirectory(releaseRoot); err != nil {
			return "", fmt.Errorf("sync release root after replaced host removal: %w", err)
		}
		return "done", nil
	}
	if !errors.Is(statErr, fs.ErrNotExist) {
		return "", fmt.Errorf("inspect installed host release: %w", statErr)
	}
	if err := os.Rename(stage, canonical); err != nil {
		return "", fmt.Errorf("publish installed host release: %w", err)
	}
	if err := syncDirectory(releaseRoot); err != nil {
		return "", fmt.Errorf("sync release root after host publication: %w", err)
	}
	return "done", nil
}

// HostReleaseMatches compares the installed files without a release change.
func HostReleaseMatches(home, source string) (bool, error) {
	root := filepath.Join(home, "release", "host")
	if source == "" {
		return false, nil
	}
	if err := ValidateHostRelease(root); err != nil {
		return false, nil
	}
	return hostReleasesEqual(root, source)
}
