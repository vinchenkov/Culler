package verbs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"mc/substrate"
)

// InitArgs carries mc __init's flags: skeleton-only provisioning for a test
// or development spine.
type InitArgs struct {
	Spine         string
	Project       string
	WorkspaceRoot string
	// ArtifactRoot, when non-empty, gives the seeded profile the one durable
	// plane a packaged terminal will accept a render under. Empty keeps the
	// skeleton shape: several standalone probes call Init directly against
	// workspaces that have no `.mc-artifacts`, and giving them a root whose
	// source does not exist would refuse every dispatch at mount-attest
	// instead of only the Packager's terminal. Opt-in, never unconditional.
	ArtifactRoot string
	// Settings; zero = keep the schema default.
	TimeoutMinutes      int
	GraceMinutes        int
	HeartbeatIntervalS  int
	SpawnGraceS         int
	HardDeadlineMinutes int
	ReviewWIPCap        int
	CollieIdleTimeoutS  int
}

// Init provisions a fresh spine: applies substrate.Schema, seeds meta, one
// sandbox_profiles + projects row (whose lease row the substrate inserts
// with it), and writes the settings row. The profile carries the Project
// synthetic home under MC_HOME, which Init creates. It is not idempotent by
// design (substrate.Init): re-initializing a non-empty spine fails loudly.
func Init(a InitArgs) (any, error) {
	if a.Spine == "" || a.Project == "" || a.WorkspaceRoot == "" {
		return nil, Usagef("mc __init requires --spine, --project, and --workspace-root")
	}
	if !validStructuralText(a.Project, maxPrivateScalarBytes) ||
		!validStructuralText(a.WorkspaceRoot, maxPrivateScalarBytes) {
		return nil, Usagef("mc __init Project and workspace root must be valid UTF-8 without controls and at most 4096 bytes")
	}
	toolHome, err := initProjectToolHome(a.Project)
	if err != nil {
		return nil, err
	}
	db, err := substrate.Open(a.Spine)
	if err != nil {
		return nil, Usagef("%v", err)
	}
	defer db.Close()
	if err := substrate.Init(db); err != nil {
		return nil, Domainf("%v", err)
	}
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return nil, Usagef("generate deployment uuid: %v", err)
	}
	deployment := hex.EncodeToString(uuid[:])
	err = inTx(db, func(ctx context.Context, q Q) error {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO meta (id, deployment_uuid, schema_id) VALUES (1, ?, ?)`,
			deployment, substrate.CurrentSchemaID); err != nil {
			return err
		}
		// One sandbox profile. `artifact_roots` is the request half of the
		// artifact plane; the mount allowlist is the permission half. A
		// profile that omits this column derives a plan with no writable
		// artifact row, and the packaged terminal then refuses every render
		// as undurable.
		artifactRoots := "[]"
		if a.ArtifactRoot != "" {
			encoded, err := json.Marshal([]string{a.ArtifactRoot})
			if err != nil {
				return err
			}
			artifactRoots = string(encoded)
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO sandbox_profiles (id, workspace_root, artifact_roots, tool_home_dir)
			VALUES ('default', ?, ?, ?)`, a.WorkspaceRoot, artifactRoots, toolHome); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO projects (id, title, sandbox_profile)
			VALUES (?, ?, 'default')`, a.Project, a.Project); err != nil {
			return err
		}
		if err := applyTunables(ctx, q, a); err != nil {
			return err
		}
		return substrate.ValidateDispatchMountProjection(ctx, q)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"spine":           a.Spine,
		"project":         a.Project,
		"deployment_uuid": deployment,
	}, nil
}

// initProjectToolHome creates the Project synthetic home under the canonical
// MC_HOME, one owner-only component at a time, and returns its path.
func initProjectToolHome(project string) (string, error) {
	home, err := mcHomeDir()
	if err != nil {
		return "", err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return "", Usagef("resolve canonical MC_HOME for the Project synthetic home: %v", err)
	}
	home = filepath.Clean(home)
	toolHome := ProjectToolHome(home, project)
	for _, path := range []string{
		filepath.Join(home, "state"),
		filepath.Join(home, "state", "projects"),
		filepath.Join(home, "state", "projects", project),
		toolHome,
	} {
		if _, err := ensureOperatorRoot(path, os.Getuid()); err != nil {
			return "", Domainf("Project %s cannot use synthetic home component %q: %v", project, path, err)
		}
	}
	return toolHome, nil
}

func applyTunables(ctx context.Context, q Q, a InitArgs) error {
	set := func(col string, v int) error {
		if v <= 0 {
			return nil
		}
		_, err := q.ExecContext(ctx,
			fmt.Sprintf(`UPDATE settings SET %s = ? WHERE id = 1`, col), v)
		return err
	}
	for col, v := range map[string]int{
		"timeout_minutes":       a.TimeoutMinutes,
		"grace_minutes":         a.GraceMinutes,
		"heartbeat_interval_s":  a.HeartbeatIntervalS,
		"spawn_grace_s":         a.SpawnGraceS,
		"hard_deadline_minutes": a.HardDeadlineMinutes,
		"review_wip_cap":        a.ReviewWIPCap,
		"collie_idle_timeout_s": a.CollieIdleTimeoutS,
	} {
		if err := set(col, v); err != nil {
			return err
		}
	}
	return nil
}

// OpenSpine opens an existing spine for the non-init verbs.
func OpenSpine(path string) (*sql.DB, error) {
	if path == "" {
		return nil, Usagef("no spine: set MC_SPINE (or MC_HELPER to delegate)")
	}
	db, err := substrate.Open(path)
	if err != nil {
		return nil, Usagef("%v", err)
	}
	return db, nil
}

// OpenSpineRead opens an existing spine for the read verbs: the one shared
// read-only path (substrate.OpenReadOnly). It never creates the database and
// never requires a WAL or SHM sidecar, so a read verb works against a spine
// on a read-only mount.
func OpenSpineRead(path string) (*sql.DB, error) {
	if path == "" {
		return nil, Usagef("no spine: set MC_SPINE (or MC_HELPER to delegate)")
	}
	db, err := substrate.OpenReadOnly(path)
	if err != nil {
		return nil, Usagef("%v", err)
	}
	return db, nil
}
