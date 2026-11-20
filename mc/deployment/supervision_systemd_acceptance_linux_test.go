//go:build linux

package deployment

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A03 stage-five acceptance half. True systemd supervision behavior is not
// verified until this test and a03-systemd-acceptance.sh run on the target
// host. The test renders the real bundle and proves the shape invariants on
// that host. With A03_DUMP set it also prints every unit file an installation
// would lay down, under the name the installer uses, between BEGIN/END
// markers on standard output; the acceptance script extracts the bytes from
// the test log and hands them to systemd-analyze verify. Nothing here takes a
// destination path from the environment. The executables in the dump are
// shims because the acceptance fixture carries no release tree; every other
// value is what the renderer produces.

func TestA03RenderedUnitsExport(t *testing.T) {
	// A fixed shared fixture, not t.TempDir: systemd-analyze verify in a later
	// process must still find the shim the ExecStart lines name, and the
	// renderer bakes the home path into those lines. The acceptance script
	// removes the whole fixture directory when it finishes.
	base := filepath.Join(os.TempDir(), "a03-render-fixture")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	home, err := CanonicalHome(filepath.Join(base, "a03 home"))
	if err != nil {
		t.Fatal(err)
	}
	names, err := RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	hostRoot := filepath.Join(home, "release", "host")
	for _, dir := range []string{
		filepath.Join(hostRoot, "resident"),
		filepath.Join(hostRoot, "dashboard"),
		filepath.Join(home, "logs"),
		AgentsViewPathsFor(home).Home,
		filepath.Dir(AgentsViewPathsFor(home).Executable),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(AgentsViewPathsFor(home).Executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := unitRenderInput{
		Result:          SupervisionResultFor(home, names),
		CanonicalHome:   home,
		Names:           names,
		Spec:            SupervisionSpec{SupervisionPolicy: SupervisionPolicy{AgentsViewBind: "127.0.0.1:7334"}},
		MCPath:          "/usr/local/bin/mc",
		BunPath:         "/bin/sh",
		HostRoot:        hostRoot,
		PathEnv:         "/usr/local/bin:/usr/bin:/bin",
		LogRoot:         filepath.Join(home, "logs"),
		AgentsViewToken: strings.Repeat("a", 64),
	}
	files, err := renderUnitFiles(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range SupervisedUnits {
		body, ok := files["units/"+unit+".service"]
		if !ok {
			t.Fatalf("bundle has no units/%s.service: %v", unit, files)
		}
		text := string(body)
		wants := []string{"[Service]", "[Install]", "Restart="}
		if unit != "agentsview" {
			// The agentsview unit carries the data-dir and proxy environment,
			// not MC_HOME; every other unit pins the deployment home.
			wants = append(wants, "Environment=\"MC_HOME=")
		}
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s unit missing %q:\n%s", unit, want, text)
			}
		}
		if os.Getenv("A03_DUMP") == "" {
			continue
		}
		name := UnitLabel(names, unit) + ".service"
		if strings.ContainsAny(name, "/\n\r") {
			t.Fatalf("unit file name is not a flat token: %q", name)
		}
		fmt.Printf("A03-UNIT-BEGIN %s\n%sA03-UNIT-END\n", name, text)
	}
}
