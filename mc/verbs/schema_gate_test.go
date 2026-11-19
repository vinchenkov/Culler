package verbs

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"mc/substrate"
)

// provisionSpine creates a spine file and applies the current schema plus the
// deployment meta row, mirroring the init path without calling Init verbs.
func provisionSpine(t *testing.T, path string) {
	t.Helper()
	db, err := substrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := substrate.Init(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meta (id, deployment_uuid, schema_id) VALUES (1, 'gate-probe', ?)`, substrate.CurrentSchemaID); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSpineGateAcceptsProvisionedSpine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spine.db")
	provisionSpine(t, path)
	db, err := OpenSpine(path)
	if err != nil {
		t.Fatalf("gated open of a matching spine failed: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE meta SET deployment_uuid = 'gate-passed' WHERE id = 1`); err != nil {
		t.Fatalf("write through the gate failed: %v", err)
	}
	if got := readMetaUUID(t, path); got != "gate-passed" {
		t.Fatalf("write through the gate did not persist: %q", got)
	}
}

func readMetaUUID(t *testing.T, path string) string {
	t.Helper()
	db, err := substrate.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(`SELECT deployment_uuid FROM meta WHERE id = 1`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

// The owner's required proof: the gate must reject a spine whose RUNTIME
// structure drifted even though the stored meta.schema_id still equals
// CurrentSchemaID — checking a static file or trusting meta alone would pass.
func TestOpenSpineGateRejectsRuntimeDriftWithHonestMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spine.db")
	provisionSpine(t, path)
	drift, err := substrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drift.Exec(`ALTER TABLE tasks ADD COLUMN gate_probe TEXT`); err != nil {
		t.Fatal(err)
	}
	drift.Close()
	var metaCheck string
	check, err := substrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := check.QueryRow(`SELECT schema_id FROM meta WHERE id = 1`).Scan(&metaCheck); err != nil {
		t.Fatal(err)
	}
	check.Close()
	if metaCheck != substrate.CurrentSchemaID {
		t.Fatal("fixture premise broken: meta was mutated, the drift case must leave meta honest")
	}
	db, err := OpenSpine(path)
	if err == nil {
		db.Close()
		t.Fatal("the gate accepted a runtime schema drift with a matching meta.schema_id")
	}
	if !errors.Is(err, substrate.ErrSchemaMismatch) && !strings.Contains(err.Error(), "schema objects differ") {
		t.Fatalf("gate error = %v, want the schema-objects refusal", err)
	}
}

func TestOpenSpineGateRejectsUnprovisionedAndForeignSpines(t *testing.T) {
	t.Run("absent-meta", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "spine.db")
		db, err := substrate.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := substrate.Init(db); err != nil {
			t.Fatal(err)
		}
		db.Close()
		opened, err := OpenSpine(path)
		if err == nil {
			opened.Close()
			t.Fatal("the gate accepted a spine without deployment metadata")
		}
		if !strings.Contains(err.Error(), "metadata is absent") {
			t.Fatalf("gate error = %v, want the absent-metadata refusal", err)
		}
	})
	t.Run("foreign-schema-id", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "spine.db")
		provisionSpine(t, path)
		foreign, err := substrate.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := foreign.Exec(`UPDATE meta SET schema_id = ?`, strings.Repeat("0", 64)); err != nil {
			t.Fatal(err)
		}
		foreign.Close()
		opened, err := OpenSpine(path)
		if err == nil {
			opened.Close()
			t.Fatal("the gate accepted a foreign schema_id")
		}
		if !strings.Contains(err.Error(), "schema identity differs") {
			t.Fatalf("gate error = %v, want the identity refusal", err)
		}
	})
}

// The gate must reject without touching data: a refused open leaves the spine
// exactly as it was (no statement ran).
func TestOpenSpineGateRefusesWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spine.db")
	provisionSpine(t, path)
	seed, err := substrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`UPDATE meta SET deployment_uuid = 'untouched' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	seed.Close()
	drift, err := substrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drift.Exec(`ALTER TABLE tasks ADD COLUMN gate_probe TEXT`); err != nil {
		t.Fatal(err)
	}
	drift.Close()
	if _, err := OpenSpine(path); err == nil {
		t.Fatal("the gate accepted the drift fixture of the no-mutation case")
	}
	if got := readMetaUUID(t, path); got != "untouched" {
		t.Fatalf("a refused gated open mutated data: %q", got)
	}
}
