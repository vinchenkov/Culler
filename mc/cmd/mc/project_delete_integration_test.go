package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func projectDockerFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "containers"), 0700); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env python3
import json, os, pathlib, sys, time
root = pathlib.Path(os.environ['CULLER_DOCKER_FIXTURE'])
containers = root / 'containers'
args = sys.argv[1:]
if args[0] == 'ps':
    if (root / 'fail-list').exists(): sys.exit(1)
    selected = next((v[3:] for v in args if v.startswith('id=')), None)
    print('\n'.join(p.stem for p in containers.glob('*.json') if selected is None or p.stem == selected))
elif args[0] == 'inspect':
    path = containers / (args[-1] + '.json')
    if not path.exists():
        print('No such container', file=sys.stderr)
        sys.exit(1)
    print('[' + path.read_text() + ']')
elif args[0] == 'rm':
    if (root / 'fail-remove').exists(): sys.exit(1)
    (containers / (args[-1] + '.json')).unlink(missing_ok=True)
elif args[0] == 'build':
    (root / 'build-entered').touch()
    while True: time.sleep(.01)
elif args[0] in ('create', 'run'):
    (root / 'create-entered').touch()
    while (root / 'delay-create').exists(): time.sleep(.01)
    labels = dict(args[i+1].split('=', 1) for i in range(len(args)-1) if args[i] == '--label')
    identity = 'a' * 64
    path = containers / (identity + '.json')
    path.write_text(json.dumps({'Id': identity, 'Config': {'Labels': labels}, 'Mounts': []}))
    print(identity, flush=True)
    if args[0] == 'run':
        while path.exists(): time.sleep(.01)
else:
    raise Exception('Unexpected Docker command: ' + args[0])
`
	if err := os.WriteFile(filepath.Join(root, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CULLER_DOCKER_FIXTURE", root)
	return root
}

func fixtureContainer(t *testing.T, root, id string, labels map[string]string, source string) {
	t.Helper()
	if parent, err := filepath.EvalSymlinks(filepath.Dir(source)); err == nil {
		source = filepath.Join(parent, filepath.Base(source))
	}
	body, err := json.Marshal(map[string]any{"Id": id, "Config": map[string]any{"Labels": labels}, "Mounts": []map[string]string{{"Source": source}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "containers", id+".json"), body, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectDeleteStopsContainersAndPreservesOtherProjects(t *testing.T) {
	spine := initSpine(t)
	root := projectDockerFixture(t)
	home, err := filepath.EvalSymlinks(filepath.Dir(spine))
	if err != nil {
		t.Fatal(err)
	}
	for i, labels := range []map[string]string{
		{"mc-managed": "true", "mc-project": "ws-test", "mc-tier": "pipeline"},
		{"mc-managed": "true", "mc-project": "ws-test", "mc-tier": "setup"},
		{"mc-managed": "true", "mc-project": "ws-test", "mc-tier": "wiki-export"},
		{"mc-managed": "true", "com.culler.project": "ws-test", "com.culler.home": home, "mc-tier": "collie"},
		{"mc-managed": "true", "mc-project": "other"},
		{"mc-managed": "true", "mc-project": "ws-test", "com.culler.home": home + "-other"},
		{"mc-managed": "true", "mc-tier": "collie"},
		{"mc-project": "ws-test"},
	} {
		fixtureContainer(t, root, strings.Repeat(string(rune('1'+i)), 64), labels, home+"/runs")
	}
	if result := runMC(t, spineEnv(spine), "", "project", "delete", "ws-test", "--confirm"); result.code != 0 {
		t.Fatal(result.stderr)
	}
	entries, err := os.ReadDir(filepath.Join(root, "containers"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("Deletion retained %d containers; want four unrelated containers.", len(entries))
	}
	for _, entry := range entries {
		if entry.Name()[0] < '5' {
			t.Fatal("Deletion retained a Project container.")
		}
	}
	if result := runMC(t, spineEnv(spine), "", "project", "list"); len(result.json["projects"].([]any)) != 0 {
		t.Fatal(result.stdout)
	}
}

func TestProjectDeleteRetainsRecordsAfterDockerFailure(t *testing.T) {
	for _, failure := range []string{"fail-list", "fail-remove"} {
		t.Run(failure, func(t *testing.T) {
			spine := initSpine(t)
			root := projectDockerFixture(t)
			fixtureContainer(t, root, strings.Repeat("a", 64), map[string]string{"mc-managed": "true", "mc-project": "ws-test"}, filepath.Dir(spine)+"/runs")
			path := filepath.Join(root, failure)
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			result := runMC(t, spineEnv(spine), "", "project", "delete", "ws-test", "--confirm")
			if result.code != 1 || !strings.Contains(result.stderr, "records remain intact") {
				t.Fatalf("Deletion ignored Docker failure: %+v", result)
			}
			if result := runMC(t, spineEnv(spine), "", "project", "list"); len(result.json["projects"].([]any)) != 1 {
				t.Fatal("A failed deletion removed the Project.")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if result := runMC(t, spineEnv(spine), "", "project", "delete", "ws-test", "--confirm"); result.code != 0 {
				t.Fatal(result.stderr)
			}
		})
	}
}

func awaitProjectCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("The Project fixture did not reach its expected state.")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProjectDeleteFencesAConcurrentContainerCreate(t *testing.T) {
	spine := initSpine(t)
	root := projectDockerFixture(t)
	delay := filepath.Join(root, "delay-create")
	if err := os.WriteFile(delay, nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var createOut, deleteOut bytes.Buffer
	create := exec.CommandContext(ctx, mcBin, "__project-docker", "ws-test", "--", "create", "--label", "mc-managed=true", "--label", "mc-project=ws-test", "image")
	create.Env = hermeticEnv(spineEnv(spine)...)
	create.Stdout, create.Stderr = &createOut, &createOut
	if err := create.Start(); err != nil {
		t.Fatal(err)
	}
	awaitProjectCondition(t, func() bool { _, err := os.Stat(filepath.Join(root, "create-entered")); return err == nil })
	remove := exec.CommandContext(ctx, mcBin, "project", "delete", "ws-test", "--confirm")
	remove.Env = hermeticEnv(spineEnv(spine)...)
	remove.Stdout, remove.Stderr = &deleteOut, &deleteOut
	if err := remove.Start(); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(spine), "project-locks", "ws-test.admission"), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	awaitProjectCondition(t, func() bool {
		err := unix.Flock(int(lock.Fd()), unix.LOCK_SH|unix.LOCK_NB)
		if err == nil {
			_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
		}
		return err == unix.EWOULDBLOCK
	})
	refused := runMC(t, spineEnv(spine), "", "__project-docker", "ws-test", "--", "create", "image")
	if refused.code != 1 {
		t.Fatalf("Deletion admitted a new create: %+v", refused)
	}
	if err := os.Remove(delay); err != nil {
		t.Fatal(err)
	}
	if err := create.Wait(); err != nil {
		t.Fatalf("The admitted create failed: %v, %s", err, createOut.String())
	}
	if err := remove.Wait(); err != nil {
		t.Fatalf("Deletion failed: %v, %s", err, deleteOut.String())
	}
	entries, err := os.ReadDir(filepath.Join(root, "containers"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("Deletion left a late container: %v, %v", entries, err)
	}
	if result := runMC(t, spineEnv(spine), "", "__project-docker", "ws-test", "--", "create", "image"); result.code != 1 {
		t.Fatal("A removed Project admitted a late launch.")
	}
}

func TestProjectDeleteGuardRefusesAnAbsentOperation(t *testing.T) {
	spine := initSpine(t)
	root := projectDockerFixture(t)
	for _, label := range []string{"mc-run-id=absent", "mc-session-id=absent", "com.culler.environment.operation=absent"} {
		result := runMC(t, spineEnv(spine), "", "__project-docker", "ws-test", "--", "create", "--label", label, "image")
		if result.code != 1 || !strings.Contains(result.stderr, "operation no longer exists") {
			t.Fatalf("An absent operation reached Docker: %+v", result)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "create-entered")); !os.IsNotExist(err) {
		t.Fatal("An absent operation started Docker.")
	}
}

func TestProjectDeleteCancelsAnImageBuild(t *testing.T) {
	spine := initSpine(t)
	root := projectDockerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, mcBin, "__project-docker", "ws-test", "--", "build", "--label", "mc-project=ws-test", "context")
	command.Env = hermeticEnv(spineEnv(spine)...)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	awaitProjectCondition(t, func() bool { _, err := os.Stat(filepath.Join(root, "build-entered")); return err == nil })
	if result := runMC(t, spineEnv(spine), "", "project", "delete", "ws-test", "--confirm"); result.code != 0 {
		t.Fatal(result.stderr)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("The build command failed: %v, %s", err, output.String())
	}
	var result struct {
		ExitCode int `json:"exitCode"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.ExitCode == 0 {
		t.Fatalf("Deletion left the build active: %s, %v", output.String(), err)
	}
}
