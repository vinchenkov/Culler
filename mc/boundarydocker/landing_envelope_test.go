//go:build docker_boundary

// Package boundarydocker holds the boundary suite: the acceptance rows whose
// required evidence is a kernel or runtime fact — `docker inspect` output,
// in-container probes, real filesystem semantics — and which therefore cannot
// be satisfied by a host-side fixture test.
//
// It is gated by the `docker_boundary` build tag, so the ordinary fast lane
// never compiles it.
//
// The distinction this package enforces: an argv assertion proves a bound was
// REQUESTED. Only an inspect or an in-container probe proves it was APPLIED.
package boundarydocker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// probeImage is a minimal image used for pure kernel/filesystem probes. These
// rows are about what the RUNTIME does with a mount table and a uid, which is
// image-independent; the rows that are about the production image's own
// behaviour belong in their own file and must not borrow this one.
const probeImage = "alpine:3.22"

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		t.Skipf("docker not available: %v (%s)", err, strings.TrimSpace(string(out)))
	}
}

// dockerRun runs one throwaway probe container and returns its combined output.
func dockerRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"run", "--rm", "--network", "none"}, args...)
	out, err := exec.Command("docker", full...).CombinedOutput()
	return string(out), err
}

// sharedTempDir is t.TempDir's replacement for fixtures that will be used as
// Docker BIND MOUNTPOINTS.
//
// t.TempDir lives under `/var/folders`, and a directory there that has served
// as a nested bind mountpoint acquires a `com.apple.provenance` xattr from
// Docker Desktop which makes it resist `os.RemoveAll` with EPERM — so the test
// body passes and then the harness fails the test during teardown. Under
// `/private/tmp` the same directory removes cleanly. Verified both ways rather
// than assumed.
func sharedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/private/tmp", "mc-boundary-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("rm", "-rf", dir).Run() })
	return dir
}

// operatorRepo builds a host directory shaped like a real operator Project:
// owned by the invoking (operator) uid, mode 0755, and NOT world-writable —
// which is the whole point, because the merge container runs as 10002 and
// nothing in the design chowns anything.
func operatorRepo(t *testing.T) string {
	t.Helper()
	root := sharedTempDir(t)
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("operator\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// mergeEnvelope writes a file shaped like the envelope the resident binds at
// /mc/repository.json.
func mergeEnvelope(t *testing.T) string {
	t.Helper()
	path := filepath.Join(sharedTempDir(t), "run-7.merge.json")
	if err := os.WriteFile(path, []byte(`{"operation":"apply","task_id":7}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---------------------------------------------------------------------------
// The final-uid VirtioFS canary.
//
// The apply operation is the only real-repository RW grant in the system. If
// uid 10002 cannot write the operator's bind, the merge lane is non-functional
// and every other acceptance row here tests a dead path — which is why this
// canary is first in the file and cheapest to run.
// ---------------------------------------------------------------------------

func TestFinalUidCanWriteTheVirtioFSProjectDockerBoundary(t *testing.T) {
	requireDocker(t)
	repo := operatorRepo(t)

	out, err := dockerRun(t,
		"--user", "10002:10002",
		"-v", repo+":/workspace/source",
		probeImage, "sh", "-c",
		"id -u && touch /workspace/source/CANARY && echo WROTE")
	if err != nil {
		t.Fatalf("uid 10002 could not write the operator-owned VirtioFS bind: %v\n%s\n"+
			"The apply operation is the only real-repository RW grant in the system; a red here means "+
			"the merge lane cannot function on this host at all.", err, out)
	}
	if !strings.Contains(out, "10002") || !strings.Contains(out, "WROTE") {
		t.Fatalf("canary output did not confirm uid and write: %q", out)
	}

	// The file must actually exist on the host — an overlay that swallowed the
	// write would look identical from inside the container.
	canary := filepath.Join(repo, "CANARY")
	fi, err := os.Stat(canary)
	if err != nil {
		t.Fatalf("the container reported a successful write but the host has no file: %v", err)
	}

	// And it must land OWNED BY THE OPERATOR, not by 10002. Nothing in the
	// design chowns a host inode, so this is what makes an apply produce
	// operator-owned bytes.
	if err := assertOwnedByInvoker(t, fi); err != nil {
		t.Fatalf("host ownership of a merge-written file: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The merge container's realized envelope.
//
// The resident's own tests assert the docker ARGV. Argv proves a bound was
// requested; only inspect proves it was applied, and the two can differ
// (an unsupported flag, a daemon-level override, a silently-dropped cap).
// ---------------------------------------------------------------------------

func TestMergeEnvelopeIsAppliedNotJustRequestedDockerBoundary(t *testing.T) {
	requireDocker(t)
	repo := operatorRepo(t)
	envelope := mergeEnvelope(t)
	name := "mc-merge-boundaryprobe01"
	_ = exec.Command("docker", "rm", "-f", name).Run()
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })

	// The apply row as the resident builds it (repository.ts): the hardening,
	// the Project repository RW, the envelope RO.
	create := exec.Command("docker", "create",
		"--name", name,
		"--network", "none",
		"--user", "10002:10002",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges=true",
		"-v", repo+":/workspace/source",
		"-v", envelope+":/mc/repository.json:ro",
		probeImage, "true")
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create merge-shaped container: %v\n%s", err, out)
	}

	for _, tc := range []struct {
		what   string
		format string
		want   string
	}{
		{"network mode", "{{.HostConfig.NetworkMode}}", "none"},
		{"user", "{{.Config.User}}", "10002:10002"},
		{"privileged", "{{.HostConfig.Privileged}}", "false"},
		{"cap drop", "{{.HostConfig.CapDrop}}", "[ALL]"},
	} {
		out, err := exec.Command("docker", "inspect", "-f", tc.format, name).CombinedOutput()
		if err != nil {
			t.Fatalf("inspect %s: %v\n%s", tc.what, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != tc.want {
			t.Fatalf("merge %s = %q, want %q — requested in argv but not applied by the runtime",
				tc.what, got, tc.want)
		}
	}

	// No Docker socket may be bound into a merge container under any circumstance.
	out, err := exec.Command("docker", "inspect", "-f", "{{range .Mounts}}{{.Source}} {{end}}", name).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect mounts: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "docker.sock") {
		t.Fatalf("a merge container has the Docker socket bound: %s", out)
	}

	// no-new-privileges must be present on the merge class specifically. The
	// setuid gate requires the pipeline class to be free of it, so the two
	// classes must never share one envelope builder.
	out, err = exec.Command("docker", "inspect", "-f", "{{.HostConfig.SecurityOpt}}", name).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect security opts: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "no-new-privileges") {
		t.Fatalf("merge security opts = %q, want no-new-privileges", strings.TrimSpace(string(out)))
	}
}

// The `none` network mode's negative probe: a merge container must not be able
// to reach the network at all. It is the only egress mode the merge class ever
// uses.
func TestMergeHasNoNetworkDockerBoundary(t *testing.T) {
	requireDocker(t)
	out, err := dockerRun(t,
		"--user", "10002:10002",
		probeImage, "sh", "-c",
		"ip -o addr show 2>/dev/null | grep -v ' lo ' | wc -l")
	if err != nil {
		t.Fatalf("network probe: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(out); got != "0" {
		t.Fatalf("a --network none merge container has %s non-loopback interfaces:\n%s", got, out)
	}
}

// ---------------------------------------------------------------------------
// The realized merge mount table.
//
// The apply container mounts exactly two rows: the Project repository RW and
// the envelope RO. The prepare container mounts the isolated run root RW in
// place of the Project repository.
//
// The resident's own tests compare the docker ARGV to a literal. That proves
// the table was requested. This proves it was APPLIED, and — the part argv can
// never show — that the ACCESS MODES mean what they say to a process inside the
// container. A bind requested `:ro` that the runtime silently mounted RW would
// look identical in every host-side test in the tree.
//
// The count assertion is as load-bearing as the contents. The table is a
// ceiling, not a floor: a third bind is a boundary violation even if both
// required rows are present and correct.
// ---------------------------------------------------------------------------

func TestMergeMountTableMatchesDockerInspectDockerBoundary(t *testing.T) {
	requireDocker(t)
	repo := operatorRepo(t)
	envelope := mergeEnvelope(t)

	name := "mc-merge-boundarytable01"
	_ = exec.Command("docker", "rm", "-f", name).Run()
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })

	// The exact two rows the resident composes for apply.
	create := exec.Command("docker", "create", "--name", name,
		"--network", "none", "--user", "10002:10002",
		"-v", repo+":/workspace/source",
		"-v", envelope+":/mc/repository.json:ro",
		probeImage, "true")
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}

	out, err := exec.Command("docker", "inspect", "-f",
		"{{range .Mounts}}{{.Destination}}={{.RW}} {{end}}", name).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect mounts: %v\n%s", err, out)
	}
	realized := strings.Fields(strings.TrimSpace(string(out)))

	want := map[string]string{
		"/workspace/source":   "true",  // the only real-repository RW grant in the system
		"/mc/repository.json": "false", // the envelope the operation reads
	}
	if len(realized) != len(want) {
		t.Fatalf("merge realized %d mounts, want exactly %d — the table is a ceiling, "+
			"not a floor:\n%v", len(realized), len(want), realized)
	}
	for _, entry := range realized {
		dest, rw, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("unparsable mount entry %q", entry)
		}
		wantRW, known := want[dest]
		if !known {
			t.Fatalf("merge carries an unexpected mount at %q", dest)
		}
		if rw != wantRW {
			t.Fatalf("merge mount %s is RW=%s, want RW=%s", dest, rw, wantRW)
		}
		delete(want, dest)
	}
	if len(want) != 0 {
		t.Fatalf("merge is missing required mounts: %v", want)
	}
}

// What inspect cannot show: whether the declared modes actually govern a
// process. The envelope must refuse a write from inside, and the one RW row
// must accept one.
func TestMergeMountModesGovernInsideTheContainerDockerBoundary(t *testing.T) {
	requireDocker(t)
	repo := operatorRepo(t)
	envelope := mergeEnvelope(t)

	out, err := dockerRun(t,
		"--user", "10002:10002",
		"-v", repo+":/workspace/source",
		"-v", envelope+":/mc/repository.json:ro",
		probeImage, "sh", "-c",
		`touch /workspace/source/w 2>/dev/null && echo SOURCE_RW || echo SOURCE_RO;`+
			`echo x >> /mc/repository.json 2>/dev/null && echo ENVELOPE_RW || echo ENVELOPE_RO;`+
			`cat /mc/repository.json > /dev/null && echo ENVELOPE_READABLE`)
	if err != nil {
		t.Fatalf("mode probe: %v\n%s", err, out)
	}

	for _, want := range []string{"SOURCE_RW", "ENVELOPE_RO", "ENVELOPE_READABLE"} {
		if !strings.Contains(out, want) {
			t.Fatalf("merge mount modes do not govern inside the container: missing %s\n%s", want, out)
		}
	}
	// The envelope names the operation and its inputs. A writable one would let
	// the operation rewrite what it was asked to do.
	if strings.Contains(out, "ENVELOPE_RW") {
		t.Fatalf("the envelope is WRITABLE inside the merge container:\n%s", out)
	}
}
