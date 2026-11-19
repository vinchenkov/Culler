// Package substrate defines the spine schema and database connections.
package substrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// Schema is the full spine DDL: tables, indexes, CHECK constraints, and the
// trigger lattice.
//
//go:embed schema.sql
var Schema string

//go:embed delivery.sql
var deliverySchema string

//go:embed environments.sql
var environmentSchema string

// CurrentSchemaID identifies the complete current schema with a SHA-256 digest.
var CurrentSchemaID string

func init() {
	Schema += "\n" + deliverySchema + "\n" + environmentSchema
	CurrentSchemaID = fmt.Sprintf("%x", sha256.Sum256([]byte(Schema)))
}

// Open opens (creating if absent) a spine database at path with the
// connection discipline: WAL journal mode, a busy timeout, and foreign keys
// on — applied via DSN pragmas so every pooled connection carries them.
//
// The spine must live on a real file in the lock domain; WAL does
// not work on ":memory:" databases, and the tests deliberately use a temp
// file for that reason.
func Open(path string) (*sql.DB, error) {
	if err := GuardLockDomain(path); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?%s", path, strings.Join([]string{
		"_pragma=busy_timeout(5000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=foreign_keys(1)",
	}, "&"))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open spine %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open spine %s: %w", path, err)
	}
	return db, nil
}

// OpenReadOnly opens the LIVE spine for reading only: mode=ro plus
// query_only, never a journal-mode change, never database creation. It is the
// one ACTIVE read entry; every mc read verb and live inspection opens here.
//
// A WAL-mode database is read through its -wal/-shm sidecars, and SQLite
// creates them for a mode=ro connection when the directory is writable.
// Where creation is impossible (file and directory both unwritable, as on a
// read-only container mount), the open FAILS with the honest driver error.
// It never degrades to an immutable read: "no -wal right now" does not prove
// the database stays unwritten during the read, and an immutable handle over
// a live database ignores WAL-committed frames and can tear against a
// concurrent checkpoint. Container read availability on read-only mounts is
// a mount-layout obligation, not a driver flag.
//
// Reading a completed snapshot copy is a different operation and must be
// named by the caller: see OpenSnapshotReadOnly.
func OpenReadOnly(path string) (*sql.DB, error) {
	if err := GuardLockDomain(path); err != nil {
		return nil, err
	}
	return openReadOnly(path, false)
}

// OpenSnapshotReadOnly reads a file the CALLER has established as a
// completed, stable snapshot (a copied backup or staged restore file):
// immutable=1 reads without sidecars or locks. The caller owns the
// precondition — snapshot generation finished, and no writer touches the
// file or its sidecars for the read's lifetime. A live spine routed through
// here can silently miss committed frames, so this entry stays explicit:
// no fallback ever selects it.
func OpenSnapshotReadOnly(path string) (*sql.DB, error) {
	if err := GuardLockDomain(path); err != nil {
		return nil, err
	}
	return openReadOnly(path, true)
}

// openReadOnly builds the read-only DSN — the single builder — and proves the
// connection with a ping, so a spine this process cannot read fails at open
// rather than on the verb's first query.
func openReadOnly(path string, immutable bool) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro"
	if immutable {
		dsn += "&immutable=1"
	}
	dsn += "&" + strings.Join([]string{
		"_pragma=query_only(1)",
		"_pragma=busy_timeout(5000)",
	}, "&")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open spine read-only %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open spine read-only %s: %w", path, err)
	}
	return db, nil
}

// Init creates the current schema in an empty database.
func Init(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin spine initialization: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(Schema); err != nil {
		return fmt.Errorf("apply spine schema: %w", err)
	}
	return tx.Commit()
}

var currentSchemaObjects = sync.OnceValues(func() (string, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := Init(db); err != nil {
		return "", err
	}
	return schemaObjects(db)
})

type schemaQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
}

func schemaObjects(db schemaQuerier) (string, error) {
	rows, err := db.Query(`SELECT type, name, tbl_name, sql FROM sqlite_schema
		WHERE name NOT GLOB 'sqlite_*' AND sql IS NOT NULL ORDER BY type, name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	hash := sha256.New()
	for rows.Next() {
		var kind, name, table, definition string
		if err := rows.Scan(&kind, &name, &table, &definition); err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%q %q %q %q\n", kind, name, table, definition)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// ErrSchemaMismatch identifies a database that requires a schema conversion or reset.
var ErrSchemaMismatch = errors.New("spine schema differs from the current schema")

// CheckSchema checks the current identity, schema objects, and dispatch records.
// It changes no database state.
func CheckSchema(db *sql.DB) error {
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("check spine schema: %w", err)
	}
	defer tx.Rollback()
	want, err := currentSchemaObjects()
	if err != nil {
		return fmt.Errorf("read current schema objects: %w", err)
	}
	got, err := schemaObjects(tx)
	if err != nil {
		return fmt.Errorf("read spine schema objects: %w", err)
	}
	if got != want {
		return fmt.Errorf("%w: schema objects differ", ErrSchemaMismatch)
	}
	var id string
	if err := tx.QueryRow(`SELECT schema_id FROM meta WHERE id = 1`).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: deployment metadata is absent", ErrSchemaMismatch)
		}
		return fmt.Errorf("read spine schema identity: %w", err)
	}
	if id != CurrentSchemaID {
		return fmt.Errorf("%w: schema identity differs", ErrSchemaMismatch)
	}
	return validateDispatchScalarAdmission(tx)
}

func validateDispatchScalarAdmission(tx *sql.Tx) error {
	checks := []struct {
		name  string
		query string
	}{
		{"meta.deployment_uuid", `SELECT typeof(deployment_uuid), CAST(deployment_uuid AS TEXT) FROM meta WHERE id = 1`},
	}
	for _, check := range checks {
		var storage, value string
		if err := tx.QueryRow(check.query).Scan(&storage, &value); err != nil {
			return fmt.Errorf("validate %s admission: %w", check.name, err)
		}
		if storage != "text" || !validDispatchScalar(value) {
			return fmt.Errorf("%s is not a valid dispatch scalar", check.name)
		}
	}
	rows, err := tx.Query(`SELECT id, typeof(title), CAST(title AS TEXT) FROM tasks`)
	if err != nil {
		return fmt.Errorf("validate tasks.title admission: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var storage, value string
		if err := rows.Scan(&id, &storage, &value); err != nil {
			return fmt.Errorf("validate tasks.title admission: %w", err)
		}
		if storage != "text" || !validDispatchScalar(value) {
			return fmt.Errorf("tasks.title for task %d is not a valid dispatch scalar", id)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("validate tasks.title admission: %w", err)
	}
	if err := ValidateDispatchMountProjection(context.Background(), tx); err != nil {
		return err
	}
	return nil
}

func validDispatchScalar(value string) bool {
	if value == "" || len(value) > 4*1024 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
