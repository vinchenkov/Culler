//go:build docker_boundary

package boundarydocker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mc/wiki"
)

// The export command runs in the runner image with no network, no
// credential, and a read-only root. It reads only the committed openwiki/
// tree and leaves no container behind.
func TestWikiExportRunsInTheRunnerImageDockerBoundary(t *testing.T) {
	requireProdImage(t)
	root := sharedTempDir(t)
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "openwiki", "guide"), 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Boundary", "-c",
			"user.email=boundary@example.test", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	for name, body := range map[string]string{
		"openwiki/index.md":       "---\ntitle: Boundary\ntype: Index\n---\n# Boundary\n\nRead the [guide](guide/start.md).\n",
		"openwiki/guide/start.md": "---\ntitle: Start\ntype: Guide\n---\n# Start\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("commit", "-q", "-m", "Accept the wiki")
	accepted := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "openwiki", "draft.md"), []byte("# Draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exporter := wiki.Exporter{Root: filepath.Join(root, "home", "wiki"), Image: prodImage, Run: wiki.DockerExport}
	results, err := exporter.Export(context.Background(), []wiki.ExportProject{{ID: "ws-boundary", Root: repo}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != wiki.ExportExported || results[0].Commit == nil || *results[0].Commit != accepted {
		t.Fatalf("export results = %+v", results)
	}
	record, ok := wiki.ReadExport(exporter.Root, "ws-boundary")
	if !ok || record.Commit != accepted {
		t.Fatalf("export record = %+v %v", record, ok)
	}
	folder := filepath.Join(exporter.Root, "ws-boundary", accepted)
	body, err := os.ReadFile(filepath.Join(folder, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		Root  string `json:"root"`
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		Edges []any `json:"edges"`
	}
	if err := json.Unmarshal(body, &graph); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, node := range graph.Nodes {
		ids = append(ids, node.ID)
	}
	if graph.Root != "openwiki" || len(graph.Nodes) != 2 || len(graph.Edges) != 1 || strings.Contains(string(body), "Draft") {
		t.Fatalf("graph root %q, nodes %v, edges %d", graph.Root, ids, len(graph.Edges))
	}
	page, err := os.ReadFile(filepath.Join(folder, "index.html"))
	if err != nil || !strings.Contains(string(page), `data-static-export="true"`) {
		t.Fatalf("the exported page is not a static export: %v", err)
	}
	for _, name := range wiki.ExportFiles {
		info, err := os.Stat(filepath.Join(folder, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
	}
	left, err := exec.Command("docker", "ps", "-a", "--filter", "label=mc-tier=wiki-export",
		"--filter", "label=mc-project=ws-boundary", "--format", "{{.Names}}").CombinedOutput()
	if err != nil || strings.TrimSpace(string(left)) != "" {
		t.Fatalf("the export left a container: %q %v", left, err)
	}
}
