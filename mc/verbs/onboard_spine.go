package verbs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"mc/substrate"
)

// OnboardSpineRequest is the bounded, path-free host→helper frame used during
// production Home provisioning. The caller's mirror observation is evidence;
// the helper computes every spine fact itself from its fixed /mc/spine scope.
type OnboardSpineRequest struct {
	ProtocolVersion int    `json:"protocol_version"`
	SchemaID        string `json:"schema_id"`
	MirrorState     string `json:"mirror_state"`
	MirrorUUID      string `json:"mirror_uuid,omitempty"`
	// AllowReset permits the replacement of a database with a different schema
	// or of an absent database. Reset replaces the database in every state.
	// Each replacement keeps the deployment identity, and the caller stops
	// every database user first.
	AllowReset bool `json:"allow_reset"`
	Reset      bool `json:"reset"`
}

type OnboardSpineResult struct {
	ProtocolVersion int    `json:"protocol_version"`
	Status          string `json:"status"`
	DeploymentUUID  string `json:"deployment_uuid"`
	SchemaID        string `json:"schema_id"`
}

// PrepareOnboardHome performs the host half of the production Home crossing.
// It validates the canonical home before any write and sends only the
// deployment mirror observation to the helper; it never receives a spine
// path and therefore cannot open the runtime-local database.
func PrepareOnboardHome(allowReset bool) (OnboardSpineRequest, error) {
	if _, _, err := onboardPreflight(); err != nil {
		return OnboardSpineRequest{}, err
	}
	home, err := mcHomeDir()
	if err != nil {
		return OnboardSpineRequest{}, err
	}
	uuid, exists, err := readDeploymentMirror(home)
	if err != nil {
		return OnboardSpineRequest{}, err
	}
	req := OnboardSpineRequest{
		ProtocolVersion: 1,
		SchemaID:        substrate.CurrentSchemaID,
		MirrorState:     "absent",
		AllowReset:      allowReset,
	}
	if exists {
		req.MirrorState = "present"
		req.MirrorUUID = uuid

	}
	return req, nil
}

// FinalizeOnboardHome is the host half after the helper has committed and
// re-read spine identity. It scaffolds MC_HOME and atomically publishes only
// the returned UUID mirror; no database bytes cross kernels.
func FinalizeOnboardHome(result OnboardSpineResult) (string, string, error) {
	if result.ProtocolVersion != 1 || result.SchemaID != substrate.CurrentSchemaID {
		return "", "", Domainf("helper onboard-spine response has mismatched protocol/schema identity")
	}
	if len(result.DeploymentUUID) != 32 {
		return "", "", Domainf("helper onboard-spine response has invalid deployment UUID")
	}
	if _, err := hex.DecodeString(result.DeploymentUUID); err != nil || strings.ToLower(result.DeploymentUUID) != result.DeploymentUUID {
		return "", "", Domainf("helper onboard-spine response has invalid deployment UUID")
	}
	validStatus := map[string]bool{
		"ok": true, "initialized": true, "repair-mirror": true,
		"reset": true,
	}
	if !validStatus[result.Status] {
		return "", "", Domainf("helper onboard-spine response has invalid status %q", result.Status)
	}
	home, err := mcHomeDir()
	if err != nil {
		return "", "", err
	}
	mirrored, mirrorExists, err := readDeploymentMirror(home)
	if err != nil {
		return "", "", err
	}
	if mirrorExists && mirrored != result.DeploymentUUID {
		return "", "", Domainf("deployment identity mismatch: MC_HOME has %s but helper proved spine %s — restore the matching backup", mirrored, result.DeploymentUUID)
	}
	changed, err := scaffoldOnboardHome(home)
	if err != nil {
		return "", "", err
	}
	if !mirrorExists {
		if err := writeDeploymentMirror(home, result.DeploymentUUID); err != nil {
			return "", "", err
		}
		changed = true
	}
	status := "ok"
	if changed || result.Status != "ok" {
		status = "done"
	}
	detail := fmt.Sprintf("deployment %s provisioned through helper (schema %s)", result.DeploymentUUID, result.SchemaID)
	if result.Status == "reset" {
		detail += "; the database is a new empty database"
	}
	return status, detail, nil
}

func validateOnboardSpineRequest(req OnboardSpineRequest) error {
	if req.ProtocolVersion != 1 {
		return Usagef("private onboard-spine protocol_version %d is unsupported", req.ProtocolVersion)
	}
	if req.SchemaID != substrate.CurrentSchemaID {
		return Domainf("private onboard-spine schema identity %s does not match helper build %s",
			req.SchemaID, substrate.CurrentSchemaID)
	}
	switch req.MirrorState {
	case "absent":
		if req.MirrorUUID != "" {
			return Usagef("private onboard-spine absent mirror carries a UUID")
		}
	case "present":
		if len(req.MirrorUUID) != 32 || strings.ToLower(req.MirrorUUID) != req.MirrorUUID {
			return Usagef("private onboard-spine mirror has an invalid deployment UUID")
		}
		if _, err := hex.DecodeString(req.MirrorUUID); err != nil {
			return Usagef("private onboard-spine mirror has an invalid deployment UUID")
		}
	default:
		return Usagef("private onboard-spine mirror_state must be absent or present")
	}
	return nil
}

// lockOnboardSpine serializes database inspection, reset, and creation across processes.
// The persistent lock remains separate from the database files that a reset removes.
func lockOnboardSpine(spine string) (*os.File, error) {
	if spine == "" {
		return nil, Usagef("database path is required")
	}
	if err := substrate.GuardLockDomain(spine); err != nil {
		return nil, Domainf("%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(spine), 0o700); err != nil {
		return nil, Domainf("create database directory: %v", err)
	}
	lock, err := os.OpenFile(spine+".onboard.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, Domainf("open database lock: %v", err)
	}
	info, err := lock.Stat()
	if err != nil {
		lock.Close()
		return nil, Domainf("inspect database lock: %v", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		lock.Close()
		return nil, Domainf("database lock must be an owner-only single-link regular file")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		lock.Close()
		return nil, Domainf("acquire database lock: %v", err)
	}
	current, err := os.Lstat(lock.Name())
	if err != nil || !os.SameFile(info, current) {
		lock.Close()
		return nil, Domainf("database lock changed identity")
	}
	return lock, nil
}

// OnboardSpine performs only runtime-local spine work. It never reads MC_HOME,
// config, credentials, routing, or a host path; production calls it with the
// helper's fixed /mc/spine/spine.db path after private scope validation.
func OnboardSpine(spine string, req OnboardSpineRequest) (OnboardSpineResult, error) {
	if err := validateOnboardSpineRequest(req); err != nil {
		return OnboardSpineResult{}, err
	}
	lock, err := lockOnboardSpine(spine)
	if err != nil {
		return OnboardSpineResult{}, err
	}
	defer lock.Close()
	inspection, err := inspectSpineReadOnly(spine)
	if err != nil {
		return OnboardSpineResult{}, err
	}
	status := "initialized"
	uuid := req.MirrorUUID
	if inspection.tables > 0 {
		if err := validateInspection(spine, inspection); err != nil {
			return OnboardSpineResult{}, err
		}
		if req.MirrorState == "present" && req.MirrorUUID != inspection.uuid {
			return OnboardSpineResult{}, Domainf("deployment identity mismatch: host mirror has %s but spine has %s", req.MirrorUUID, inspection.uuid)
		}
		if replace := req.Reset || (req.AllowReset && !inspection.compatible); !replace {
			if !inspection.compatible {
				return OnboardSpineResult{}, &DomainError{Code: "database-schema-differs", Msg: "the database schema differs from this release; redeploy.sh --reset-database replaces the database with an empty database"}
			}
			status = "ok"
			if req.MirrorState == "absent" {
				status = "repair-mirror"
			}
			return OnboardSpineResult{ProtocolVersion: 1, Status: status, DeploymentUUID: inspection.uuid, SchemaID: substrate.CurrentSchemaID}, nil
		}
		if req.MirrorState != "present" {
			return OnboardSpineResult{}, Domainf("database reset requires the deployment identity mirror")
		}
		if err := resetDevelopmentSpine(spine); err != nil {
			return OnboardSpineResult{}, err
		}
		status = "reset"
	} else if req.MirrorState == "present" {
		if !req.Reset && !req.AllowReset {
			return OnboardSpineResult{}, &DomainError{Code: "database-absent", Msg: "spine lost: the deployment database is absent; redeploy.sh --reset-database creates an empty database"}
		}
		if err := resetDevelopmentSpine(spine); err != nil {
			return OnboardSpineResult{}, err
		}
		status = "reset"
	}

	parent := filepath.Dir(spine)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return OnboardSpineResult{}, Usagef("create spine volume root: %v", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return OnboardSpineResult{}, Usagef("secure spine volume root: %v", err)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return OnboardSpineResult{}, Usagef("generate deployment uuid: %v", err)
	}
	if uuid == "" {
		uuid = hex.EncodeToString(raw[:])
	}
	db, err := substrate.Open(spine)
	if err != nil {
		return OnboardSpineResult{}, Usagef("%v", err)
	}
	err = inTx(db, func(ctx context.Context, q Q) error {
		if _, err := q.ExecContext(ctx, substrate.Schema); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx,
			`INSERT INTO meta (id, deployment_uuid, schema_id) VALUES (1, ?, ?)`,
			uuid, substrate.CurrentSchemaID)
		return err
	})
	closeErr := db.Close()
	if err != nil {
		return OnboardSpineResult{}, Domainf("initialize spine: %v", err)
	}
	if closeErr != nil {
		return OnboardSpineResult{}, Domainf("close provisioned spine: %v", closeErr)
	}
	if err := os.Chmod(spine, 0o600); err != nil {
		return OnboardSpineResult{}, Domainf("secure provisioned spine: %v", err)
	}
	return OnboardSpineResult{
		ProtocolVersion: 1,
		Status:          status,
		DeploymentUUID:  uuid,
		SchemaID:        substrate.CurrentSchemaID,
	}, nil
}

func (r OnboardSpineResult) String() string {
	return fmt.Sprintf("%s deployment %s schema %s", r.Status, r.DeploymentUUID, r.SchemaID)
}

// DeployedDatabase compares a deployed database with the schema of this release.
// State is absent, current, or differs.
type DeployedDatabase struct {
	ProtocolVersion int    `json:"protocol_version"`
	SchemaID        string `json:"schema_id"`
	State           string `json:"state"`
	DeploymentUUID  string `json:"deployment_uuid,omitempty"`
}

// InspectDeployedDatabase reads the database through a read-only open and
// takes no lock, so a running deployment keeps its database unchanged.
func InspectDeployedDatabase(spine string) (DeployedDatabase, error) {
	result := DeployedDatabase{ProtocolVersion: 1, SchemaID: substrate.CurrentSchemaID, State: "absent"}
	inspection, err := inspectSpineReadOnly(spine)
	if err != nil {
		return result, err
	}
	if inspection.tables == 0 {
		return result, nil
	}
	if err := validateInspection(spine, inspection); err != nil {
		return result, err
	}
	result.DeploymentUUID = inspection.uuid
	result.State = "differs"
	if inspection.compatible {
		result.State = "current"
	}
	return result, nil
}
