// Package substrate defines the spine schema and database connections.
package substrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
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

// OpenReadOnly opens an EXISTING spine — or a snapshot of one — for reading
// only: mode=ro plus query_only, never a journal-mode change, never database
// creation. It is the ONE place the read-only DSN is built; every mc read
// verb and every read-only inspection opens through it.
//
// The flags exist for the read-only mount case. A WAL-mode database is
// ordinarily read through its -wal/-shm sidecars, and SQLite creates them
// even for a mode=ro connection when they are absent. Where that creation is
// impossible (spine file and directory both unwritable), the open fails —
// SQLITE_READONLY_DIRECTORY / SQLITE_CANTOPEN — exactly when no writer's
// sidecars happen to exist, and succeeds minutes later when they do: the
// state-dependent asymmetry that intermittently killed `mc activity list`
// inside an agent container while sibling reads worked. When that refusal
// happens and no -wal exists beside the database, the file is one complete
// committed snapshot, so the open retries with immutable=1, which reads
// without sidecars or locks. A database with a live -wal never takes that
// fallback: immutable would ignore the WAL's committed frames and could tear
// against a concurrent checkpoint, so the honest answer there is the original
// refusal.
func OpenReadOnly(path string) (*sql.DB, error) {
	if err := GuardLockDomain(path); err != nil {
		return nil, err
	}
	db, err := openReadOnly(path, false)
	if err == nil {
		return db, nil
	}
	if _, statErr := os.Stat(path + "-wal"); statErr == nil {
		return nil, err
	}
	if db, retryErr := openReadOnly(path, true); retryErr == nil {
		return db, nil
	}
	// The first attempt's error names the real condition; the retry's would
	// only restate it with a flag the caller never chose.
	return nil, err
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
