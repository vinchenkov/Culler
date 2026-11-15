//go:build docker_e2e

// The artifact access of a packet conversation with real Docker binds. The
// pipeline produces the packet of one task. A preparation for that packet
// starts the Collie container of the conversation. The container reads the
// artifact files of its task through a read-only bind at the recorded artifact
// paths. An empty mask hides the artifact root of the Project folder, so the
// artifact files of each other task are unreachable.
package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc/verbs"
)

func TestColliePacketArtifactsDockerBoundary(t *testing.T) {
	f := setup(t)
	f.startResident()

	res := f.mcOK("", "task", "add", "packet conversation task", "--project", project,
		"--description", "The packet conversation reads the artifact files of this task.")
	taskID := int64(res["task_id"].(float64))
	f.waitForTaskStatus(taskID, "packaged", 300*time.Second)
	packet := f.waitForReadyPacket(taskID, 120*time.Second)
	packetID := int64(packet["packet_id"].(float64))
	versionID := int64(packet["version_id"].(float64))
	taskFolder := fmt.Sprintf("%s/%d", e2eArtifactPlane, taskID)
	renderPath, _ := packet["render_path"].(string)
	evidencePath, _ := packet["verifier_evidence"].(string)
	for name, path := range map[string]string{"report": renderPath, "Verifier evidence": evidencePath} {
		if !strings.HasPrefix(path, taskFolder+"/") {
			t.Fatalf("the %s path %q is outside the task folder %s", name, path, taskFolder)
		}
	}
	hostTaskFolder, err := filepath.EvalSymlinks(filepath.Join(verbs.ProjectArtifactRoot(f.ws), fmt.Sprint(taskID)))
	if err != nil {
		t.Fatalf("resolve the host task folder: %v", err)
	}
	hostReport := filepath.Join(hostTaskFolder, strings.TrimPrefix(renderPath, taskFolder+"/"))
	report, err := os.ReadFile(hostReport)
	if err != nil {
		t.Fatalf("read the host report: %v", err)
	}

	// The artifact files of another task.
	otherTask := taskID + 1
	otherFile := fmt.Sprintf("%d/packet/report.md", otherTask)
	const otherBody = "the report of another task\n"
	if err := os.MkdirAll(filepath.Join(verbs.ProjectArtifactRoot(f.ws), fmt.Sprint(otherTask), "packet"), 0o755); err != nil {
		t.Fatalf("create the folder of another task: %v", err)
	}
	if err := os.WriteFile(filepath.Join(verbs.ProjectArtifactRoot(f.ws), otherFile), []byte(otherBody), 0o644); err != nil {
		t.Fatalf("write the report of another task: %v", err)
	}

	start := f.mcOK("", "collie", "start", "--from", "dashboard:dash-e2e00001",
		"--packet", fmt.Sprint(packetID), "--version", fmt.Sprint(versionID))
	session, _ := start["session_id"].(string)
	if session == "" {
		t.Fatalf("mc collie start --packet returned no session_id: %v", start)
	}
	containerName := "mc-collie-" + session
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
	})

	// A preparation starts the container without a message.
	f.waitFor(90*time.Second, "the packet conversation container runs", func() (bool, string) {
		out, err := exec.Command("docker", "inspect", "--format", "{{.State.Running}}", containerName).CombinedOutput()
		if err != nil {
			note := fmt.Sprintf("docker inspect %s failed: %v: %s", containerName, err, strings.TrimSpace(string(out)))
			if !strings.Contains(strings.ToLower(string(out)), "no such object") {
				return false, outageNote + note
			}
			return false, note
		}
		if strings.TrimSpace(string(out)) != "true" {
			return false, "the container is not running yet"
		}
		return true, ""
	})

	// The create plan holds the read-only task bind and the read-only masks.
	out, err := exec.Command("docker", "inspect", containerName).CombinedOutput()
	if err != nil {
		t.Fatalf("docker inspect %s: %v: %s", containerName, err, out)
	}
	var records []struct {
		Mounts []struct {
			Destination string
			RW          bool
			Source      string
			Type        string
		}
	}
	if err := json.Unmarshal(out, &records); err != nil || len(records) != 1 {
		t.Fatalf("the inspect record of %s is malformed: %v", containerName, err)
	}
	maskDir := filepath.Join(f.home, "runs", "artifact-mask")
	want := map[string]string{
		taskFolder: hostTaskFolder,
		"/workspaces/" + project + "/.mc-artifacts": maskDir,
		"/workspace/source/.mc-artifacts":           maskDir,
	}
	seen := map[string]bool{}
	for _, mount := range records[0].Mounts {
		source, ok := want[mount.Destination]
		if !ok {
			if strings.HasPrefix(mount.Destination, "/mc/artifacts") {
				t.Fatalf("the container holds the unplanned artifact mount %s", mount.Destination)
			}
			continue
		}
		if mount.Type != "bind" || mount.RW || normalizeVMBindSource(mount.Source) != source {
			t.Fatalf("the mount at %s = type %s source %q rw %v, want a read-only bind of %q",
				mount.Destination, mount.Type, mount.Source, mount.RW, source)
		}
		seen[mount.Destination] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("the container holds the mounts %v, want each of %v", seen, want)
	}

	inContainer := func(args ...string) (string, error) {
		out, err := exec.Command("docker", append([]string{"exec", "--user", "10002:10002", containerName}, args...)...).CombinedOutput()
		return string(out), err
	}

	// The container reads the report and the Verifier evidence of its task.
	if got, err := inContainer("cat", renderPath); err != nil || got != string(report) {
		t.Fatalf("the report read = err %v output %q, want %q", err, got, report)
	}
	if got, err := inContainer("cat", evidencePath); err != nil || got == "" {
		t.Fatalf("the Verifier evidence read = err %v output %q", err, got)
	}

	// Each write to the artifact files fails.
	if got, err := inContainer("touch", taskFolder+"/probe"); err == nil ||
		!strings.Contains(strings.ToLower(got), "read-only file system") {
		t.Fatalf("a new file in the task folder = err %v output %q, want a read-only refusal", err, got)
	}
	if got, err := inContainer("sh", "-c", "echo changed >> "+renderPath); err == nil {
		t.Fatalf("a write to the report succeeded: %q", got)
	}
	if body, err := os.ReadFile(hostReport); err != nil || string(body) != string(report) {
		t.Fatalf("the host report changed: err %v body %q", err, body)
	}

	// The artifact files of another task are unreachable.
	for _, path := range []string{
		e2eArtifactPlane + "/" + otherFile,
		"/workspaces/" + project + "/.mc-artifacts/" + otherFile,
		"/workspace/source/.mc-artifacts/" + otherFile,
	} {
		if got, err := inContainer("cat", path); err == nil || strings.Contains(got, otherBody) {
			t.Fatalf("the read of %s = err %v output %q, want a failure", path, err, got)
		}
	}
	for _, root := range []string{"/workspaces/" + project + "/.mc-artifacts", "/workspace/source/.mc-artifacts"} {
		if got, err := inContainer("ls", "-A", root); err != nil || strings.TrimSpace(got) != "" {
			t.Fatalf("the listing of %s = err %v output %q, want an empty folder", root, err, got)
		}
	}

	// The setuid mc of the container reads the report through the task bind.
	command := exec.Command("docker", "exec", "--user", "10002:10002", containerName,
		"mc", "collie", "packet-context", session)
	stdout, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("mc collie packet-context: %v: %s", err, exit.Stderr)
		}
		t.Fatalf("mc collie packet-context: %v", err)
	}
	var delivered struct {
		SessionID string `json:"session_id"`
		PacketID  int64  `json:"packet_id"`
		VersionID int64  `json:"version_id"`
		Context   string `json:"context"`
	}
	if err := json.Unmarshal(stdout, &delivered); err != nil {
		t.Fatalf("mc collie packet-context returned malformed JSON %q: %v", stdout, err)
	}
	if delivered.SessionID != session || delivered.PacketID != packetID || delivered.VersionID != versionID {
		t.Fatalf("the packet context names session %q packet %d version %d, want %q %d %d",
			delivered.SessionID, delivered.PacketID, delivered.VersionID, session, packetID, versionID)
	}
	if !strings.Contains(delivered.Context, strings.TrimSpace(string(report))) {
		t.Fatalf("the packet context holds no report text %q:\n%s", report, delivered.Context)
	}
	for _, line := range []string{"- Report file: " + renderPath, "- Verifier evidence: " + evidencePath} {
		if !strings.Contains(delivered.Context, line+"\n") && !strings.HasSuffix(delivered.Context, line) {
			t.Fatalf("the packet context holds no line %q:\n%s", line, delivered.Context)
		}
	}
	// Each artifact path of the packet context resolves in the container.
	for _, line := range strings.Split(delivered.Context, "\n") {
		for _, label := range []string{"- Report file: ", "- Verifier evidence: ", "- Change data inventory: ", "- Change data patch: "} {
			path, ok := strings.CutPrefix(line, label)
			if !ok {
				continue
			}
			if got, err := inContainer("cat", path); err != nil {
				t.Fatalf("the read of the packet context path %s = err %v output %q", path, err, got)
			}
		}
	}
}
