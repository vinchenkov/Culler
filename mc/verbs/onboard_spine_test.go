package verbs

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mc/substrate"
)

func absentMirrorRequest() OnboardSpineRequest {
	return OnboardSpineRequest{
		ProtocolVersion: 1,
		SchemaID:        substrate.CurrentSchemaID,
		MirrorState:     "absent",
	}
}

func TestOnboardSpinePrivateStateMatrix(t *testing.T) {
	t.Run("empty volume and absent mirror initialize exactly once", func(t *testing.T) {
		spine := filepath.Join(t.TempDir(), "spine.db")
		got, err := OnboardSpine(spine, absentMirrorRequest())
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "initialized" || got.DeploymentUUID == "" || got.SchemaID != substrate.CurrentSchemaID {
			t.Fatalf("result = %#v", got)
		}
		inspection, err := inspectSpineReadOnly(spine)
		if err != nil {
			t.Fatal(err)
		}
		if !inspection.hasMeta || inspection.uuid != got.DeploymentUUID {
			t.Fatalf("inspection = %#v, result = %#v", inspection, got)
		}
	})

	t.Run("committed meta with absent mirror repairs only the host mirror", func(t *testing.T) {
		spine := filepath.Join(t.TempDir(), "spine.db")
		first, err := OnboardSpine(spine, absentMirrorRequest())
		if err != nil {
			t.Fatal(err)
		}
		got, err := OnboardSpine(spine, absentMirrorRequest())
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "repair-mirror" || got.DeploymentUUID != first.DeploymentUUID {
			t.Fatalf("repair = %#v, first = %#v", got, first)
		}
	})

	t.Run("matching mirror is idempotent and mismatch refuses", func(t *testing.T) {
		spine := filepath.Join(t.TempDir(), "spine.db")
		first, err := OnboardSpine(spine, absentMirrorRequest())
		if err != nil {
			t.Fatal(err)
		}
		match := absentMirrorRequest()
		match.MirrorState = "present"
		match.MirrorUUID = first.DeploymentUUID
		got, err := OnboardSpine(spine, match)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "ok" {
			t.Fatalf("matching replay = %#v", got)
		}
		match.MirrorUUID = strings.Repeat("f", 32)
		if _, err := OnboardSpine(spine, match); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
			t.Fatalf("mismatch error = %v", err)
		}
	})

	t.Run("existing mirror plus empty volume is spine loss", func(t *testing.T) {
		spine := filepath.Join(t.TempDir(), "spine.db")
		req := absentMirrorRequest()
		req.MirrorState = "present"
		req.MirrorUUID = strings.Repeat("a", 32)
		_, err := OnboardSpine(spine, req)
		var rejected *DomainError
		if !errors.As(err, &rejected) || rejected.Code != "database-absent" || !strings.Contains(err.Error(), "spine lost") {
			t.Fatalf("spine-loss error = %v", err)
		}
		if _, err := os.Stat(spine); !os.IsNotExist(err) {
			t.Fatalf("spine loss created a database: %v", err)
		}
	})

	t.Run("non-empty volume without meta is never adopted", func(t *testing.T) {
		spine := filepath.Join(t.TempDir(), "spine.db")
		db, err := sql.Open("sqlite", spine)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE foreign_table (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := OnboardSpine(spine, absentMirrorRequest()); err == nil || !strings.Contains(err.Error(), "no meta identity") {
			t.Fatalf("foreign-spine error = %v", err)
		}
	})

}

func TestOnboardSpineFrameCarriesNoPathOrConfigField(t *testing.T) {
	b, err := json.Marshal(absentMirrorRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"path", "home", "config", "credential", "routing", "project"} {
		if strings.Contains(strings.ToLower(string(b)), forbidden) {
			t.Fatalf("private frame carries %q: %s", forbidden, b)
		}
	}
}

func TestProductionHomeHostHalfPublishesOnlyReturnedMirror(t *testing.T) {
	home := filepath.Join(t.TempDir(), "mc-home")
	t.Setenv("MC_HOME", home)
	req, err := PrepareOnboardHome(true)
	if err != nil {
		t.Fatal(err)
	}
	if req.MirrorState != "absent" || req.MirrorUUID != "" {
		t.Fatalf("fresh request = %#v", req)
	}
	uuid := strings.Repeat("a", 32)
	status, _, err := FinalizeOnboardHome(OnboardSpineResult{
		ProtocolVersion: 1, Status: "initialized", DeploymentUUID: uuid,
		SchemaID: substrate.CurrentSchemaID,
	})
	if err != nil || status != "done" {
		t.Fatalf("finalize status=%q err=%v", status, err)
	}
	b, err := os.ReadFile(filepath.Join(home, deploymentUUIDFilename))
	if err != nil || string(b) != uuid+"\n" {
		t.Fatalf("mirror = %q err=%v", b, err)
	}
	if _, err := os.Stat(filepath.Join(home, "spine.db")); !os.IsNotExist(err) {
		t.Fatalf("host half created/opened a spine: %v", err)
	}
	req, err = PrepareOnboardHome(true)
	if err != nil || req.MirrorState != "present" || req.MirrorUUID != uuid {
		t.Fatalf("repeat request = %#v err=%v", req, err)
	}
}

func TestProductionHomeHostHalfRejectsUntrustedHelperResponse(t *testing.T) {
	t.Setenv("MC_HOME", filepath.Join(t.TempDir(), "mc-home"))
	base := OnboardSpineResult{
		ProtocolVersion: 1, Status: "initialized", DeploymentUUID: strings.Repeat("a", 32),
		SchemaID: substrate.CurrentSchemaID,
	}
	bad := base
	bad.DeploymentUUID = "not-a-uuid"
	if _, _, err := FinalizeOnboardHome(bad); err == nil {
		t.Fatal("invalid helper UUID was accepted")
	}
	bad = base
	bad.Status = "invented"
	if _, _, err := FinalizeOnboardHome(bad); err == nil {
		t.Fatal("invalid helper status was accepted")
	}
	if _, err := os.Stat(os.Getenv("MC_HOME")); !os.IsNotExist(err) {
		t.Fatalf("rejected helper response wrote MC_HOME: %v", err)
	}
}

func TestOnboardSpineResetsOnlyARequestedMatchingDeployment(t *testing.T) {
	spine := filepath.Join(t.TempDir(), "spine.db")
	first, err := OnboardSpine(spine, absentMirrorRequest())
	if err != nil {
		t.Fatal(err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO activity (actor, kind, detail) VALUES ('operator', 'task.added', '{}')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	req := absentMirrorRequest()
	req.MirrorState, req.MirrorUUID = "present", first.DeploymentUUID
	got, err := OnboardSpine(spine, req)
	if err != nil || got.Status != "ok" {
		t.Fatalf("current database: %+v %v", got, err)
	}
	db, err = substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM activity`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("current records: %d %v", rows, err)
	}
	db.Close()
	permitted := req
	permitted.AllowReset = true
	if got, err := OnboardSpine(spine, permitted); err != nil || got.Status != "ok" {
		t.Fatalf("permitted reset of a current database: %+v %v", got, err)
	}
	forced := req
	forced.Reset = true
	forced.MirrorState, forced.MirrorUUID = "absent", ""
	if _, err := OnboardSpine(spine, forced); err == nil {
		t.Fatal("a reset without the identity mirror replaced the database")
	}
	forced.MirrorState, forced.MirrorUUID = "present", first.DeploymentUUID
	got, err = OnboardSpine(spine, forced)
	if err != nil || got.Status != "reset" || got.DeploymentUUID != first.DeploymentUUID {
		t.Fatalf("reset of a current database: %+v %v", got, err)
	}
	db, err = substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM activity`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("records after the reset of a current database: %d %v", rows, err)
	}
	if _, err := db.Exec(`INSERT INTO activity (actor, kind, detail) VALUES ('operator', 'task.added', '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE meta RENAME COLUMN schema_id TO incompatible_id`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(spine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OnboardSpine(spine, req); err == nil {
		t.Fatal("ordinary request reset the database")
	} else {
		var rejected *DomainError
		if !errors.As(err, &rejected) || rejected.Code != "database-schema-differs" || !strings.Contains(err.Error(), "database schema differs") {
			t.Fatalf("schema difference lost its code: %v", err)
		}
	}
	if after, err := os.ReadFile(spine); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("the refused request changed the database: %v", err)
	}
	req.AllowReset = true
	req.MirrorUUID = strings.Repeat("f", 32)
	if _, err := OnboardSpine(spine, req); err == nil {
		t.Fatal("foreign mirror reset the database")
	}
	req.MirrorState, req.MirrorUUID = "absent", ""
	if _, err := OnboardSpine(spine, req); err == nil {
		t.Fatal("absent mirror authorized a reset")
	}
	req.MirrorState, req.MirrorUUID = "present", first.DeploymentUUID
	got, err = OnboardSpine(spine, req)
	if err != nil || got.Status != "reset" || got.DeploymentUUID != first.DeploymentUUID {
		t.Fatalf("reset: %+v %v", got, err)
	}
	db, err = substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := substrate.CheckSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM activity`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("reset records: %d %v", rows, err)
	}
}

func TestOnboardSpinePrivateDeploymentRecreatesMissingDatabase(t *testing.T) {
	req := absentMirrorRequest()
	req.AllowReset = true
	req.MirrorState, req.MirrorUUID = "present", strings.Repeat("a", 32)
	for _, emptyFile := range []bool{false, true} {
		spine := filepath.Join(t.TempDir(), "spine.db")
		if emptyFile {
			if err := os.WriteFile(spine, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := os.WriteFile(spine+suffix, []byte("discarded state"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got, err := OnboardSpine(spine, req)
		if err != nil || got.DeploymentUUID != req.MirrorUUID {
			t.Fatalf("recreate: %+v %v", got, err)
		}
	}
}

func TestOnboardSpineRejectsMalformedMirrorBeforeDatabaseCreation(t *testing.T) {
	for _, uuid := range []string{"", "not-a-uuid", strings.Repeat("A", 32), strings.Repeat("x", 32)} {
		req := absentMirrorRequest()
		req.AllowReset = true
		req.MirrorState, req.MirrorUUID = "present", uuid
		spine := filepath.Join(t.TempDir(), "spine.db")
		if _, err := OnboardSpine(spine, req); err == nil {
			t.Fatalf("invalid mirror accepted: %q", uuid)
		}
		if _, err := os.Stat(spine); !os.IsNotExist(err) {
			t.Fatalf("invalid mirror created a database: %v", err)
		}
	}
}

func TestOnboardSpineConcurrentResetProcess(t *testing.T) {
	frame := os.Getenv("MC_ONBOARD_RESET_TEST")
	if frame == "" {
		return
	}
	var input struct {
		Spine   string
		Request OnboardSpineRequest
	}
	if err := json.Unmarshal([]byte(frame), &input); err != nil {
		t.Fatal(err)
	}
	result, err := OnboardSpine(input.Spine, input.Request)
	if err != nil {
		t.Fatal(err)
	}
	db, err := substrate.Open(input.Spine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO activity (actor, kind, detail) VALUES ('operator', 'task.added', ?)`, fmt.Sprint(os.Getpid())); err != nil {
		t.Fatal(err)
	}
	db.Close()
	fmt.Println(result.Status)
	os.Exit(0)
}

func TestOnboardSpineConcurrentPrivateResetPreservesNewRecords(t *testing.T) {
	spine := filepath.Join(t.TempDir(), "spine.db")
	first, err := OnboardSpine(spine, absentMirrorRequest())
	if err != nil {
		t.Fatal(err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE incompatible_state (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	req := absentMirrorRequest()
	req.AllowReset = true
	req.MirrorState, req.MirrorUUID = "present", first.DeploymentUUID
	frame, err := json.Marshal(struct {
		Spine   string
		Request OnboardSpineRequest
	}{spine, req})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockOnboardSpine(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	const callers = 6
	processes := make([]*exec.Cmd, callers)
	outputs := make([]bytes.Buffer, callers)
	for i := range processes {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOnboardSpineConcurrentResetProcess$")
		cmd.Env = append(os.Environ(), "MC_ONBOARD_RESET_TEST="+string(frame))
		cmd.Stdout, cmd.Stderr = &outputs[i], &outputs[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		processes[i] = cmd
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	resets := 0
	for i, cmd := range processes {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("caller %d: %v %s", i, err, outputs[i].String())
		}
		if strings.TrimSpace(outputs[i].String()) == "reset" {
			resets++
		}
	}
	if resets != 1 {
		t.Fatalf("reset count = %d, want 1", resets)
	}
	db, err = substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := substrate.CheckSchema(db); err != nil {
		t.Fatal(err)
	}
	var uuid string
	var records int
	if err := db.QueryRow(`SELECT deployment_uuid FROM meta WHERE id = 1`).Scan(&uuid); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM activity`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if uuid != first.DeploymentUUID || records != callers {
		t.Fatalf("database uuid=%q records=%d", uuid, records)
	}
}

func TestOnboardSpineRejectsUnsafeLockFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "hardlink", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			spine := filepath.Join(root, "spine.db")
			lockPath := spine + ".onboard.lock"
			target := filepath.Join(root, "protected")
			if err := os.WriteFile(target, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(target, lockPath)
			case "directory":
				err = os.Mkdir(lockPath, 0o700)
			case "hardlink":
				err = os.Link(target, lockPath)
			case "permissions":
				err = os.WriteFile(lockPath, nil, 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := OnboardSpine(spine, absentMirrorRequest()); err == nil {
				t.Fatal("unsafe lock accepted")
			}
			if _, err := os.Stat(spine); !os.IsNotExist(err) {
				t.Fatalf("unsafe lock created a database: %v", err)
			}
			content, err := os.ReadFile(target)
			if err != nil || string(content) != "preserve" {
				t.Fatalf("protected target changed: %q %v", content, err)
			}
		})
	}
}

// The production reset replaces only the database files. The host files stay,
// and the tunables section restores the settings from config.toml.
func TestOnboardSpineResetKeepsHostFilesAndConfiguredSettings(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "mc-home")
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)
	if _, _, err := onboardHome(spine, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardTunables(OnboardArgs{Spine: spine, ReviewWIPCap: 7, TimeoutMinutes: 90}); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(home, "runtime-auth", "grants.json"):             "credential",
		filepath.Join(home, "runs", "repositories", "r1", "repo", "f"): "run worktree",
		filepath.Join(root, "project", "README.md"):                    "Project repository",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO activity (actor, kind, detail) VALUES ('operator', 'task.added', '{}')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	hostFiles := func() map[string]string {
		files := map[string]string{}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || strings.HasPrefix(path, spine) {
				return err
			}
			content, err := os.ReadFile(path)
			files[path] = string(content)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := hostFiles()
	if before[filepath.Join(home, "config.toml")] == "" || before[filepath.Join(home, deploymentUUIDFilename)] == "" {
		t.Fatalf("fixture lacks config.toml or the identity mirror: %v", before)
	}

	req, err := PrepareOnboardHome(false)
	if err != nil {
		t.Fatal(err)
	}
	req.Reset = true
	result, err := OnboardSpine(spine, req)
	if err != nil || result.Status != "reset" || result.DeploymentUUID != req.MirrorUUID {
		t.Fatalf("reset = %+v %v", result, err)
	}
	if _, _, err := FinalizeOnboardHome(result); err != nil {
		t.Fatal(err)
	}
	after := hostFiles()
	if len(after) != len(before) {
		t.Fatalf("host files changed: before %d after %d", len(before), len(after))
	}
	for path, content := range before {
		if after[path] != content {
			t.Fatalf("the reset changed %s", path)
		}
	}
	settings := func() (int, int) {
		db, err := substrate.Open(spine)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var records, reviewCap, timeout int
		if err := db.QueryRow(`SELECT (SELECT count(*) FROM activity), review_wip_cap, timeout_minutes FROM settings WHERE id = 1`).Scan(&records, &reviewCap, &timeout); err != nil {
			t.Fatal(err)
		}
		if records != 0 {
			t.Fatalf("records after the reset = %d", records)
		}
		return reviewCap, timeout
	}
	if reviewCap, timeout := settings(); reviewCap == 7 || timeout == 90 {
		t.Fatalf("the empty database kept the configured settings: %d %d", reviewCap, timeout)
	}
	if _, _, err := onboardTunables(OnboardArgs{Spine: spine}); err != nil {
		t.Fatal(err)
	}
	if reviewCap, timeout := settings(); reviewCap != 7 || timeout != 90 {
		t.Fatalf("settings from config.toml = %d %d, want 7 90", reviewCap, timeout)
	}
}

func TestInspectDeployedDatabaseComparesTheSchemaWithoutAWrite(t *testing.T) {
	spine := filepath.Join(t.TempDir(), "spine.db")
	absent, err := InspectDeployedDatabase(spine)
	if err != nil || absent.State != "absent" || absent.SchemaID != substrate.CurrentSchemaID || absent.ProtocolVersion != 1 {
		t.Fatalf("absent database: %+v %v", absent, err)
	}
	if _, err := os.Stat(spine); !os.IsNotExist(err) {
		t.Fatalf("the inspection of an absent database created a file: %v", err)
	}
	first, err := OnboardSpine(spine, absentMirrorRequest())
	if err != nil {
		t.Fatal(err)
	}
	current, err := InspectDeployedDatabase(spine)
	if err != nil || current.State != "current" || current.DeploymentUUID != first.DeploymentUUID {
		t.Fatalf("current database: %+v %v", current, err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE meta RENAME COLUMN schema_id TO incompatible_id`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := os.Remove(spine + ".onboard.lock"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(spine)
	if err != nil {
		t.Fatal(err)
	}
	differs, err := InspectDeployedDatabase(spine)
	if err != nil || differs.State != "differs" || differs.DeploymentUUID != first.DeploymentUUID {
		t.Fatalf("different schema: %+v %v", differs, err)
	}
	after, err := os.ReadFile(spine)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the inspection changed the database: %v", err)
	}
	if _, err := os.Stat(spine + ".onboard.lock"); err == nil {
		t.Fatal("the inspection took the onboard lock")
	}
}
