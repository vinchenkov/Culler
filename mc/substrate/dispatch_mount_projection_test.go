package substrate_test

import (
	"context"
	"testing"

	"mc/substrate"
)

// A control character in one Project id fails the whole projection, and with
// it every dispatch in the deployment. `mc project add` validates this same
// projection inside its own transaction, so this pins the blast radius of
// the rule at its real consumer.
func TestLoadDispatchProjectProjectionRefusesAControlCharacterInAProjectID(t *testing.T) {
	db := openSpine(t)
	mkProject(t, db, "ws-ok")

	got, err := substrate.LoadDispatchProjectProjection(context.Background(), db)
	if err != nil {
		t.Fatalf("a plain Project id must load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("projection holds %d Projects, want 2", len(got))
	}

	mkProject(t, db, "ws\nbad")

	if _, err := substrate.LoadDispatchProjectProjection(context.Background(), db); err == nil {
		t.Fatal("a Project id with a newline loaded; the whole-deployment dispatch hazard is unpinned")
	}
}

// A Project is a Git repository: the schema refuses a Project row without a
// sandbox profile, and every projected Project carries its profile.
func TestLoadDispatchProjectProjectionRequiresAProfile(t *testing.T) {
	db := openSpine(t)
	wantAbort(t, db, `INSERT INTO projects (id, title) VALUES ('bare', 'No profile')`)
	wantAbort(t, db, `INSERT INTO projects (id, title, sandbox_profile) VALUES ('bare', 'No profile', 'missing')`)
	got, err := substrate.LoadDispatchProjectProjection(context.Background(), db)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].ProjectID != "ws" {
		t.Fatalf("projection = %+v, want the fixture Project only", got)
	}
	ws := got[0]
	if !ws.ProfilePresent || ws.ProfileID != "ws-profile" || ws.WorkspaceRoot != "/workspaces/ws" {
		t.Fatalf("the fixture Project projects without its profile: %+v", ws)
	}
}
