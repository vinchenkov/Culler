package deployment

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServiceStopsRemainSeparateForEachDeployment(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	if err := WriteServiceStops(one, ServiceStops{"resident": true}); err != nil {
		t.Fatal(err)
	}
	stops, err := ReadServiceStops(one)
	if err != nil || !stops["resident"] {
		t.Fatalf("%v %v", stops, err)
	}
	stops, err = ReadServiceStops(two)
	if err != nil || len(stops) != 0 {
		t.Fatal("stop choice crossed deployments")
	}
	path := filepath.Join(one, ServiceStopsRecord)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadServiceStops(one); err == nil {
		t.Fatal("unsafe choice record accepted")
	}
}

func TestActivationAndInspectionPreserveExplicitServiceStops(t *testing.T) {
	for _, stopped := range []ServiceStops{{"resident": true}, {"dashboard": true}, {"agentsview": true}, {"resident": true, "dashboard": true, "agentsview": true}} {
		home, launchAgents, release, launchd, deps, _ := activationFixture(t)
		if err := WriteServiceStops(home, stopped); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ActivateSupervision(home, launchAgents, release, deps); err != nil {
			t.Fatal(err)
		}
		names, _ := RuntimeNames(home)
		for _, unit := range SupervisedUnits {
			if launchd.loaded[UnitLabel(names, unit)] == stopped[unit] {
				t.Fatalf("%s changed its stop choice", unit)
			}
			if err := InspectServiceDefinition(home, launchAgents, unit, release); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := InspectActiveSupervision(home, launchAgents, release, launchd, deps.Now()); err != nil {
			t.Fatal(err)
		}
	}
}
