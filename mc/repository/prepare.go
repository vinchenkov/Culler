package repository

import (
	"context"
	"fmt"
	"strings"
)

// Candidate is the Git merge of a submitted commit with the destination commit.
// A merged candidate goes to review. A conflict returns the task to the Worker.
type Candidate struct {
	SubmittedCommit   string            `json:"submitted_commit"`
	DestinationBranch string            `json:"destination_branch"`
	DestinationCommit string            `json:"destination_commit"`
	Commit            string            `json:"commit,omitempty"`
	Status            string            `json:"status"`
	ConflictPaths     []string          `json:"conflict_paths,omitempty"`
	Changes           *CandidateChanges `json:"changes,omitempty"`
}

// CandidateChanges locates the stored change data of a merged candidate: the
// range from the destination commit to the candidate commit.
type CandidateChanges struct {
	Candidate ChangeRecord `json:"candidate"`
}

// ChangeRecord locates the stored file inventory and patch of one Git range.
type ChangeRecord struct {
	FromCommit      string `json:"from_commit"`
	ToCommit        string `json:"to_commit"`
	InventoryPath   string `json:"inventory_path"`
	InventorySHA256 string `json:"inventory_sha256"`
	PatchPath       string `json:"patch_path"`
	PatchSHA256     string `json:"patch_sha256"`
	PatchBytes      int64  `json:"patch_bytes"`
	Files           int    `json:"files"`
}

// Prepare runs only in a container with an isolated repository.
// Its caller gives the container no accepted branch or operator checkout write access.
func Prepare(ctx context.Context, g Git, submitted, branch, destination, name, email string) (Candidate, error) {
	c := Candidate{SubmittedCommit: submitted, DestinationBranch: branch, DestinationCommit: destination}
	var err error
	if !ValidCommit(submitted) || !ValidCommit(destination) || name == "" || email == "" {
		return c, fmt.Errorf("merge preparation requires exact commits and a Git author")
	}
	if _, err := g.output(ctx, "check-ref-format", "refs/heads/"+branch); err != nil {
		return c, err
	}
	if _, err := g.output(ctx, "checkout", "--quiet", "--detach", destination); err != nil {
		return c, err
	}
	if g.Ancestor(ctx, destination, submitted) {
		_, err = g.output(ctx, "checkout", "--quiet", "--detach", submitted)
	} else {
		_, err = g.output(ctx, "-c", "user.name="+name, "-c", "user.email="+email,
			"-c", "commit.gpgSign=false", "-c", "merge.gpgSign=false",
			"merge", "--no-ff", "--no-edit", "--no-verify", "--no-gpg-sign", submitted)
	}
	if err != nil {
		paths, readErr := g.command(ctx, nil, "diff", "--name-only", "--diff-filter=U", "-z")
		if readErr != nil || len(paths) == 0 {
			return c, err
		}
		c.Status = "conflict"
		c.ConflictPaths = strings.Split(strings.TrimSuffix(string(paths), "\x00"), "\x00")
		return c, nil
	}
	c.Commit, err = g.Commit(ctx, "HEAD")
	if err != nil {
		return c, err
	}
	if !g.Ancestor(ctx, submitted, c.Commit) || !g.Ancestor(ctx, destination, c.Commit) {
		return c, fmt.Errorf("the candidate does not preserve both merge inputs")
	}
	c.Status = "merged"
	return c, nil
}

// Validate accepts a merged candidate of exact commits.
func (c Candidate) Validate() error {
	if c.Status != "merged" || !ValidCommit(c.Commit) || !ValidCommit(c.SubmittedCommit) || !ValidCommit(c.DestinationCommit) {
		return fmt.Errorf("the candidate requires a Git merge of exact commits")
	}
	return nil
}
