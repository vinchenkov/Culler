package main

import (
	"context"
	"database/sql"
	"time"

	"mc/repository"
)

// acceptedCommitLimit is the time limit of one read of the accepted commit.
// A set of reads at the same time shares one limit.
var acceptedCommitLimit = 5 * time.Second

// acceptedCommitAt reads the accepted commit of a Project: the commit of the
// branch main in the repository at root. It returns "" for an empty root, for
// each error, and for no result in the time limit.
func acceptedCommitAt(root string) string {
	return acceptedCommitsAt(map[string]string{"": root})[""]
}

func readAcceptedCommit(ctx context.Context, root string) string {
	if root == "" {
		return ""
	}
	commit, err := repository.Git{Root: root}.Branch(ctx, "main")
	if err != nil {
		return ""
	}
	return commit
}

// acceptedCommitsAt reads the accepted commit at each root at the same time
// under one shared time limit. roots holds the repository root of each
// Project. A root with no result in that time has the value "".
func acceptedCommitsAt(roots map[string]string) map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), acceptedCommitLimit)
	defer cancel()
	type observation struct{ project, commit string }
	results := make(chan observation, len(roots))
	commits := make(map[string]string, len(roots))
	for project, root := range roots {
		commits[project] = ""
		go func() { results <- observation{project, readAcceptedCommit(ctx, root)} }()
	}
	for range roots {
		select {
		case result := <-results:
			commits[result.project] = result.commit
		case <-ctx.Done():
			return commits
		}
	}
	return commits
}

// hostAcceptedCommits reads the accepted commit of each Project from its
// repository on this host. roots holds the repository root of each Project.
type hostAcceptedCommits struct {
	roots map[string]string
}

func (h hostAcceptedCommits) All(_ context.Context, projects []string) map[string]string {
	roots := make(map[string]string, len(projects))
	for _, project := range projects {
		roots[project] = h.roots[project]
	}
	return acceptedCommitsAt(roots)
}

func (h hostAcceptedCommits) One(_ context.Context, project string) string {
	return acceptedCommitAt(h.roots[project])
}

// loadProjectRoots reads the repository root of each Project.
func loadProjectRoots(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT w.id, COALESCE(s.workspace_root,'') FROM projects w LEFT JOIN sandbox_profiles s ON s.id=w.sandbox_profile ORDER BY w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roots := map[string]string{}
	for rows.Next() {
		var project, root string
		if err := rows.Scan(&project, &root); err != nil {
			return nil, err
		}
		roots[project] = root
	}
	return roots, rows.Err()
}

// projectRootsOf returns the repository root of each row of a Project list.
func projectRootsOf(listed map[string]any) map[string]string {
	rows, _ := listed["projects"].([]map[string]any)
	roots := make(map[string]string, len(rows))
	for _, row := range rows {
		project, _ := row["id"].(string)
		root, _ := row["workspace_root"].(string)
		if project != "" {
			roots[project] = root
		}
	}
	return roots
}
