package substrate_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"mc/substrate"
)

func TestDispatchMountProjectionExactAggregateBudget(t *testing.T) {
	db := openSpine(t)
	roots := make([]string, 0, 64)
	for i := 0; i < 63; i++ {
		prefix := fmt.Sprintf("/%02d", i)
		roots = append(roots, prefix+strings.Repeat("x", 4096-len(prefix)))
	}
	roots = append(roots, "/z")
	row := substrate.DispatchProject{
		ProjectID: "ws", Status: "active", ProfilePresent: true,
		ProfileID: "profile", WorkspaceRoot: "/tmp/workspace", ArtifactRoots: roots,
		DeniedPaths: []string{},
		// The fixture must use the same default as the stored profile.
		RuntimeAuthDelivery: "projection",
	}
	body, err := json.Marshal([]substrate.DispatchProject{row})
	if err != nil {
		t.Fatal(err)
	}
	delta := substrate.MaxDispatchMountProjectionBytes - len(body)
	if delta < 0 || delta > 4094 {
		t.Fatalf("exact-budget fixture needs delta %d outside 0..4094", delta)
	}
	roots[len(roots)-1] += strings.Repeat("x", delta)
	artifactJSON, err := json.Marshal(roots)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots)
		VALUES ('profile', '/tmp/workspace', ?)`, string(artifactJSON))
	mustExec(t, db, `UPDATE projects SET sandbox_profile='profile' WHERE id='ws'`)
	if err := substrate.ValidateDispatchMountProjection(context.Background(), db); err != nil {
		t.Fatalf("exact aggregate budget rejected: %v", err)
	}

	roots[len(roots)-1] += "x"
	artifactJSON, err = json.Marshal(roots)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE sandbox_profiles SET artifact_roots=? WHERE id='profile'`, string(artifactJSON))
	if err := substrate.ValidateDispatchMountProjection(context.Background(), db); err == nil {
		t.Fatal("aggregate budget + 1 was admitted")
	}
}
