package repository

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var ErrDestinationChanged = errors.New("the destination commit changed")
var ErrOperatorCheckout = errors.New("the operator checkout requires a decision")

// CheckoutRefusal is a condition of the operator checkout that stops the merge.
// Its text names each path and the action of the operator in plain words.
type CheckoutRefusal struct{ Text string }

func (r *CheckoutRefusal) Error() string { return r.Text }
func (r *CheckoutRefusal) Unwrap() error { return ErrOperatorCheckout }

func refuse(format string, args ...any) error {
	return &CheckoutRefusal{Text: fmt.Sprintf(format, args...)}
}

// Preflight tests the merge of a candidate before review. It checks the
// destination access and the Git two-way merge in the operator checkout. It
// changes no file in the operator checkout.
func Preflight(ctx context.Context, root string, c Candidate) error {
	if err := c.Validate(); err != nil {
		return err
	}
	g, common, cleanup, err := applicationView(ctx, root, c.DestinationBranch)
	if err != nil {
		return err
	}
	defer cleanup()
	if !g.Ancestor(ctx, c.SubmittedCommit, c.Commit) || !g.Ancestor(ctx, c.DestinationCommit, c.Commit) {
		return fmt.Errorf("the candidate does not preserve both authorized inputs")
	}
	live, err := g.Branch(ctx, c.DestinationBranch)
	if err != nil {
		return err
	}
	if live != c.DestinationCommit {
		return ErrDestinationChanged
	}
	checkedOut, err := checkoutState(common, c.DestinationBranch)
	if err != nil {
		return err
	}
	if checkedOut {
		g.index = filepath.Join(g.directory, "candidate-index")
		if err := loadIndex(ctx, g, common, c); err != nil {
			return err
		}
		if err := mergeRefusal(ctx, g, c); err != nil {
			return err
		}
	}
	_, abort, err := reserveUpdate(ctx, g, "refs/heads/"+c.DestinationBranch, c)
	if err != nil {
		return err
	}
	abort()
	return nil
}

type Application struct {
	Authorization string    `json:"authorization"`
	Candidate     Candidate `json:"candidate"`
}

type Applied struct {
	Authorization string `json:"authorization"`
	Commit        string `json:"commit"`
	Recovered     bool   `json:"recovered"`
	CleanupError  string `json:"cleanup_error,omitempty"`
}

// Apply receives an authorization from mc and executes no project code.
// A durable journal precedes the update. A receipt follows the branch update.
func Apply(ctx context.Context, repository string, a Application) (Applied, error) {
	return apply(ctx, repository, a, nil)
}

func apply(ctx context.Context, root string, a Application, boundary func(string) error) (Applied, error) {
	result := Applied{Authorization: a.Authorization, Commit: a.Candidate.Commit}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(a.Authorization) {
		return result, fmt.Errorf("the update authorization is invalid")
	}
	c := a.Candidate
	if c.Validate() != nil {
		return result, fmt.Errorf("the authorization requires a merged candidate")
	}
	g, common, cleanup, err := applicationView(ctx, root, c.DestinationBranch)
	if err != nil {
		return result, err
	}
	defer cleanup()
	journalPath := filepath.Join(common, "mc", "applications", a.Authorization+".json")
	journal, err := readJournal(journalPath, a)
	if err != nil {
		return result, err
	}
	if !g.Ancestor(ctx, c.SubmittedCommit, c.Commit) || !g.Ancestor(ctx, c.DestinationCommit, c.Commit) {
		return result, fmt.Errorf("the candidate does not preserve both authorized inputs")
	}
	receiptRef := "refs/mc/applications/" + a.Authorization
	body, err := json.Marshal(a)
	if err != nil {
		return result, err
	}
	receipt, err := g.command(ctx, body, "hash-object", "-w", "--stdin")
	if err != nil {
		return result, err
	}
	receiptID := strings.TrimSpace(string(receipt))
	if recorded, err := g.output(ctx, "rev-parse", "--verify", receiptRef); err == nil {
		if recorded != receiptID {
			return result, fmt.Errorf("the authorization already identifies another update")
		}
		result.Recovered = true
		if err := recoverAppliedIndex(ctx, g, common, journalPath, journal); err != nil {
			result.CleanupError = err.Error()
		}
		return result, nil
	}
	live, err := g.Branch(ctx, c.DestinationBranch)
	if err != nil {
		return result, err
	}
	if journal != nil && g.Ancestor(ctx, c.Commit, live) {
		if _, err := g.output(ctx, "update-ref", receiptRef, receiptID, strings.Repeat("0", len(receiptID))); err != nil {
			return result, err
		}
		result.Recovered = true
		if err := recoverAppliedIndex(ctx, g, common, journalPath, journal); err != nil {
			result.CleanupError = err.Error()
		}
		return result, nil
	}
	if live != c.DestinationCommit {
		return result, ErrDestinationChanged
	}
	ref := "refs/heads/" + c.DestinationBranch
	checkedOut, err := checkoutState(common, c.DestinationBranch)
	if err != nil {
		return result, err
	}
	var indexLock *os.File
	keepIndex := journal != nil
	if checkedOut {
		lockPath := filepath.Join(common, "index.lock")
		if journal != nil {
			if !journal.owns(lockPath) {
				return result, fmt.Errorf("the index reservation belongs to another operation")
			}
			indexLock, err = os.OpenFile(lockPath, os.O_WRONLY, 0600)
		} else {
			indexLock, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if os.IsExist(err) {
				return result, refuse("The operator checkout has the file .git/index.lock. A running Git command holds it, or a stopped Git command left it. When no Git command runs, remove the file.")
			}
		}
		if err != nil {
			return result, refuse("Culler cannot write the file .git/index.lock in the Project repository. Make sure that your user can write the .git folder.")
		}
		defer func() {
			indexLock.Close()
			if !keepIndex {
				os.Remove(indexLock.Name())
			}
		}()
		g.index = filepath.Join(g.directory, "candidate-index")
		if err := loadIndex(ctx, g, common, c); err != nil {
			return result, err
		}
		if journal == nil {
			err = mergeRefusal(ctx, g, c)
		} else {
			err = resumeCheckout(ctx, g, c)
		}
		if err != nil {
			return result, err
		}
	}
	if boundary != nil {
		if err := boundary("before-update"); err != nil {
			return result, err
		}
	}
	commit, abort, err := reserveUpdate(ctx, g, ref, c)
	if err != nil {
		return result, err
	}
	defer abort()
	if boundary != nil {
		if err := boundary("reserved"); err != nil {
			return result, err
		}
	}
	if journal == nil {
		journal = &applicationJournal{Application: a}
		if checkedOut {
			device, inode, err := indexIdentity(indexLock.Name())
			if err != nil {
				return result, err
			}
			journal.IndexDevice, journal.IndexInode = device, inode
		}
		if err := writeJournal(journalPath, *journal); err != nil {
			return result, err
		}
		keepIndex = checkedOut
	}
	if boundary != nil {
		if err := boundary("after-journal"); err != nil {
			return result, err
		}
	}
	if checkedOut {
		if _, err := g.output(ctx, "read-tree", "-u", "-m", c.DestinationCommit, c.Commit); err != nil {
			return result, fmt.Errorf("the operator files prevent the update: %w", err)
		}
		data, err := os.ReadFile(g.index)
		if err != nil {
			return result, err
		}
		if err := indexLock.Truncate(0); err != nil {
			return result, err
		}
		if _, err := indexLock.Seek(0, 0); err != nil {
			return result, err
		}
		if _, err := indexLock.Write(data); err != nil {
			return result, err
		}
		if err := indexLock.Sync(); err != nil {
			return result, err
		}
		if boundary != nil {
			if err := boundary("after-checkout"); err != nil {
				return result, err
			}
		}
	}
	if err := commit(); err != nil {
		return result, fmt.Errorf("the conditional destination update failed: %w", err)
	}
	if boundary != nil {
		if err := boundary("after-reference"); err != nil {
			return result, err
		}
	}
	if _, err := g.output(ctx, "update-ref", receiptRef, receiptID, strings.Repeat("0", len(receiptID))); err != nil {
		return result, err
	}
	if boundary != nil {
		if err := boundary("after-receipt"); err != nil {
			return result, err
		}
	}
	if checkedOut {
		indexLock.Close()
		if err := os.Rename(indexLock.Name(), filepath.Join(common, "index")); err != nil {
			result.CleanupError = "the destination advanced, but the checkout index requires recovery: " + err.Error()
		}
		if result.CleanupError == "" {
			if err := syncDirectory(common); err != nil {
				result.CleanupError = err.Error()
			}
			if err := os.Remove(journalPath); err != nil {
				result.CleanupError = err.Error()
			}
			keepIndex = false
		}
	} else if err := os.Remove(journalPath); err != nil {
		result.CleanupError = err.Error()
	}
	if boundary != nil {
		if err := boundary("after-update"); err != nil {
			return result, err
		}
	}
	return result, nil
}

func reserveUpdate(ctx context.Context, g Git, ref string, c Candidate) (func() error, func(), error) {
	cmd := g.process(ctx, "update-ref", "--stdin")
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		input.Close()
		return nil, nil, err
	}
	done := false
	abort := func() {
		if !done {
			input.Close()
			_ = cmd.Wait()
			done = true
		}
	}
	transaction := fmt.Sprintf("start\nupdate %s %s %s\nprepare\n", ref, c.Commit, c.DestinationCommit)
	if _, err := input.Write([]byte(transaction)); err != nil {
		abort()
		return nil, nil, err
	}
	scanner := bufio.NewScanner(output)
	for _, expected := range []string{"start: ok", "prepare: ok"} {
		if !scanner.Scan() || scanner.Text() != expected {
			abort()
			if live, err := g.Branch(ctx, c.DestinationBranch); err == nil && live != c.DestinationCommit {
				return nil, nil, ErrDestinationChanged
			}
			return nil, nil, fmt.Errorf("reserve the destination update: %s", stderr.String())
		}
	}
	commit := func() error {
		if _, err := input.Write([]byte("commit\n")); err != nil {
			return err
		}
		input.Close()
		err := cmd.Wait()
		done = true
		if err != nil {
			return fmt.Errorf("commit the destination update: %w: %s", err, stderr.String())
		}
		return nil
	}
	return commit, abort, nil
}

func applicationView(ctx context.Context, root, branch string) (Git, string, func(), error) {
	g := Git{Root: root}
	none := func() {}
	if _, err := g.output(ctx, "check-ref-format", "refs/heads/"+branch); err != nil {
		return g, "", none, err
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return g, "", none, err
	}
	common := filepath.Join(canonical, ".git")
	if info, err := os.Lstat(common); err != nil || !info.IsDir() {
		return g, "", none, fmt.Errorf("the Project requires a primary Git repository")
	}
	format, err := g.output(ctx, "rev-parse", "--show-object-format")
	if err != nil {
		return g, "", none, err
	}
	tmp, err := os.MkdirTemp("", "mc-apply-")
	if err != nil {
		return g, "", none, err
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if _, err := (Git{Root: tmp}).output(ctx, "init", "--quiet", "--bare", "--template=", "--object-format="+format); err != nil {
		cleanup()
		return g, "", none, err
	}
	for _, name := range []string{"objects", "refs", "packed-refs", "shallow"} {
		if _, err := os.Lstat(filepath.Join(common, name)); os.IsNotExist(err) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(tmp, name)); err != nil {
			cleanup()
			return g, "", none, err
		}
		if err := os.Symlink(filepath.Join(common, name), filepath.Join(tmp, name)); err != nil {
			cleanup()
			return g, "", none, err
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "HEAD"), []byte("ref: refs/heads/"+branch+"\n"), 0600); err != nil {
		cleanup()
		return g, "", none, err
	}
	g.Root, g.directory = canonical, tmp
	return g, common, cleanup, nil
}
