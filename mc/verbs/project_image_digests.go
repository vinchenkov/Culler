package verbs

import "database/sql"

// ProjectImageDigests lists each image digest that a Project selects as its
// image or as its base. The published update keeps each listed image.
func ProjectImageDigests(db *sql.DB) (any, error) {
	rows, err := db.Query(`SELECT selected_image_digest AS digest FROM project_environments WHERE selected_image_digest<>''
UNION SELECT selected_base_digest FROM project_environments WHERE selected_base_digest<>'' ORDER BY digest`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	digests := []string{}
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return nil, classify(err)
		}
		digests = append(digests, digest)
	}
	if err := rows.Err(); err != nil {
		return nil, classify(err)
	}
	return map[string]any{"digests": digests}, nil
}
