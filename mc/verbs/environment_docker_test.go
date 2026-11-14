//go:build docker_boundary

package verbs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/deployment"
)

func TestEnvironmentSetupRunnerUsesMountedRuntime(t *testing.T) {
	testEnvironmentSetupRunnerDocker(t, "")
}

func TestEnvironmentImageDependenciesResumeWithOneAdditionalAgent(t *testing.T) {
	testEnvironmentSetupRunnerDocker(t, "approve")
}

func TestEnvironmentDeniedImageImportsWithCurrentImage(t *testing.T) {
	testEnvironmentSetupRunnerDocker(t, "deny")
}

func testEnvironmentSetupRunnerDocker(t *testing.T, decision string) {
	project := os.Getenv("CULLER_PROJECT_TEST_IMAGE")
	if project == "" {
		project = "mc-project"
	}
	runtime := os.Getenv("CULLER_RUNTIME_TEST_IMAGE")
	if runtime == "" {
		runtime = "mc-prod"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	docker := func(input string, args ...string) []byte {
		t.Helper()
		command := exec.CommandContext(ctx, "docker", args...)
		command.Stdin = strings.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("Docker %v: %v\n%s", args, err, output)
		}
		return output
	}
	image := strings.TrimSpace(string(docker("", "image", "inspect", "--format", "{{.Id}}", project)))
	db := environmentFixture(t)
	environmentCall(t, db, nil, "base-record", EnvironmentRequest{BaseDigest: image, Metadata: map[string]any{"release_identity": "docker-test"}})
	configuration := EnvironmentConfiguration{
		BaseDigest:   image,
		Setup:        EnvironmentCommand{Command: `test -n "$SETUP_TOKEN" && test -z "${APP_TOKEN:-}" && printf ready >.setup-proof`, Directory: "."},
		Secrets:      []EnvironmentVariable{{Name: "SETUP_TOKEN", Reference: "setup-fixture", Scope: "setup"}, {Name: "APP_TOKEN", Reference: "app-fixture", Scope: "agent"}},
		Declarations: []string{"package.json"},
	}
	environmentCall(t, db, nil, "save", EnvironmentRequest{ProjectID: "ws-test", Configuration: &configuration})
	work := environmentCall(t, db, nil, "work", EnvironmentRequest{DefaultImageDigest: image, RuntimeIdentity: "docker-setup-check", RuntimeBinding: PrivateRuntimeBinding{ID: "fake", Harness: "fake"}})["operations"].([]EnvironmentOperation)
	if len(work) != 1 || work[0].Kind != "setup" {
		t.Fatalf("setup operations: %+v", work)
	}
	op := work[0]
	fixture := t.TempDir()
	if err := os.Chmod(fixture, 0755); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(fixture, "session")
	if err := os.Mkdir(session, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(session, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(op.WorkspaceRoot, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(op.WorkspaceRoot, ".gitignore"), []byte(".setup-proof\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("VACUUM INTO ?", filepath.Join(fixture, "spine.db")); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(fixture, name), body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	envelope := func(operation EnvironmentOperation) map[string]any {
		var tree string
		if decision == "deny" && operation.Kind == "validate" {
			body, err := os.ReadFile(filepath.Join(session, "project-input-tree"))
			if err != nil {
				t.Fatal(err)
			}
			tree = strings.TrimSpace(string(body))
		}
		return map[string]any{
			"tier": "setup", "role": "setup", "run_id": operation.ID, "project": operation.ProjectID, "harness": "fake", "model_binding": "fake",
			"brief": operation.Brief, "skills": operation.Skills, "harness_config": map[string]string{"behavior": "fixture"},
			"mounts":      map[string]string{"workspace": "/workspace/source", "session": "/mc/session"},
			"environment": map[string]any{"configuration": operation.Configuration, "revision_id": operation.RevisionID, "inputs_digest": operation.InputsDigest, "kind": operation.Kind, "initial_setup": true, "setup_id": operation.Metadata["setup_id"], "input_tree": tree, "denied_build_files": operation.Metadata["denied_build_files"]},
		}
	}
	write("run.json", envelope(op))
	harness := `import {projectFilesDigest} from "/opt/culler-runtime/app/src/shared/project-files.ts";
const brief=await Bun.stdin.text();
if(brief.includes("private-setup-value")||brief.includes("private-app-value"))throw Error("A secret entered the brief");
if(process.env.SETUP_TOKEN!==undefined||process.env.APP_TOKEN!=="private-app-value")throw Error("The agent secret scope differs");
if(await Bun.file(".setup-proof").text()!=="ready")throw Error("The setup command did not finish");
await Bun.write("package.json",JSON.stringify({name:"setup-fixture",private:true,scripts:{check:"node --check index.js"}}));
await Bun.write("index.js","export {};\n");
const check=Bun.spawnSync(["node","--check","index.js"]);
if(check.exitCode!==0)throw Error("The actual application check failed");
const nodeVersion=Bun.spawnSync(["node","--version"]);
if(nodeVersion.exitCode!==0)throw Error("The Node runtime check failed");
await Bun.write("/mc/session/first-check.json",JSON.stringify({completed_at:Date.now()}));
const envelope=await Bun.file("/mc/run.json").json();
const report={operation_id:envelope.run_id,outcome:"complete",configuration:envelope.environment.configuration,
 files_digest:await projectFilesDigest(process.cwd()),summary:"The Project preparation is complete.",details:"Node and the syntax command returned exit 0.",
 tool_versions:{node:nodeVersion.stdout.toString().trim()},package_records:["package.json"]};
if(process.env.FIXTURE_IMAGE_DECISION) {
 if(envelope.environment.kind==="setup") {
  const content="FROM culler-project-base:"+envelope.environment.configuration.base_digest.slice(7)+"\nRUN printf image-dependency >/usr/local/share/image-dependency-proof\n";
  report.configuration.dockerfile={path:".culler/Dockerfile",content};
  report.outcome="proposal";report.summary="The development workflow requires the additional system file.";
  await Bun.write(".culler/Dockerfile",content);
 } else if(process.env.FIXTURE_IMAGE_DECISION==="deny") {
  if(await Bun.file(".culler/Dockerfile").exists())throw Error("The denied image file remains in the source");
  report.summary="Setup used the current image. The proposed image tool remains unavailable.";
 } else if(await Bun.file("/usr/local/share/image-dependency-proof").text()!=="image-dependency")throw Error("The additional image file is absent");
}
await Bun.write("/mc/session/report.json",JSON.stringify(report));
const result=Bun.spawnSync(["mc","environment","report"],{stdin:Buffer.from(JSON.stringify(report))});
if(result.exitCode!==0)throw Error(result.stderr.toString()+result.stdout.toString());
console.log("The setup runner completed its check and report.");
`
	if err := os.WriteFile(filepath.Join(fixture, "harness.ts"), []byte(harness), 0644); err != nil {
		t.Fatal(err)
	}
	volume := fmt.Sprintf("culler-setup-runtime-test-%d", time.Now().UnixNano())
	spine := volume + "-spine"
	docker("", "volume", "create", volume)
	docker("", "volume", "create", spine)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", volume, spine).Run() })
	docker("", "run", "--rm", "--network", "none", "--user", "0:0", "--mount", "type=volume,src="+volume+",dst=/runtime-output,volume-nocopy", runtime, "/bin/sh", "-ec", "cp -a /opt/culler-runtime/. /runtime-output/")
	docker("", "run", "--rm", "--network", "none", "--user", "0:0", "--mount", "type=volume,src="+spine+",dst=/mc/spine", "--mount", "type=bind,src="+fixture+",dst=/fixture,readonly", project, "/bin/sh", "-ec", "cp /fixture/spine.db /mc/spine/spine.db; chown 10001:10001 /mc/spine/spine.db; chmod 0600 /mc/spine/spine.db")
	common := []string{"run", "--rm", "--interactive", "--network", "none", "--user", "10002:10002", "--cap-drop", "ALL",
		"--mount", "type=volume,src=" + volume + ",dst=/opt/culler-runtime,readonly,volume-nocopy",
		"--mount", "type=volume,src=" + spine + ",dst=/mc/spine",
		"--mount", "type=bind,src=" + op.WorkspaceRoot + ",dst=/workspace/source",
		"--env", "MC_SPINE=/mc/spine/spine.db", "--env", "HOME=/home/mc-model",
		"--env", "PATH=/opt/culler-runtime/commands:/usr/local/bin:/usr/bin:/bin"}
	agent := append(append([]string{}, common...), "--mount", "type=bind,src="+filepath.Join(fixture, "run.json")+",dst=/mc/run.json,readonly",
		"--mount", "type=bind,src="+fixture+",dst=/fixture,readonly", "--mount", "type=bind,src="+session+",dst=/mc/session",
		"--env", "FIXTURE_IMAGE_DECISION="+decision, "--env", "MC_HARNESS_CLI=/fixture/harness.ts", "--env", "MC_PRIVATE_BUN=/opt/culler-runtime/bin/bun",
		"--entrypoint", "/opt/culler-runtime/bin/bun", project, "/opt/culler-runtime/app/src/setup-runner/main.ts")
	started := time.Now()
	output := docker("{\"SETUP_TOKEN\":\"private-setup-value\",\"APP_TOKEN\":\"private-app-value\"}\n", agent...)
	if strings.Contains(string(output), "private-setup-value") || strings.Contains(string(output), "private-app-value") {
		t.Fatal("A secret entered the output")
	}
	var firstCheck struct {
		CompletedAt int64 `json:"completed_at"`
	}
	checkBody, err := os.ReadFile(filepath.Join(session, "first-check.json"))
	if err != nil || json.Unmarshal(checkBody, &firstCheck) != nil || firstCheck.CompletedAt == 0 {
		t.Fatal("The setup runner omitted its first check timestamp.")
	}
	t.Logf("The agent tool observations finished %.3f seconds after the container start request.", time.UnixMilli(firstCheck.CompletedAt).Sub(started).Seconds())
	var report EnvironmentRequest
	body, err := os.ReadFile(filepath.Join(session, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	control := func(action string, request EnvironmentRequest) map[string]json.RawMessage {
		t.Helper()
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		args := append(append([]string{}, common...), "--entrypoint", "/opt/culler-runtime/commands/mc", project, "environment", action)
		var result map[string]json.RawMessage
		if err = json.Unmarshal(docker(string(body), args...), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	selectedImage := image
	if decision != "" {
		view := control("get", EnvironmentRequest{ProjectID: "ws-test"})
		var state EnvironmentState
		if err = json.Unmarshal(view["environment"], &state); err != nil {
			t.Fatal(err)
		}
		if state.Phase != "approval" {
			t.Fatalf("proposal state: %+v", state)
		}
		control(decision, EnvironmentRequest{ProjectID: "ws-test", RevisionID: state.DraftRevisionID})
		queued := control("work", EnvironmentRequest{DefaultImageDigest: image, RuntimeIdentity: "docker-setup-check", RuntimeBinding: PrivateRuntimeBinding{ID: "fake", Harness: "fake"}})
		if decision == "approve" {
			var builds []EnvironmentOperation
			if err = json.Unmarshal(queued["operations"], &builds); err != nil || len(builds) != 1 || builds[0].Kind != "build" {
				t.Fatalf("build operations: %+v %v", builds, err)
			}
			alias := "culler-project-base:" + strings.TrimPrefix(image, "sha256:")
			docker("", "tag", image, alias)
			if got := strings.TrimSpace(string(docker("", "image", "inspect", "--format", "{{.Id}}", alias))); got != image {
				t.Fatal("The build alias differs from the selected base.")
			}
			tag := volume + ":candidate"
			t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", tag).Run() })
			docker(builds[0].Configuration.Dockerfile.Content, "build", "--network", "none", "--tag", tag, "-")
			selectedImage = strings.TrimSpace(string(docker("", "image", "inspect", "--format", "{{.Id}}", tag)))
			if err = deployment.CheckProjectImage(ctx, image, selectedImage); err != nil {
				t.Fatal(err)
			}
			control("operation-result", EnvironmentRequest{OperationID: builds[0].ID, InputsDigest: builds[0].InputsDigest, Status: "succeeded", ImageDigest: selectedImage, RuntimeCompatible: true})
			queued = control("work", EnvironmentRequest{DefaultImageDigest: image, RuntimeIdentity: "docker-setup-check", RuntimeBinding: PrivateRuntimeBinding{ID: "fake", Harness: "fake"}})
		}
		var continued []EnvironmentOperation
		if err = json.Unmarshal(queued["operations"], &continued); err != nil || len(continued) != 1 || continued[0].Kind != "validate" || continued[0].ID == op.ID {
			t.Fatalf("continuation: %+v %v", continued, err)
		}
		write("run.json", envelope(continued[0]))
		next := append([]string{}, agent...)
		for i := range next {
			if next[i] == project {
				next[i] = selectedImage
			}
		}
		docker("{\"SETUP_TOKEN\":\"private-setup-value\",\"APP_TOKEN\":\"private-app-value\"}\n", next...)
		body, err = os.ReadFile(filepath.Join(session, "report.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(body, &report); err != nil {
			t.Fatal(err)
		}
	}
	pending := control("work", EnvironmentRequest{DefaultImageDigest: image, RuntimeIdentity: "docker-setup-check", RuntimeBinding: PrivateRuntimeBinding{ID: "fake", Harness: "fake"}})
	var accept []EnvironmentOperation
	if err = json.Unmarshal(pending["operations"], &accept); err != nil {
		t.Fatal(err)
	}
	if len(accept) != 1 || accept[0].Kind != "accept" {
		t.Fatalf("acceptance operations: %+v", accept)
	}
	files := append([]EnvironmentFile{}, accept[0].Configuration.BuildFiles...)
	if accept[0].Configuration.Dockerfile != nil {
		files = append(files, *accept[0].Configuration.Dockerfile)
	}
	acceptedFiles := map[string]any{"files": files}
	if decision == "deny" {
		tree, err := os.ReadFile(filepath.Join(session, "project-input-tree"))
		if err != nil {
			t.Fatal(err)
		}
		acceptedFiles["denied"] = map[string]any{"tree": strings.TrimSpace(string(tree)), "files": accept[0].Metadata["denied_build_files"]}
	}
	write("accepted-build-files.json", acceptedFiles)
	args := append(append([]string{}, common...), "--mount", "type=bind,src="+filepath.Join(fixture, "accepted-build-files.json")+",dst=/mc/build-files.json,readonly", "--env", "GIT_AUTHOR_NAME=Fixture", "--env", "GIT_AUTHOR_EMAIL=fixture@example.test", "--env", "GIT_COMMITTER_NAME=Fixture", "--env", "GIT_COMMITTER_EMAIL=fixture@example.test",
		"--entrypoint", "/opt/culler-runtime/bin/bun", image, "/opt/culler-runtime/app/src/shared/project-files.ts", "accept", "/workspace/source", report.FilesDigest, "", "/mc/build-files.json", accept[0].ID)
	var accepted struct {
		SourceCommit string `json:"source_commit"`
	}
	if err = json.Unmarshal(docker("", args...), &accepted); err != nil {
		t.Fatal(err)
	}
	view := control("operation-result", EnvironmentRequest{OperationID: accept[0].ID, InputsDigest: accept[0].InputsDigest, Status: "succeeded", SourceCommit: accepted.SourceCommit, FilesDigest: report.FilesDigest})
	var state EnvironmentState
	if err = json.Unmarshal(view["environment"], &state); err != nil {
		t.Fatal(err)
	}
	if state.Readiness != "ready" || state.SelectedImageDigest != selectedImage {
		t.Fatalf("accepted state: %+v", state)
	}
	pending = control("work", EnvironmentRequest{DefaultImageDigest: image, RuntimeIdentity: "docker-setup-check", RuntimeBinding: PrivateRuntimeBinding{ID: "fake", Harness: "fake"}})
	if err = json.Unmarshal(pending["operations"], &accept); err != nil || len(accept) != 0 {
		t.Fatalf("duplicate setup: %+v %v", accept, err)
	}
	t.Logf("The setup runner used scoped secrets and accepted the minimum Project source. Image decision: %s.", decision)
}
