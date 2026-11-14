package deployment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"mc/distribution"
)

var updateIDPattern = regexp.MustCompile(`^[a-f0-9]{16,64}$`)
var updatePhases = []string{"stop", "prepare", "checkpoint", "replace", "verify", "committed", "complete", "restore", "restored"}

type UpdateJournal struct {
	Version             int                  `json:"version"`
	ID                  string               `json:"id"`
	Deployment          string               `json:"deployment"`
	Phase               string               `json:"phase"`
	Candidate           distribution.Record  `json:"candidate"`
	Previous            *distribution.Record `json:"previous,omitempty"`
	CandidateRoot       string               `json:"candidate_root"`
	CandidateSHA256     string               `json:"candidate_sha256,omitempty"`
	CandidateTreeSHA256 string               `json:"candidate_tree_sha256,omitempty"`
	BinDir              string               `json:"bin_dir,omitempty"`
	PriorServices       map[string]bool      `json:"prior_services"`
	PriorStops          ServiceStops         `json:"prior_stops"`
	ConfigSHA256        string               `json:"config_sha256,omitempty"`
	Checkpoint          *UpdateCheckpoint    `json:"checkpoint,omitempty"`
	Problem             string               `json:"problem,omitempty"`
	StartedAt           time.Time            `json:"started_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

func UpdateJournalPath(home string) string   { return filepath.Join(home, "updates", "transaction.json") }
func UpdateDirectory(home, id string) string { return filepath.Join(home, "updates", id) }
func (j *UpdateJournal) Committed() bool     { return j.Phase == "committed" || j.Phase == "complete" }
func (j *UpdateJournal) BlocksWork() bool    { return !j.Committed() && j.Phase != "restored" }

func (j *UpdateJournal) validate(home string) error {
	names, err := RuntimeNames(home)
	if err != nil {
		return err
	}
	if j.Version != 1 || !updateIDPattern.MatchString(j.ID) || j.Deployment != names.Suffix || !slices.Contains(updatePhases, j.Phase) {
		return errors.New("The update record has an invalid identity or phase. Keep Culler stopped and repair the update record.")
	}
	if err := j.Candidate.Validate(); err != nil {
		return fmt.Errorf("The update candidate is invalid: %w", err)
	}
	if j.Previous != nil {
		if err := j.Previous.Validate(); err != nil {
			return fmt.Errorf("The previous release is invalid: %w", err)
		}
	}
	if j.CandidateRoot != filepath.Join(UpdateDirectory(home, j.ID), "candidate") {
		return errors.New("The retained candidate path is invalid.")
	}
	if j.BinDir != "" && (!filepath.IsAbs(j.BinDir) || filepath.Clean(j.BinDir) != j.BinDir) {
		return errors.New("The update command directory is invalid.")
	}
	for _, value := range []string{j.CandidateSHA256, j.CandidateTreeSHA256, j.ConfigSHA256} {
		if value != "" && !validSHA256(value) {
			return errors.New("The update checksum is invalid.")
		}
	}
	for unit := range j.PriorServices {
		if !slices.Contains(SupervisedUnits, unit) {
			return errors.New("The update names an unknown service.")
		}
	}
	for unit := range j.PriorStops {
		if !slices.Contains(SupervisedUnits, unit) {
			return errors.New("The update names an unknown stop choice.")
		}
	}
	if j.Checkpoint != nil {
		return j.Checkpoint.validate(home, j)
	}
	return nil
}

func LoadUpdateJournal(home string) (*UpdateJournal, error) {
	path := UpdateJournalPath(home)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || links != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4<<20 {
		return nil, errors.New("The update record requires an owner-only regular file.")
	}
	if err := ensureUpdateRoot(home); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record UpdateJournal
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("The update record is unreadable: %w", err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("The update record contains extra data.")
	}
	if err := record.validate(home); err != nil {
		return nil, err
	}
	return &record, nil
}

func SaveUpdateJournal(home string, j *UpdateJournal) error {
	if j == nil {
		return errors.New("The update record is absent.")
	}
	if err := j.validate(home); err != nil {
		return err
	}
	if err := ensureUpdateRoot(home); err != nil {
		return err
	}
	j.UpdatedAt = time.Now().UTC()
	if j.StartedAt.IsZero() {
		j.StartedAt = j.UpdatedAt
	}
	body, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return writeUpdateFile(UpdateJournalPath(home), append(body, '\n'), 0600)
}

func UpdateBlocksWork(home string) error {
	j, err := LoadUpdateJournal(home)
	if err != nil {
		return err
	}
	if j != nil && j.BlocksWork() {
		return errors.New("An incomplete update keeps Culler stopped. Run culler update to recover.")
	}
	if j != nil {
		path := filepath.Join(home, "state", "install-maintenance.json")
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		uid, links, ok := statOwner(info)
		if !ok || int(uid) != os.Getuid() || links != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
			return errors.New("The update activation request is invalid. Keep Culler stopped and run culler update.")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var request struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(body, &request) != nil || request.ID == "" {
			return errors.New("The update activation request is unreadable. Keep Culler stopped.")
		}
		if request.ID == j.ID {
			return errors.New("The update has not released its activation gate. Run culler update to recover.")
		}
	}
	return nil
}

func ensureUpdateRoot(home string) error {
	root := filepath.Join(home, "updates")
	if err := ensureOwnerDirectory(root, "update records"); err != nil {
		return err
	}
	return syncDirectory(home)
}

func writeUpdateFile(path string, body []byte, mode os.FileMode) error {
	parent := filepath.Dir(path)
	file, err := os.CreateTemp(parent, ".update-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(body)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncDirectory(parent)
}
