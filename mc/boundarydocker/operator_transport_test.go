//go:build docker_boundary

package boundarydocker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/substrate"
	"mc/verbs"
)

// The test stages current protected commands over an existing runtime image.
// Both volumes belong to this test and contain no deployment data.
func TestCollieOperatorTransportDockerBoundary(t *testing.T) {
	requireProdImage(t)
	stage := sharedTempDir(t)
	for _, build := range []struct{ output, source string }{{"mc-real", "./cmd/mc"}, {"mc", "../runner/image/mc_dispatch.go"}} {
		command := exec.Command("mise", "exec", "--", "go", "build", "-ldflags=-X main.releaseBuildID=development", "-o", filepath.Join(stage, build.output), build.source)
		command.Dir = ".."
		command.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("Build the current protected command: %v\n%s", err, out)
		}
	}
	db, err := substrate.Open(filepath.Join(stage, "spine.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := substrate.Init(db); err != nil {
		t.Fatal(err)
	}
	permissions := []string{"project.pause", "project.archive"}
	allowlist, _ := json.Marshal(permissions)
	for _, statement := range []string{
		`INSERT INTO meta(id,deployment_uuid,schema_id) VALUES(1,'00000000-0000-4000-8000-000000000001','` + substrate.CurrentSchemaID + `')`,
		`INSERT INTO sandbox_profiles(id,workspace_root) VALUES('operator-profile','/workspace')`,
		`INSERT INTO projects(id,title,sandbox_profile) VALUES('operator-project','Operator boundary','operator-profile')`,
		`INSERT INTO collie_sessions(id,container_name,verb_allowlist,session_path,binding) VALUES('h-operator-boundary','boundary','` + string(allowlist) + `','/mc/session','codex/fixture')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	skills, err := verbs.CollieSkillSet()
	if err != nil {
		t.Fatal(err)
	}
	resources := make([]map[string]any, 0, len(skills))
	for _, skill := range skills {
		resource := map[string]any{"name": skill.Name, "body": skill.Body}
		if len(skill.Files) > 0 {
			resource["files"] = skill.Files
		}
		resources = append(resources, resource)
	}
	for _, release := range []string{"development", "stale"} {
		body, _ := json.Marshal(map[string]any{"run_id": "h-operator-boundary", "tier": "collie", "release_build_id": release, "verb_allowlist": permissions, "skills": resources})
		if err := os.WriteFile(filepath.Join(stage, "run-"+release+".json"), body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	installer, err := os.ReadFile("../../runner/agent-runner/skills.ts")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "skills.ts"), installer, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stage, 0755); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	runtimeVolume, spineVolume := "culler-operator-runtime-"+suffix, "culler-operator-spine-"+suffix
	for _, volume := range []string{runtimeVolume, spineVolume} {
		if out, err := exec.Command("docker", "volume", "create", volume).CombinedOutput(); err != nil {
			t.Fatalf("Create the scratch volume: %v %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", volume).Run() })
	}
	setup := `mkdir -p /runtime/commands /runtime/libexec /mc/spine/supervision
cp /fixture/mc /runtime/commands/mc
cp /fixture/mc-real /runtime/libexec/mc-real
chown 10001:10001 /runtime/libexec/mc-real
chmod 6755 /runtime/libexec/mc-real
chmod 0755 /runtime/commands/mc
cp /fixture/spine.db /mc/spine/spine.db
printf '%s\n' '{"releaseBuildId":"development","mcHome":"/mc/spine"}' >/mc/spine/supervision/resident.json
chown -R 10001:10001 /mc/spine
chmod 0700 /mc/spine
chmod 0600 /mc/spine/spine.db /mc/spine/supervision/resident.json`
	if out, err := dockerRun(t, "--user", "0:0", "-v", stage+":/fixture:ro", "-v", runtimeVolume+":/runtime", "-v", spineVolume+":/mc/spine", prodImage, "sh", "-ec", setup); err != nil {
		t.Fatalf("Stage the protected runtime: %v\n%s", err, out)
	}
	prefix := []string{"run", "--rm", "-i", "--network", "none", "--cap-drop", "ALL", "-e", "MC_HOME=/mc/spine",
		"--mount", "type=volume,src=" + runtimeVolume + ",dst=/opt/culler-runtime/commands,volume-subpath=commands,readonly",
		"--mount", "type=volume,src=" + runtimeVolume + ",dst=/opt/culler-runtime/libexec,volume-subpath=libexec,readonly",
		"-v", spineVolume + ":/mc/spine", "-v", stage + ":/fixture:ro"}
	invoke := func(agent bool, identity, input string, args ...string) (map[string]any, string, int) {
		t.Helper()
		argv := append([]string{}, prefix...)
		if agent {
			argv = append(argv, "--user", "10002:10002", "-v", filepath.Join(stage, "run-"+identity+".json")+":/mc/run.json:ro", "-e", "MC_RUN_JSON=/tmp/forged-absence.json")
		} else {
			argv = append(argv, "--user", "10001:10001")
		}
		argv = append(argv, prodImage, "/opt/culler-runtime/commands/mc")
		argv = append(argv, args...)
		command := exec.Command("docker", argv...)
		command.Stdin = strings.NewReader(input)
		var out, diagnostic bytes.Buffer
		command.Stdout = &out
		command.Stderr = &diagnostic
		code := 0
		if err := command.Run(); err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		var result map[string]any
		if json.Unmarshal(out.Bytes(), &result) != nil {
			t.Fatalf("The boundary returned no JSON object: %s %s", out.String(), diagnostic.String())
		}
		return result, diagnostic.String(), code
	}
	request := func(id, action string) string {
		body, _ := json.Marshal(map[string]any{"id": id, "command": []string{"project", action, "operator-project"}, "release": "development", "origin": "external"})
		return string(body)
	}
	if help, _, code := invoke(true, "development", "", "--help"); code != 0 || help["release"] != "development" {
		t.Fatalf("The test did not execute its current binary: %v", help)
	}
	if _, text, code := invoke(true, "stale", request("stale", "pause"), "__operator-submit"); code != 1 || !strings.Contains(text, "different releases") {
		t.Fatalf("A stale launch passed admission: %d %s", code, text)
	}
	submitted, text, code := invoke(true, "development", request("boundary-pause", "pause"), "__operator-submit")
	if code != 0 || submitted["origin"] != "collie" {
		t.Fatalf("The agent could not submit its request: %d %v %s", code, submitted, text)
	}
	for _, args := range [][]string{{"__operator-execute", "boundary-pause"}, {"__operator-forward", "boundary-pause", "--", "project", "pause", "operator-project"}, {"__operator-refuse"}} {
		if _, text, code := invoke(true, "development", "", args...); code != 1 || !strings.Contains(text, "host scope") {
			t.Fatalf("The protected command admitted host execution: %d %s", code, text)
		}
	}
	if _, text, code := invoke(false, "", "", "__operator-execute", "boundary-pause"); code != 0 {
		t.Fatalf("The host could not execute the accepted request: %s", text)
	}
	receipt, text, code := invoke(true, "development", "", "operation", "get", "boundary-pause")
	if code != 0 {
		t.Fatal(text)
	}
	op := receipt["operation"].(map[string]any)
	if op["state"] != "complete" || op["exit_code"] != float64(0) {
		t.Fatalf("The request did not complete: %v", op)
	}
	listed, text, code := invoke(false, "", "", "project", "list")
	if code != 0 || listed["projects"].([]any)[0].(map[string]any)["status"] != "paused" {
		t.Fatalf("The request changed no Project: %v %s", listed, text)
	}
	check := `import { installRunSkills } from "/fixture/skills.ts";
const envelope = JSON.parse(await Bun.file("/mc/run.json").text());
const root = "/home/mc-model/.agents/skills";
await installRunSkills(root, envelope.skills);
const skill = envelope.skills.find(value => value.name === "culler");
if (!skill || Object.keys(skill.files).length < 9) throw new Error("The common skill references are absent.");
for (const [name, body] of Object.entries(skill.files)) {
  if (await Bun.file(root + "/culler/" + name).text() !== body) throw new Error("A skill reference differs.");
}
const direct = Bun.spawnSync(["sh", "-c", "cat /mc/spine/spine.db >/dev/null"], {stderr:"ignore"});
if (direct.exitCode === 0) throw new Error("The agent read the spine directly.");
const help = Bun.spawnSync(["mc", "project", "pause", "--help"]);
if (help.exitCode !== 0 || JSON.parse(new TextDecoder().decode(help.stdout)).command.path.join(" ") !== "project pause") throw new Error("The protected command help failed.");
console.log(JSON.stringify({skill:skill.name, references:Object.keys(skill.files).length, spine_denied:true, help:true}));`
	args := append(append([]string{}, prefix...), "--user", "10002:10002", "-v", filepath.Join(stage, "run-development.json")+":/mc/run.json:ro", "-e", "HOME=/home/mc-model", prodImage, "/opt/culler-runtime/bin/bun", "-e", check)
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("The installed skill or command boundary failed: %v\n%s", err, out)
	} else {
		t.Logf("The current protected runtime passed: %s", strings.TrimSpace(string(out)))
	}
}
