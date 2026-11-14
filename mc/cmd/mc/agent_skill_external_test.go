package main_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mc/deployment"
)

func copyExternalAgentExecutable(t *testing.T, source, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseSkillWorksForExternalAgentWithoutSourceOrDevelopmentTools(t *testing.T) {
	spine := initSpine(t)
	home, err := filepath.EvalSymlinks(filepath.Dir(spine))
	if err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(home, "bin", "mc")
	copyExternalAgentExecutable(t, mcBin, binary)
	t.Setenv("MC_BIN_DIR", filepath.Dir(binary))

	resolveBun := exec.Command("mise", "exec", "--", "bun", "-p", "process.execPath")
	bunOutput, err := resolveBun.Output()
	if err != nil {
		t.Fatalf("resolve the installed host Bun fixture: %v", err)
	}
	bun := filepath.Join(home, "bun", "fixture", "bin", "bun")
	copyExternalAgentExecutable(t, strings.TrimSpace(string(bunOutput)), bun)
	previousBun := deployment.HostBunPath
	deployment.HostBunPath = func(string) (string, error) { return bun, nil }
	t.Cleanup(func() { deployment.HostBunPath = previousBun })
	if _, err := deployment.InstallHostRelease(home, source); err != nil {
		t.Fatal(err)
	}
	skillRoot := filepath.Join(home, "release", "host", "skills", "culler")
	var installation struct {
		CommandPrefix []string `json:"command_prefix"`
		Home          string   `json:"mc_home"`
		Release       string   `json:"release"`
		Bun           string   `json:"bun"`
		TraceRoot     string   `json:"trace_root"`
		TraceReader   string   `json:"trace_reader"`
	}
	body, err := os.ReadFile(filepath.Join(skillRoot, "installation.json"))
	if err != nil || json.Unmarshal(body, &installation) != nil {
		t.Fatalf("read installed metadata: %v", err)
	}
	if installation.Home != home || len(installation.CommandPrefix) != 3 || installation.CommandPrefix[1] != "MC_HOME="+home || installation.CommandPrefix[2] != binary || installation.Bun != bun {
		t.Fatalf("the installed skill does not select its custom deployment: %s", body)
	}

	links := regexp.MustCompile(`\]\(([^)# ]+)(?:#[^)]*)?\)`)
	err = filepath.WalkDir(skillRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range links.FindAllStringSubmatch(string(text), -1) {
			if strings.Contains(match[1], "://") {
				continue
			}
			target := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(match[1])))
			if !strings.HasPrefix(target, skillRoot+string(filepath.Separator)) {
				t.Fatalf("reference escapes the installed skill: %s", target)
			}
			if info, err := os.Lstat(target); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("installed reference %s: %v", target, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	invoke := func(program string, args []string, input string, environment ...string) map[string]any {
		t.Helper()
		command := exec.Command(program, args...)
		command.Dir = outside
		command.Env = hermeticEnv(append([]string{"PATH=/nonexistent"}, environment...)...)
		command.Stdin = strings.NewReader(input)
		var output, diagnostic bytes.Buffer
		command.Stdout, command.Stderr = &output, &diagnostic
		if err := command.Run(); err != nil {
			t.Fatalf("installed command %v: %v\n%s\n%s", args, err, output.String(), diagnostic.String())
		}
		var response map[string]any
		if err := json.Unmarshal(output.Bytes(), &response); err != nil {
			t.Fatalf("installed command did not return one object: %s (%v)", output.String(), err)
		}
		return response
	}
	envProgram, err := exec.LookPath(installation.CommandPrefix[0])
	if err != nil {
		t.Fatal(err)
	}
	agentCall := func(database string, args ...string) map[string]any {
		command := append(append([]string{}, installation.CommandPrefix[1:]...), args...)
		return invoke(envProgram, command, "", "MC_SPINE="+database)
	}
	absent := filepath.Join(home, "absent-spine.db")
	for _, args := range [][]string{{"--help"}, {"project", "--help"}, {"project", "resume", "--help"}} {
		response := agentCall(absent, args...)
		if response["release"] != installation.Release {
			t.Fatalf("help and installed skill use different releases: %v", response)
		}
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatalf("help created a database: %v", err)
	}

	before := agentCall(spine, "pause", "status")
	if len(before["projects"].([]any)) == 0 || before["projects"].([]any)[0].(map[string]any)["status"] != "active" {
		t.Fatalf("the installed command did not read the active scratch Project: %v", before)
	}
	request := "release-external-pause"
	changed := agentCall(spine, "pause", "--request-id", request)
	if changed["projects"].([]any)[0].(map[string]any)["status"] != "paused" {
		t.Fatalf("the installed command did not pause its Project: %v", changed)
	}
	replay := agentCall(spine, "pause", "--request-id", request)
	if replay["projects"].([]any)[0].(map[string]any)["status"] != "paused" {
		t.Fatalf("the repeated request changed its result: %v", replay)
	}
	current := agentCall(spine, "pause", "status")
	if current["projects"].([]any)[0].(map[string]any)["status"] != "paused" {
		t.Fatalf("the state read differs from the accepted write: %v", current)
	}
	receipt := agentCall(spine, "operation", "get", request)["operation"].(map[string]any)
	if receipt["origin"] != "external" || receipt["state"] != "complete" {
		t.Fatalf("the operation lost its external origin or completion: %v", receipt)
	}

	if err := os.MkdirAll(installation.TraceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(installation.TraceRoot, "trace.db")
	fixture := `import { Database } from "bun:sqlite";
 const db = new Database(process.argv[1]);
 db.exec("CREATE TABLE session_v2(id TEXT,parent_id TEXT,title TEXT,time_created INTEGER,time_updated INTEGER); CREATE TABLE session_message(id TEXT,session_id TEXT,type TEXT,seq INTEGER,time_created INTEGER,time_updated INTEGER,data TEXT); INSERT INTO session_v2 VALUES('ses_external',NULL,'Trace',1,2)");
 db.query("INSERT INTO session_message VALUES (?,?,?,?,?,?,?)").run("msg_external","ses_external","assistant",1,1,2,JSON.stringify({content:[{type:"tool",id:"tool_external",text:"x".repeat(25000)}]}));
 db.close(); console.log("{}");`
	invoke(installation.Bun, []string{"-e", fixture, trace}, "")
	traceResult := invoke(installation.Bun, []string{installation.TraceReader, trace, "ses_external"}, "", "MC_TRACE_ROOT="+installation.TraceRoot)
	message := traceResult["messages"].([]any)[0].(map[string]any)
	if message["id"] != "msg_external" || message["session_id"] != "ses_external" || len(message["data"].(string)) != 16000 || message["characters"].(float64) <= 16000 {
		t.Fatalf("the installed trace reader lost native records or exceeded its bound: %v", message)
	}
	t.Log("The installed skill, executable, and host Bun work from an unrelated directory with no development tools on PATH.")
	t.Log("The fixture supplies a scratch spine through the test-only command build; the production Docker helper remains a separate boundary.")
}
