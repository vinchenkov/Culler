package main_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectImageDigestsVerbListsSelectionsForTheHostOnly(t *testing.T) {
	spine := initSpine(t)
	db := openDB(t, spine)
	base := "sha256:" + strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	if _, err := db.Exec(`INSERT INTO project_environments(project_id, setup_id, selected_base_digest, selected_image_digest) VALUES('ws-test', 'setup-test', ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET selected_base_digest=excluded.selected_base_digest, selected_image_digest=excluded.selected_image_digest`, base, image); err != nil {
		t.Fatal(err)
	}
	res := runMC(t, spineEnv(spine), "", "__project-image-digests")
	if res.code != 0 {
		t.Fatalf("__project-image-digests failed (%d): %s", res.code, res.stderr)
	}
	body, err := json.Marshal(res.json)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"digests":["` + base + `","` + image + `"]}`; string(body) != want {
		t.Fatalf("__project-image-digests =\n%s\nwant\n%s", body, want)
	}
	if usage := runMC(t, spineEnv(spine), "", "__project-image-digests", "--all"); usage.code != 2 {
		t.Fatalf("an extra argument exited %d: %s", usage.code, usage.stderr)
	}
	agent := runMC(t, runJSONEnv(t, spine, "r-worker", "pipeline", "worker"), "", "__project-image-digests")
	if agent.code != 1 || !strings.Contains(agent.stderr, "host scope only") || strings.Contains(agent.stdout, base) {
		t.Fatalf("an agent read the Project image digests (%d): %s %s", agent.code, agent.stdout, agent.stderr)
	}
}
