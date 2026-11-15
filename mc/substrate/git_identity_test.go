package substrate_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestSandboxProfileGitIdentity(t *testing.T) {
	db := openSpine(t)
	mustExec(t, db, `INSERT INTO sandbox_profiles (id, workspace_root) VALUES ('profile', '/tmp/workspace')`)
	assertSandboxProfileGitIdentityMatrix(t, db, "profile")
}

func assertSandboxProfileGitIdentityMatrix(t *testing.T, db *sql.DB, profile string) {
	t.Helper()
	var name, email sql.NullString
	if err := db.QueryRow(`SELECT git_author_name, git_author_email
		FROM sandbox_profiles WHERE id=?`, profile).Scan(&name, &email); err != nil {
		t.Fatal(err)
	}
	if name.Valid || email.Valid {
		t.Fatalf("initial override = (%v, %v), want NULL/NULL inheritance", name, email)
	}
	wantAbort(t, db, `UPDATE sandbox_profiles SET git_author_name='Project Bot' WHERE id=?`, profile)
	mustExec(t, db, `UPDATE sandbox_profiles
		SET git_author_name='Project Bot', git_author_email='bot@example.test' WHERE id=?`, profile)
	for label, pair := range map[string][2]string{
		"blank name":      {"   ", "bot@example.test"},
		"malformed email": {"Project Bot", "not-an-email"},
		"control":         {"Project Bot\nInjected", "bot@example.test"},
		"over-limit":      {strings.Repeat("x", 257), "bot@example.test"},
	} {
		if _, err := db.Exec(`UPDATE sandbox_profiles
			SET git_author_name=?, git_author_email=? WHERE id=?`,
			pair[0], pair[1], profile); err == nil {
			t.Fatalf("%s Git identity pair committed", label)
		}
	}
	mustExec(t, db, `UPDATE sandbox_profiles
		SET git_author_name=NULL, git_author_email=NULL WHERE id=?`, profile)
}
