package substrate_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"mc/substrate"
)

func TestCheckSchemaPreservesCurrentData(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO meta (id, deployment_uuid, schema_id) VALUES (1, 'current', ?)`, substrate.CurrentSchemaID)
	mustExec(t, db, `INSERT INTO tasks (title, project) VALUES ('Keep this task', 'ws')`)
	for range 2 {
		if err := substrate.CheckSchema(db); err != nil {
			t.Fatal(err)
		}
	}
	if got := oneStr(t, db, `SELECT title FROM tasks`); got != "Keep this task" {
		t.Fatalf("task title = %q", got)
	}
	if got := oneStr(t, db, `SELECT deployment_uuid FROM meta`); got != "current" {
		t.Fatalf("deployment identity = %q", got)
	}
}

func TestCheckSchemaRejectsIncompatibleStateWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		change string
	}{
		{"different identity", `UPDATE meta SET schema_id = 'different'`},
		{"absent metadata", `DELETE FROM meta`},
		{"absent identity column", `ALTER TABLE meta DROP COLUMN schema_id`},
		{"absent trigger", `DROP TRIGGER activity_append_only_update`},
		{"extra column", `ALTER TABLE tasks ADD COLUMN unexpected TEXT`},
		{"extra table", `CREATE TABLE unexpected (id INTEGER)`},
		{"extra table with similar system prefix", `CREATE TABLE sqliteXextra (id INTEGER)`},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openSpine(t)
			mustExec(t, db, `INSERT INTO meta (id, deployment_uuid, schema_id) VALUES (1, 'current', ?)`, substrate.CurrentSchemaID)
			mustExec(t, db, `INSERT INTO tasks (title, project) VALUES ('Keep this task', 'ws')`)
			mustExec(t, db, test.change)
			before := oneInt(t, db, `PRAGMA schema_version`)
			if err := substrate.CheckSchema(db); !errors.Is(err, substrate.ErrSchemaMismatch) {
				t.Fatalf("schema check = %v", err)
			}
			if got := oneInt(t, db, `PRAGMA schema_version`); got != before {
				t.Fatal("the schema check changed the database schema")
			}
			if got := oneStr(t, db, `SELECT title FROM tasks`); got != "Keep this task" {
				t.Fatalf("task title = %q", got)
			}
		})
	}
}

func TestCheckSchemaRejectsInvalidDispatchRecords(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, *sql.DB)
	}{
		{"tasks.title", func(t *testing.T, db *sql.DB) {
			mustExec(t, db, `INSERT INTO tasks (title, project) VALUES (?, 'ws')`, strings.Repeat("x", 4097))
		}},
		{"artifact_roots", func(t *testing.T, db *sql.DB) {
			mustExec(t, db, `INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots) VALUES ('profile', '/tmp/workspace', ?)`, `["`+strings.Repeat("x", 4097)+`"]`)
			mustExec(t, db, `UPDATE projects SET sandbox_profile = 'profile' WHERE id = 'ws'`)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openSpine(t)
			mustExec(t, db, `INSERT INTO meta (id, deployment_uuid, schema_id) VALUES (1, 'current', ?)`, substrate.CurrentSchemaID)
			test.prepare(t, db)
			if err := substrate.CheckSchema(db); err == nil || !strings.Contains(err.Error(), test.name) {
				t.Fatalf("record check = %v", err)
			}
		})
	}
}

func TestInitFailurePreservesTheDatabase(t *testing.T) {
	db, err := substrate.Open(filepath.Join(t.TempDir(), "spine.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustExec(t, db, `CREATE TABLE tasks (title TEXT)`)
	mustExec(t, db, `INSERT INTO tasks VALUES ('Keep this task')`)
	if err := substrate.Init(db); err == nil {
		t.Fatal("initialization accepted a database with an incompatible table")
	}
	if got := oneInt(t, db, `SELECT count(*) FROM sqlite_schema WHERE type = 'table'`); got != 1 {
		t.Fatalf("failed initialization left %d tables", got)
	}
	if got := oneStr(t, db, `SELECT title FROM tasks`); got != "Keep this task" {
		t.Fatalf("task title = %q", got)
	}
}
