package routing

import "testing"

func TestScribeRouteUsesTheWorkerDefaultAndAllowsAnOverride(t *testing.T) {
	registry := referenceRegistry(t)
	table, err := Parse([]byte(canonical), registry)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := table.Resolve("worker")
	if err != nil {
		t.Fatal(err)
	}
	scribe, err := table.Resolve("scribe")
	if err != nil || scribe.Harness != worker.Harness || scribe.Binding != worker.Binding {
		t.Fatalf("the Scribe default differs from the normal route: %+v %v", scribe, err)
	}
	next, err := table.WithRoute("scribe", "chatgpt", registry)
	if err != nil {
		t.Fatal(err)
	}
	scribe, err = next.Resolve("scribe")
	if err != nil || scribe.Harness != "codex" {
		t.Fatalf("the Scribe override did not select its harness: %+v %v", scribe, err)
	}
	prior, err := table.Resolve("scribe")
	if err != nil || prior.Binding != worker.Binding {
		t.Fatal("the route update changed the previous assignment")
	}
	parsed, err := Parse([]byte(next.Markdown()), registry)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := parsed.Resolve("scribe")
	if err != nil || stored != scribe {
		t.Fatalf("the Scribe route changed after its save: %+v %v", stored, err)
	}
}
