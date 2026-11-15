package verbs

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"mc/substrate"

	_ "modernc.org/sqlite"
)

var backupSnapshotName = regexp.MustCompile(`^spine-[0-9]{8}T[0-9]{6}(?:\.[0-9]{9})?Z(?:-[0-9]+)?\.db$`)

const (
	spineTransferProtocol  = 1
	spineTransferMaxHeader = 64 * 1024
	spineTransferMaxBytes  = int64(1 << 50)
)

// SpineTransferRequest is the closed host/helper identity frame for backup and
// restore. Snapshot bytes cross the kernel boundary, but no host path does.
type SpineTransferRequest struct {
	ProtocolVersion     int    `json:"protocol_version"`
	ReleaseBuildID      string `json:"release_build_id"`
	ControlVersion      int    `json:"control_version"`
	SpineSchemaID       string `json:"spine_schema_id"`
	ConfigSchemaVersion int    `json:"config_schema_version"`
	DeploymentUUID      string `json:"deployment_uuid"`
	// BackupRetain rides the REQUEST rather than the helper's header because
	// retention is operator policy and the mountless helper has no
	// config.toml to read it from. The helper echoes the request it was given;
	// the host prunes its own backups directory on its own number.
	BackupRetain int `json:"backup_retain"`
}

type SpineBackupHeader struct {
	SpineTransferRequest
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func PrepareSpineTransfer(releaseBuildID string, controlVersion, configSchemaVersion int) (SpineTransferRequest, string, error) {
	home, err := mcHomeDir()
	if err != nil {
		return SpineTransferRequest{}, "", err
	}
	homeInfo, err := os.Lstat(home)
	if err != nil {
		return SpineTransferRequest{}, "", Domainf("inspect MC_HOME for spine transfer: %v", err)
	}
	homeStat, ok := homeInfo.Sys().(*syscall.Stat_t)
	if !ok || homeInfo.Mode()&os.ModeSymlink != 0 || !homeInfo.IsDir() || homeInfo.Mode().Perm() != 0o700 || homeStat.Uid != uint32(os.Getuid()) {
		return SpineTransferRequest{}, "", Domainf("MC_HOME must be a real owner-only directory for spine transfer")
	}
	uuid, exists, err := readDeploymentMirror(home)
	if err != nil {
		return SpineTransferRequest{}, "", err
	}
	if !exists {
		return SpineTransferRequest{}, "", Domainf("deployment identity is not provisioned; run mc onboard home first")
	}
	cfg, _, err := loadOperatorConfig()
	if err != nil {
		return SpineTransferRequest{}, "", err
	}
	return SpineTransferRequest{
		ProtocolVersion: spineTransferProtocol, ReleaseBuildID: releaseBuildID,
		ControlVersion: controlVersion, SpineSchemaID: substrate.CurrentSchemaID,
		ConfigSchemaVersion: configSchemaVersion, DeploymentUUID: uuid,
		BackupRetain: cfg.Timing.BackupRetain,
	}, home, nil
}

func validateSpineTransferRequest(req SpineTransferRequest, releaseBuildID string, controlVersion, configSchemaVersion int) error {
	if req.ProtocolVersion != spineTransferProtocol || req.ReleaseBuildID != releaseBuildID ||
		req.ControlVersion != controlVersion || req.SpineSchemaID != substrate.CurrentSchemaID ||
		req.ConfigSchemaVersion != configSchemaVersion || len(req.DeploymentUUID) != 32 {
		return Domainf("private spine-transfer build/schema identity mismatch")
	}
	if req.BackupRetain < 1 || req.BackupRetain > 10000 {
		return Domainf("private spine-transfer retention policy is outside its bound")
	}
	if _, err := hex.DecodeString(req.DeploymentUUID); err != nil || strings.ToLower(req.DeploymentUUID) != req.DeploymentUUID {
		return Domainf("private spine-transfer deployment identity is invalid")
	}
	return nil
}

func writeTransferHeader(w io.Writer, header SpineBackupHeader) error {
	body, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if len(body) == 0 || len(body) > spineTransferMaxHeader {
		return fmt.Errorf("spine-transfer header is outside its bound")
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if _, err := w.Write(prefix[:]); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func readTransferHeader(r io.Reader) (SpineBackupHeader, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return SpineBackupHeader{}, fmt.Errorf("read spine-transfer header length: %w", err)
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length == 0 || length > spineTransferMaxHeader {
		return SpineBackupHeader{}, fmt.Errorf("spine-transfer header length %d is outside its bound", length)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return SpineBackupHeader{}, fmt.Errorf("read spine-transfer header: %w", err)
	}
	var header SpineBackupHeader
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&header); err != nil {
		return SpineBackupHeader{}, fmt.Errorf("decode spine-transfer header: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return SpineBackupHeader{}, fmt.Errorf("spine-transfer header carries trailing data")
	}
	if header.Bytes < 1 || header.Bytes > spineTransferMaxBytes || len(header.SHA256) != 64 || header.BackupRetain < 1 || header.BackupRetain > 10000 {
		return SpineBackupHeader{}, fmt.Errorf("spine-transfer header carries invalid size, digest, or retention")
	}
	if _, err := hex.DecodeString(header.SHA256); err != nil || strings.ToLower(header.SHA256) != header.SHA256 {
		return SpineBackupHeader{}, fmt.Errorf("spine-transfer header digest is invalid")
	}
	return header, nil
}

func snapshotInto(db *sql.DB, path string) error {
	if _, err := db.Exec(`VACUUM INTO ?`, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// ExportSpineBackup runs in the helper. It creates a consistent temporary
// SQLite snapshot on the runtime-local volume, frames it to stdout, and always
// removes the helper-side copy.
func ExportSpineBackup(spine string, req SpineTransferRequest, releaseBuildID string, controlVersion, configSchemaVersion int, out io.Writer) error {
	if err := validateSpineTransferRequest(req, releaseBuildID, controlVersion, configSchemaVersion); err != nil {
		return err
	}
	inspection, err := inspectSpineReadOnly(spine)
	if err != nil {
		return err
	}
	if err := validateInspection(spine, inspection); err != nil {
		return err
	}
	if inspection.uuid != req.DeploymentUUID ||
		!inspection.compatible {
		return Domainf("private backup deployment identity mismatch")
	}
	db, err := substrate.Open(spine)
	if err != nil {
		return Usagef("%v", err)
	}
	defer db.Close()
	tmp, err := os.CreateTemp(filepath.Dir(spine), ".backup-export-*.db")
	if err != nil {
		return Domainf("create helper backup stage: %v", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return Domainf("close helper backup stage: %v", err)
	}
	if err := os.Remove(tmpPath); err != nil {
		return Domainf("prepare helper backup stage: %v", err)
	}
	defer os.Remove(tmpPath)
	if err := snapshotInto(db, tmpPath); err != nil {
		return Domainf("snapshot spine: %v", err)
	}
	f, err := os.Open(tmpPath)
	if err != nil {
		return Domainf("open helper snapshot: %v", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > spineTransferMaxBytes {
		return Domainf("helper snapshot has invalid shape or size")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return Domainf("hash helper snapshot: %v", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Domainf("rewind helper snapshot: %v", err)
	}
	header := SpineBackupHeader{SpineTransferRequest: req, Bytes: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}
	if err := writeTransferHeader(out, header); err != nil {
		return Usagef("write backup header: %v", err)
	}
	if copied, err := io.CopyN(out, f, info.Size()); err != nil || copied != info.Size() {
		return Usagef("write backup bytes: copied %d of %d: %v", copied, info.Size(), err)
	}
	return nil
}

func snapshotInspection(path string) (spineInspection, error) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() < 1 {
		return spineInspection{}, fmt.Errorf("snapshot is not a non-empty regular file: %v", err)
	}
	db, err := substrate.OpenReadOnly(path)
	if err != nil {
		return spineInspection{}, err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return spineInspection{}, fmt.Errorf("snapshot integrity check failed: %v %s", err, integrity)
	}
	inspection := spineInspection{exists: true, bytes: st.Size()}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&inspection.tables); err != nil {
		return spineInspection{}, err
	}
	if err := substrate.CheckSchema(db); err != nil {
		return spineInspection{}, err
	}
	if err := db.QueryRow(`SELECT deployment_uuid, schema_id FROM meta WHERE id = 1`).Scan(&inspection.uuid, &inspection.schemaID); err != nil {
		return spineInspection{}, err
	}
	inspection.hasMeta = true
	inspection.compatible = true
	return inspection, nil
}

func backupName(prefix string, now time.Time, suffix int) string {
	name := prefix + now.UTC().Format("20060102T150405.000000000Z")
	if suffix > 0 {
		name += fmt.Sprintf("-%04d", suffix)
	}
	return name + ".db"
}

func pruneBackups(dir string, retain int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && len(name) <= 80 && backupSnapshotName.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names[:max(0, len(names)-retain)] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return syncDir(dir)
}

func requireSecureBackupDir(home string) (string, error) {
	dir := filepath.Join(home, "backups")
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", err
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 || stat.Uid != uint32(os.Getuid()) {
		return "", fmt.Errorf("backups dir must be a real owner-only directory")
	}
	return dir, nil
}

// PublishSpineBackup receives one helper frame into MC_HOME/backups and makes
// it visible only after digest, SQLite integrity, schema, and deployment
// identity all pass.
func PublishSpineBackup(home string, req SpineTransferRequest, input io.Reader, now time.Time) (string, int64, error) {
	header, err := readTransferHeader(input)
	if err != nil {
		return "", 0, Domainf("receive backup: %v", err)
	}
	if header.SpineTransferRequest != req {
		return "", 0, Domainf("backup response identity mismatch")
	}
	dir, err := requireSecureBackupDir(home)
	if err != nil {
		return "", 0, Domainf("secure backups dir: %v", err)
	}
	stage, err := os.CreateTemp(dir, ".backup-receive-*.tmp")
	if err != nil {
		return "", 0, Domainf("create backup receive stage: %v", err)
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)
	if err := stage.Chmod(0o600); err != nil {
		stage.Close()
		return "", 0, Domainf("secure backup receive stage: %v", err)
	}
	hash := sha256.New()
	copied, copyErr := io.CopyN(io.MultiWriter(stage, hash), input, header.Bytes)
	var extra [1]byte
	extraN, extraErr := input.Read(extra[:])
	if copyErr != nil || copied != header.Bytes || extraN != 0 || (extraErr != nil && !errors.Is(extraErr, io.EOF)) {
		stage.Close()
		return "", 0, Domainf("backup stream length mismatch")
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != header.SHA256 {
		stage.Close()
		return "", 0, Domainf("backup stream digest mismatch")
	}
	if err := stage.Sync(); err != nil {
		stage.Close()
		return "", 0, Domainf("sync backup receive stage: %v", err)
	}
	if err := stage.Close(); err != nil {
		return "", 0, Domainf("close backup receive stage: %v", err)
	}
	inspection, err := snapshotInspection(stagePath)
	if err != nil || inspection.uuid != req.DeploymentUUID || inspection.schemaID != req.SpineSchemaID {
		return "", 0, Domainf("backup snapshot identity/integrity mismatch: %v", err)
	}
	prefix := "spine-"
	var final string
	for suffix := 0; ; suffix++ {
		final = filepath.Join(dir, backupName(prefix, now, suffix))
		if _, err := os.Lstat(final); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return "", 0, Domainf("probe backup destination: %v", err)
		}
	}
	if err := os.Rename(stagePath, final); err != nil {
		return "", 0, Domainf("publish backup: %v", err)
	}
	if err := syncDir(dir); err != nil {
		return "", 0, Domainf("sync published backup: %v", err)
	}
	if err := pruneBackups(dir, header.BackupRetain); err != nil {
		return "", 0, Domainf("prune backups: %v", err)
	}
	return final, header.Bytes, nil
}

func secureBackupFile(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm() == 0o600 && stat.Uid == uint32(os.Getuid()) && stat.Nlink == 1
}

// OpenLatestSpineBackup chooses the newest closed snapshot and returns a
// framed stream suitable for RestoreSpine. The caller must close the stream.
func OpenLatestSpineBackup(home string, req SpineTransferRequest) (io.ReadCloser, SpineBackupHeader, string, error) {
	dir, err := requireSecureBackupDir(home)
	if err != nil {
		return nil, SpineBackupHeader{}, "", Domainf("secure backups dir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, SpineBackupHeader{}, "", Domainf("read backups dir: %v", err)
	}
	names := []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() && len(entry.Name()) <= 80 && backupSnapshotName.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return nil, SpineBackupHeader{}, "", Domainf("no spine backup is available under %q", dir)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	path := filepath.Join(dir, names[0])
	before, err := os.Lstat(path)
	if err != nil || !secureBackupFile(before) {
		return nil, SpineBackupHeader{}, "", Domainf("newest spine backup is not an owner-only single-link regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, SpineBackupHeader{}, "", Domainf("open newest spine backup: %v", err)
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, SpineBackupHeader{}, "", Domainf("newest spine backup changed identity while opening")
	}
	inspection, err := snapshotInspection(path)
	if err != nil || inspection.uuid != req.DeploymentUUID || !inspection.compatible || inspection.schemaID != req.SpineSchemaID {
		f.Close()
		return nil, SpineBackupHeader{}, "", Domainf("newest spine backup identity/integrity mismatch: %v", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		f.Close()
		return nil, SpineBackupHeader{}, "", Domainf("hash newest spine backup: %v", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, SpineBackupHeader{}, "", Domainf("rewind newest spine backup: %v", err)
	}
	header := SpineBackupHeader{SpineTransferRequest: req, Bytes: after.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}
	return f, header, path, nil
}

type framedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (f framedReadCloser) Close() error { return f.closer.Close() }

func FrameSpineBackup(file io.ReadCloser, header SpineBackupHeader) (io.ReadCloser, error) {
	var prefix strings.Builder
	if err := writeTransferHeader(&prefix, header); err != nil {
		file.Close()
		return nil, err
	}
	return framedReadCloser{Reader: io.MultiReader(strings.NewReader(prefix.String()), file), closer: file}, nil
}

// RestoreSpine runs in the helper and only admits a matching, intact snapshot
// into a missing/empty spine slot. It never overwrites a healthy or unknown
// non-empty spine.
func RestoreSpine(spine string, releaseBuildID string, controlVersion, configSchemaVersion int, input io.Reader) (SpineBackupHeader, error) {
	header, err := readTransferHeader(input)
	if err != nil {
		return SpineBackupHeader{}, Domainf("restore frame identity is invalid: %v", err)
	}
	req := header.SpineTransferRequest
	if err := validateSpineTransferRequest(req, releaseBuildID, controlVersion, configSchemaVersion); err != nil {
		return SpineBackupHeader{}, err
	}
	current, err := inspectSpineReadOnly(spine)
	if err != nil {
		return SpineBackupHeader{}, err
	}
	if current.bytes > 0 || current.tables > 0 {
		if err := validateInspection(spine, current); err != nil || !current.compatible || current.uuid != req.DeploymentUUID {
			return SpineBackupHeader{}, Domainf("restore refuses to overwrite a non-empty or foreign spine")
		}

	}
	parent := filepath.Dir(spine)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return SpineBackupHeader{}, Domainf("create spine root for restore: %v", err)
	}
	stage, err := os.CreateTemp(parent, ".restore-*.db")
	if err != nil {
		return SpineBackupHeader{}, Domainf("create restore stage: %v", err)
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)
	if err := stage.Chmod(0o600); err != nil {
		stage.Close()
		return SpineBackupHeader{}, Domainf("secure restore stage: %v", err)
	}
	hash := sha256.New()
	copied, copyErr := io.CopyN(io.MultiWriter(stage, hash), input, header.Bytes)
	var extra [1]byte
	extraN, extraErr := input.Read(extra[:])
	if copyErr != nil || copied != header.Bytes || extraN != 0 || (extraErr != nil && !errors.Is(extraErr, io.EOF)) || hex.EncodeToString(hash.Sum(nil)) != header.SHA256 {
		stage.Close()
		return SpineBackupHeader{}, Domainf("restore stream length or digest mismatch")
	}
	if err := stage.Sync(); err != nil {
		stage.Close()
		return SpineBackupHeader{}, Domainf("sync restore stage: %v", err)
	}
	if err := stage.Close(); err != nil {
		return SpineBackupHeader{}, Domainf("close restore stage: %v", err)
	}
	inspection, err := snapshotInspection(stagePath)
	if err != nil || inspection.uuid != req.DeploymentUUID || !inspection.compatible || inspection.schemaID != req.SpineSchemaID {
		return SpineBackupHeader{}, Domainf("restore snapshot identity/integrity mismatch: %v", err)
	}
	if current.bytes > 0 || current.tables > 0 {
		return header, nil
	}
	for _, sibling := range []string{spine + "-wal", spine + "-shm"} {
		if info, err := os.Lstat(sibling); err == nil {
			if !info.Mode().IsRegular() || info.Size() != 0 {
				return SpineBackupHeader{}, Domainf("restore refuses unexpected non-empty SQLite sibling %q", sibling)
			}
			if err := os.Remove(sibling); err != nil {
				return SpineBackupHeader{}, Domainf("remove empty SQLite sibling: %v", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return SpineBackupHeader{}, Domainf("inspect SQLite sibling: %v", err)
		}
	}
	if err := os.Rename(stagePath, spine); err != nil {
		return SpineBackupHeader{}, Domainf("commit restored spine: %v", err)
	}
	if err := syncDir(parent); err != nil {
		return SpineBackupHeader{}, Domainf("sync restored spine: %v", err)
	}
	return header, nil
}
