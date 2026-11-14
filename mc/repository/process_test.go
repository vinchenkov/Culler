package repository

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestApplicationProcessBoundaries(t *testing.T) {
	if path := os.Getenv("MC_REPOSITORY_CRASH_CASE"); path != "" {
		var request struct {
			Root, Boundary string
			Application    Application
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		_, err = apply(context.Background(), request.Root, request.Application, func(at string) error {
			if at == request.Boundary {
				fmt.Fprintln(os.Stdout, "ready")
				var b [1]byte
				_, err := os.Stdin.Read(b[:])
				return err
			}
			return nil
		})
		t.Fatalf("the process passed its crash boundary: %v", err)
	}
	for _, branch := range []string{"culler", "feature/task-1"} {
		for _, boundary := range []string{"after-journal", "after-reference", "after-receipt"} {
			t.Run(branch+"/"+boundary, func(t *testing.T) {
				source, initial := fixture(t)
				if branch != "culler" {
					gitTest(t, source, "branch", branch, initial)
				}
				worker := runTest(t, source, initial)
				writeTest(t, worker.Root, "complete.txt", "the complete contribution\n")
				gitTest(t, worker, "add", ".")
				gitTest(t, worker, "commit", "-qm", "Complete the contribution")
				c, err := Prepare(context.Background(), worker, gitTest(t, worker, "rev-parse", "HEAD"), branch, initial, "Test", "test@example.invalid")
				if err != nil {
					t.Fatal(err)
				}
				if err := source.Retain(context.Background(), worker.Root, c.Commit, "refs/mc/tasks/1/candidates/1"); err != nil {
					t.Fatal(err)
				}
				a := Application{Authorization: "process-crash", Candidate: c}
				body, _ := json.Marshal(struct {
					Root, Boundary string
					Application    Application
				}{source.Root, boundary, a})
				request := filepath.Join(t.TempDir(), "request.json")
				if err := os.WriteFile(request, body, 0600); err != nil {
					t.Fatal(err)
				}
				binary, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, "-test.run=^TestApplicationProcessBoundaries$")
				cmd.Env = append(os.Environ(), "MC_REPOSITORY_CRASH_CASE="+request)
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				stdin, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer stdin.Close()
				cmd.Stderr = os.Stderr
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				line, err := bufio.NewReader(stdout).ReadString('\n')
				if err != nil || line != "ready\n" {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatalf("the boundary produced %q, %v", line, err)
				}
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = cmd.Wait()
				lock := filepath.Join(source.Root, ".git", "refs", "heads", branch+".lock")
				deadline := time.Now().Add(3 * time.Second)
				for {
					if _, err := os.Stat(lock); os.IsNotExist(err) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("Git did not release the interrupted reference reservation")
					}
					time.Sleep(10 * time.Millisecond)
				}
				result, err := Apply(context.Background(), source.Root, a)
				if err != nil || result.CleanupError != "" {
					t.Fatalf("recovery = %+v, %v", result, err)
				}
				if got := gitTest(t, source, "rev-parse", branch); got != c.Commit {
					t.Fatal("recovery changed the applied commit")
				}
				if got := gitTest(t, source, "status", "--porcelain"); got != "" {
					t.Fatalf("recovery left checkout changes: %s", got)
				}
			})
		}
	}
}

func TestApplicationRecoversAPartialCheckoutWithoutDiscardingOperatorEdits(t *testing.T) {
	for _, operatorEdit := range []bool{false, true} {
		t.Run(fmt.Sprint(operatorEdit), func(t *testing.T) {
			source, initial := fixture(t)
			worker := runTest(t, source, initial)
			writeTest(t, worker.Root, "settings.txt", "settings = { limit: 20, label: new }\n")
			writeTest(t, worker.Root, "complete.txt", "the complete contribution\n")
			gitTest(t, worker, "add", ".")
			gitTest(t, worker, "commit", "-qm", "Complete both changes")
			c, err := Prepare(context.Background(), worker, gitTest(t, worker, "rev-parse", "HEAD"), "culler", initial, "Test", "test@example.invalid")
			if err != nil {
				t.Fatal(err)
			}
			if err := source.Retain(context.Background(), worker.Root, c.Commit, "refs/mc/tasks/1/candidates/1"); err != nil {
				t.Fatal(err)
			}
			a := Application{Authorization: "partial-checkout", Candidate: c}
			stop := errors.New("the checkout stopped")
			_, err = apply(context.Background(), source.Root, a, func(at string) error {
				if at == "after-journal" {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) {
				t.Fatal("the test missed the durable journal")
			}
			content := "the complete contribution\n"
			if operatorEdit {
				content = "the operator edit\n"
			}
			writeTest(t, source.Root, "complete.txt", content)
			result, err := Apply(context.Background(), source.Root, a)
			if operatorEdit {
				if !errors.Is(err, ErrOperatorCheckout) {
					t.Fatalf("the operator edit was accepted: %v", err)
				}
				body, _ := os.ReadFile(filepath.Join(source.Root, "complete.txt"))
				if string(body) != content {
					t.Fatal("recovery replaced the operator edit")
				}
				if gitTest(t, source, "rev-parse", "culler") != initial {
					t.Fatal("the obstructed checkout advanced culler")
				}
			} else if err != nil || result.CleanupError != "" || gitTest(t, source, "status", "--porcelain") != "" {
				t.Fatalf("partial recovery: %+v, %v", result, err)
			}
		})
	}
}

func TestRepositoryStorageMeasurement(t *testing.T) {
	source, initial := fixture(t)
	writeTest(t, source.Root, "large.txt", string(make([]byte, 2<<20)))
	gitTest(t, source, "add", ".")
	gitTest(t, source, "commit", "-qm", "Add a representative object")
	commit := gitTest(t, source, "rev-parse", "HEAD")
	estimate, err := source.RequiredBytes(context.Background(), commit, initial)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	run := runTest(t, source, commit, initial)
	var measured int64
	err = filepath.WalkDir(run.Root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err == nil {
			measured += info.Size()
		}
		return err
	})
	if err != nil || measured < 2<<20 || estimate < measured {
		t.Fatalf("estimate=%d, actual=%d, error=%v", estimate, measured, err)
	}
	if err := RequireFreeSpace(run.Root, 1<<62); err == nil {
		t.Fatal("the impossible disk requirement succeeded")
	}
	t.Logf("Repository preparation took %s. The estimate was %d bytes. The temporary repository used %d bytes.", time.Since(started), estimate, measured)
}
