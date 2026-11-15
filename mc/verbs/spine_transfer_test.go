package verbs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/substrate"
)

const transferTestUUID = "0123456789abcdef0123456789abcdef"

func transferFixture(t *testing.T) (string, string, SpineTransferRequest) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, deploymentUUIDFilename), []byte(transferTestUUID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Retention is operator config now, so a deployment fixture must carry one:
	// a small value keeps the retention case from publishing 51 snapshots.
	retaining := operatorconfig.Default()
	retaining.Timing.BackupRetain = 5
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), retaining); err != nil {
		t.Fatal(err)
	}
	spineRoot := filepath.Join(root, "volume")
	if err := os.Mkdir(spineRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	spine := filepath.Join(spineRoot, "spine.db")
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(substrate.Schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meta (id, deployment_uuid, schema_id) VALUES (1, ?, ?)`, transferTestUUID, substrate.CurrentSchemaID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO activity (id, actor, kind, detail) VALUES (7, 'operator', 'task.added', 'restore sentinel')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	req := SpineTransferRequest{
		ProtocolVersion: spineTransferProtocol, ReleaseBuildID: "release-test",
		ControlVersion: 1, SpineSchemaID: substrate.CurrentSchemaID,
		ConfigSchemaVersion: 1, DeploymentUUID: transferTestUUID,
		// Retention is host authority: the mountless helper has no config.toml,
		// so it echoes what the request carries instead of choosing.
		BackupRetain: 5,
	}
	return home, spine, req
}

func exportTransfer(t *testing.T, spine string, req SpineTransferRequest) []byte {
	t.Helper()
	var stream bytes.Buffer
	if err := ExportSpineBackup(spine, req, req.ReleaseBuildID, req.ControlVersion, req.ConfigSchemaVersion, &stream); err != nil {
		t.Fatal(err)
	}
	return stream.Bytes()
}

func TestPrepareSpineTransferBindsOwnerOnlyHomeAndMirror(t *testing.T) {
	home, _, want := transferFixture(t)
	t.Setenv("MC_HOME", home)
	got, gotHome, err := PrepareSpineTransfer(want.ReleaseBuildID, want.ControlVersion, want.ConfigSchemaVersion)
	if err != nil || got != want || gotHome != home {
		t.Fatalf("prepared request=%+v home=%q err=%v", got, gotHome, err)
	}
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareSpineTransfer(want.ReleaseBuildID, want.ControlVersion, want.ConfigSchemaVersion); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("widened MC_HOME accepted: %v", err)
	}
}

// The transfer keeps the last backup_retain snapshots, and the operator owns
// that number. It was a compiled 48 for as long as nothing read config.toml
// here, so a retuned retention pruned nothing differently.
func TestPrepareSpineTransferCarriesTheConfiguredBackupRetention(t *testing.T) {
	home, _, _ := transferFixture(t)
	t.Setenv("MC_HOME", home)
	path := filepath.Join(home, "config.toml")
	req, gotHome, err := PrepareSpineTransfer("release-test", 1, 1)
	if err != nil || gotHome != home {
		t.Fatalf("prepare = %+v home %q err %v", req, gotHome, err)
	}
	if req.BackupRetain != 5 {
		t.Fatalf("configured retention = %d, want the deployment's 5", req.BackupRetain)
	}
	if _, err := operatorconfig.UpdateAtomic(path, func(cfg *operatorconfig.Config) error {
		cfg.Timing.BackupRetain = 7
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	req, _, err = PrepareSpineTransfer("release-test", 1, 1)
	if err != nil || req.BackupRetain != 7 {
		t.Fatalf("retuned retention = %d, err %v", req.BackupRetain, err)
	}
	// A deployment whose operator config cannot be read must not fall back to
	// a compiled number and silently prune on it.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareSpineTransfer("release-test", 1, 1); err == nil {
		t.Fatal("a missing config.toml still produced a retention policy")
	}
}

func TestSpineBackupCrossingPublishesClosedSnapshotAndRetention(t *testing.T) {
	home, spine, req := transferFixture(t)
	stream := exportTransfer(t, spine, req)
	base := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	first, size, err := PublishSpineBackup(home, req, bytes.NewReader(stream), base)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(first)
	if err != nil || info.Mode().Perm() != 0o600 || size != info.Size() {
		t.Fatalf("published snapshot shape info=%v size=%d err=%v", info, size, err)
	}
	inspection, err := snapshotInspection(first)
	if err != nil || inspection.uuid != transferTestUUID || inspection.schemaID != substrate.CurrentSchemaID {
		t.Fatalf("published snapshot inspection=%+v err=%v", inspection, err)
	}
	unrelated := filepath.Join(home, "backups", "spine-operator-notes.db")
	if err := os.WriteFile(unrelated, []byte("do not prune"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < req.BackupRetain+3; i++ {
		if _, _, err := PublishSpineBackup(home, req, bytes.NewReader(stream), base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(home, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	valid := 0
	for _, entry := range entries {
		if backupSnapshotName.MatchString(entry.Name()) {
			valid++
		}
	}
	if valid != req.BackupRetain {
		t.Fatalf("retention left %d snapshots, want %d", valid, req.BackupRetain)
	}
	if body, err := os.ReadFile(unrelated); err != nil || string(body) != "do not prune" {
		t.Fatalf("retention touched unrelated file: %q %v", body, err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("oldest snapshot survived retention: %v", err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatalf("source stopped accepting opens after backup: %v", err)
	}
	db.Close()
}

func TestSpineBackupRefusesSymlinkedOrWidenedBackupRoot(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T, home string){
		"symlink": func(t *testing.T, home string) {
			target := filepath.Join(filepath.Dir(home), "target")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(home, "backups")); err != nil {
				t.Fatal(err)
			}
		},
		"widened": func(t *testing.T, home string) {
			if err := os.Mkdir(filepath.Join(home, "backups"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			home, spine, req := transferFixture(t)
			prepare(t, home)
			stream := exportTransfer(t, spine, req)
			if _, _, err := PublishSpineBackup(home, req, bytes.NewReader(stream), time.Now()); err == nil || !strings.Contains(err.Error(), "owner-only") {
				t.Fatalf("unsafe backup root accepted: %v", err)
			}
		})
	}
}

func TestSpineBackupCrossingRejectsTruncationAndIdentityDriftWithoutResidue(t *testing.T) {
	home, spine, req := transferFixture(t)
	stream := exportTransfer(t, spine, req)
	for name, body := range map[string][]byte{
		"truncated": stream[:len(stream)-1],
		"trailing":  append(append([]byte(nil), stream...), 'x'),
	} {
		if _, _, err := PublishSpineBackup(home, req, bytes.NewReader(body), time.Now()); err == nil {
			t.Fatalf("%s stream accepted", name)
		}
	}
	drift := req
	drift.DeploymentUUID = strings.Repeat("a", 32)
	if _, _, err := PublishSpineBackup(home, drift, bytes.NewReader(stream), time.Now()); err == nil {
		t.Fatal("identity-drifted response accepted")
	}
	entries, err := os.ReadDir(filepath.Join(home, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed publications left residue: %v", entries)
	}
}

func TestSpineRestoreAdmitsNewestMatchingSnapshotOnlyIntoLostSlot(t *testing.T) {
	home, spine, req := transferFixture(t)
	stream := exportTransfer(t, spine, req)
	backup, _, err := PublishSpineBackup(home, req, bytes.NewReader(stream), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(spine); err != nil {
		t.Fatal(err)
	}
	for _, sibling := range []string{spine + "-wal", spine + "-shm"} {
		_ = os.Remove(sibling)
	}
	file, header, selected, err := OpenLatestSpineBackup(home, req)
	if err != nil {
		t.Fatal(err)
	}
	if selected != backup {
		t.Fatalf("selected %q, want %q", selected, backup)
	}
	framed, err := FrameSpineBackup(file, header)
	if err != nil {
		t.Fatal(err)
	}
	defer framed.Close()
	result, err := RestoreSpine(spine, req.ReleaseBuildID, req.ControlVersion, req.ConfigSchemaVersion, framed)
	if err != nil || result != header {
		t.Fatalf("restore result=%+v err=%v", result, err)
	}
	inspection, err := inspectSpineReadOnly(spine)
	if err != nil || inspection.uuid != transferTestUUID || inspection.schemaID != substrate.CurrentSchemaID {
		t.Fatalf("restored spine inspection=%+v err=%v", inspection, err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var title string
	if err := db.QueryRow(`SELECT detail FROM activity WHERE id = 7`).Scan(&title); err != nil || title != "restore sentinel" {
		t.Fatalf("restored sentinel title=%q err=%v", title, err)
	}
}

func TestSpineRestoreReplaysHealthyTargetAndRefusesBadStreamWithoutChangingIt(t *testing.T) {
	_, spine, req := transferFixture(t)
	stream := exportTransfer(t, spine, req)
	if _, err := RestoreSpine(spine, req.ReleaseBuildID, req.ControlVersion, req.ConfigSchemaVersion, bytes.NewReader(stream)); err != nil {
		t.Fatalf("healthy restore replay failed: %v", err)
	}
	inspection, err := inspectSpineReadOnly(spine)
	if err != nil || inspection.uuid != transferTestUUID {
		t.Fatalf("refusal changed target: %+v %v", inspection, err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	foreignUUID := strings.Repeat("f", 32)
	if _, err := db.Exec(`UPDATE meta SET deployment_uuid = ? WHERE id = 1`, foreignUUID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreSpine(spine, req.ReleaseBuildID, req.ControlVersion, req.ConfigSchemaVersion, bytes.NewReader(stream)); err == nil || !strings.Contains(err.Error(), "foreign") {
		t.Fatalf("foreign non-empty target accepted: %v", err)
	}
	inspection, err = inspectSpineReadOnly(spine)
	if err != nil || inspection.uuid != foreignUUID {
		t.Fatalf("foreign refusal changed target: %+v %v", inspection, err)
	}
	if err := os.Remove(spine); err != nil {
		t.Fatal(err)
	}
	for _, sibling := range []string{spine + "-wal", spine + "-shm"} {
		_ = os.Remove(sibling)
	}
	bad := stream[:len(stream)-1]
	if _, err := RestoreSpine(spine, req.ReleaseBuildID, req.ControlVersion, req.ConfigSchemaVersion, bytes.NewReader(bad)); err == nil {
		t.Fatal("truncated restore accepted")
	}
	if _, err := os.Stat(spine); !os.IsNotExist(err) {
		t.Fatalf("failed restore materialized target: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(spine))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".restore-") {
			t.Fatalf("failed restore left stage %q", entry.Name())
		}
	}
}

func TestSpineRestoreRejectsAnIncompatibleSnapshot(t *testing.T) {
	home, spine, req := transferFixture(t)
	stream := exportTransfer(t, spine, req)
	snapshot, _, err := PublishSpineBackup(home, req, bytes.NewReader(stream), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	db, err := substrate.Open(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE incompatible_state (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if file, _, _, err := OpenLatestSpineBackup(home, req); err == nil {
		file.Close()
		t.Fatal("incompatible snapshot qualified for restoration")
	}
	payload, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	header := SpineBackupHeader{SpineTransferRequest: req, Bytes: int64(len(payload)), SHA256: hex.EncodeToString(digest[:])}
	var framed bytes.Buffer
	if err := writeTransferHeader(&framed, header); err != nil {
		t.Fatal(err)
	}
	framed.Write(payload)
	target := filepath.Join(t.TempDir(), "spine.db")
	if _, err := RestoreSpine(target, req.ReleaseBuildID, req.ControlVersion, req.ConfigSchemaVersion, &framed); err == nil {
		t.Fatal("incompatible snapshot restored through the private helper")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("refused restore created a target: %v", err)
	}
}
