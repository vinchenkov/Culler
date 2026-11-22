package verbs

import (
	"encoding/json"
	"mc/substrate"
)

// ReleaseDatabase describes compatibility and active work without a database write.
type ReleaseDatabase struct {
	Schema                string                `json:"schema"`
	UUID                  string                `json:"uuid,omitempty"`
	Empty                 bool                  `json:"empty"`
	Runs                  int                   `json:"runs"`
	Leases                int                   `json:"leases"`
	RepositoryOperations  int                   `json:"repository_operations"`
	EnvironmentOperations int                   `json:"environment_operations"`
	CollieLaunches        int                   `json:"collie_launches"`
	CollieContainers      []string              `json:"collie_containers"`
	ProjectImages         []ReleaseProjectImage `json:"project_images"`
}

type ReleaseProjectImage struct {
	ProjectID string `json:"project_id"`
	Image     string `json:"image_digest"`
	Base      string `json:"base_digest"`
	Archived  bool   `json:"archived"`
}

func (r ReleaseDatabase) Idle() bool {
	return r.Runs == 0 && r.Leases == 0 && r.RepositoryOperations == 0 && r.EnvironmentOperations == 0 && r.CollieLaunches == 0
}

func InspectReleaseDatabase(spine string) (ReleaseDatabase, error) {
	r := ReleaseDatabase{Schema: substrate.CurrentSchemaID}
	inspection, err := inspectSpineReadOnly(spine)
	if err != nil {
		return r, err
	}
	if !inspection.exists || inspection.tables == 0 {
		r.Empty = true
		return r, nil
	}
	if !inspection.compatible {
		return r, Domainf("The database schema differs from this release. The installed release remains intact.")
	}
	r.UUID = inspection.uuid
	db, err := substrate.OpenReadOnly(spine)
	if err != nil {
		return r, err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil {
		return r, err
	}
	if integrity != "ok" {
		return r, Domainf("The database failed its integrity check. Preserve it and restore a verified checkpoint.")
	}
	var containers string
	err = db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM runs WHERE tier='pipeline' AND ended_at IS NULL),
		(SELECT COUNT(*) FROM leases WHERE run_id IS NOT NULL),
		(SELECT COUNT(*) FROM repository_executions WHERE ended_at IS NULL) +
		(SELECT COUNT(*) FROM merge_operations WHERE operation='apply' AND finished=0),
		(SELECT COUNT(*) FROM environment_operations WHERE status='running'),
		(SELECT COUNT(*) FROM collie_sessions WHERE status='active' AND current_launch_id IS NOT NULL AND current_container_id IS NULL),
		(SELECT json_group_array(current_container_id) FROM collie_sessions WHERE status='active' AND current_container_id IS NOT NULL)`).Scan(&r.Runs, &r.Leases, &r.RepositoryOperations, &r.EnvironmentOperations, &r.CollieLaunches, &containers)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(containers), &r.CollieContainers)
	if err != nil {
		return r, err
	}
	rows, err := db.Query(`SELECT e.project_id,e.selected_image_digest,e.selected_base_digest,p.status='archived' FROM project_environments e JOIN projects p ON p.id=e.project_id WHERE e.selected_image_digest<>'' ORDER BY e.project_id`)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var selected ReleaseProjectImage
		if err := rows.Scan(&selected.ProjectID, &selected.Image, &selected.Base, &selected.Archived); err != nil {
			return r, err
		}
		r.ProjectImages = append(r.ProjectImages, selected)
	}
	return r, rows.Err()
}
