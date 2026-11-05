package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ReconcileApplication observes an earlier authorization without advancing the destination.
// A nil result proves that the authorization has neither a receipt nor a journal.
func ReconcileApplication(ctx context.Context, root string, a Application) (*Applied, error) {
	c := a.Candidate
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(a.Authorization) || c.Validate() != nil {
		return nil, fmt.Errorf("the reconciliation requires an exact merged authorization")
	}
	g, common, cleanup, err := applicationView(ctx, root, c.DestinationBranch)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	journalPath := filepath.Join(common, "mc", "applications", a.Authorization+".json")
	journal, err := readJournal(journalPath, a)
	if err != nil {
		return nil, refuse("Culler cannot read the journal file .git/mc/applications/%s.json of a merge into %s. Make sure that your user can read the file. If the file is damaged and the branch does not hold the commit %s, remove the file.",
			a.Authorization, c.DestinationBranch, c.Commit)
	}
	body, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	receiptRef := "refs/mc/applications/" + a.Authorization
	_, err = g.output(ctx, "show-ref", "--verify", "--quiet", receiptRef)
	if err == nil {
		receipt, err := g.command(ctx, nil, "cat-file", "blob", receiptRef)
		if err != nil || !bytes.Equal(receipt, body) {
			return nil, refuse("The reference %s does not match the merge that Culler recorded. If the branch %s does not hold the commit %s, delete the reference.",
				receiptRef, c.DestinationBranch, c.Commit)
		}
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, err
		}
		if journal == nil {
			return nil, nil
		}
		live, err := g.Branch(ctx, c.DestinationBranch)
		if err != nil {
			return nil, err
		}
		if !g.Ancestor(ctx, c.Commit, live) {
			return nil, refuse("A merge into %s stopped before it updated the branch. If the file .git/index.lock exists, remove it. Then discard each change in the checkout that you did not make, and remove the file .git/mc/applications/%s.json.",
				c.DestinationBranch, a.Authorization)
		}
		receipt, err := g.command(ctx, body, "hash-object", "-w", "--stdin")
		if err != nil {
			return nil, err
		}
		receiptID := strings.TrimSpace(string(receipt))
		if _, err := g.output(ctx, "update-ref", receiptRef, receiptID, strings.Repeat("0", len(receiptID))); err != nil {
			return nil, err
		}
	}
	result := &Applied{Authorization: a.Authorization, Commit: c.Commit, Recovered: true}
	if err := recoverAppliedIndex(ctx, g, common, journalPath, journal); err != nil {
		result.CleanupError = err.Error()
	}
	return result, nil
}
