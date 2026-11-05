package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func reconcileFixture(t *testing.T) (Git, Application) {
	t.Helper()
	source, initial := fixture(t)
	worker := runTest(t, source, initial)
	writeTest(t, worker.Root, "complete.txt", "checked contribution\n")
	gitTest(t, worker, "add", ".")
	gitTest(t, worker, "commit", "-qm", "Add the contribution")
	version := gitTest(t, worker, "rev-parse", "HEAD")
	candidate, err := Prepare(context.Background(), worker, version, "main", initial, "Test", "test@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Retain(context.Background(), worker.Root, candidate.Commit, "refs/mc/tasks/1/candidates/1"); err != nil {
		t.Fatal(err)
	}
	return source, Application{Authorization: "reconcile-execution", Candidate: candidate}
}

func TestReconcileApplicationPreservesEveryDurableBoundary(t *testing.T) {
	for _, boundary := range []string{"absent", "after-journal", "after-checkout", "after-reference", "after-receipt", "after-update"} {
		t.Run(boundary, func(t *testing.T) {
			source, authorization := reconcileFixture(t)
			ctx := context.Background()
			if boundary != "absent" {
				injected := errors.New("the application process stopped")
				_, err := apply(ctx, source.Root, authorization, func(at string) error {
					if at == boundary {
						return injected
					}
					return nil
				})
				if !errors.Is(err, injected) {
					t.Fatalf("the application missed %s: %v", boundary, err)
				}
			}
			before := gitTest(t, source, "rev-parse", "main")
			result, err := ReconcileApplication(ctx, source.Root, authorization)
			if after := gitTest(t, source, "rev-parse", "main"); after != before {
				t.Fatal("reconciliation advanced the destination")
			}
			switch boundary {
			case "absent":
				if err != nil || result != nil {
					t.Fatalf("the absent application produced a receipt: %+v, %v", result, err)
				}
				if got := gitTest(t, source, "status", "--porcelain"); got != "" {
					t.Fatalf("the absent application changed the checkout: %s", got)
				}
			case "after-journal", "after-checkout":
				if !errors.Is(err, ErrOperatorCheckout) || result != nil {
					t.Fatalf("an unresolved checkout escaped the operator hold: %+v, %v", result, err)
				}
				for _, path := range []string{"index.lock", "mc/applications/reconcile-execution.json"} {
					if _, err := os.Stat(filepath.Join(source.Root, ".git", path)); err != nil {
						t.Fatalf("reconciliation discarded %s: %v", path, err)
					}
				}
			default:
				if err != nil || result == nil || !result.Recovered || result.Authorization != authorization.Authorization ||
					result.Commit != authorization.Candidate.Commit || result.CleanupError != "" {
					t.Fatalf("the completed application lost its receipt: %+v, %v", result, err)
				}
				if got := gitTest(t, source, "status", "--porcelain"); got != "" {
					t.Fatalf("the recovered checkout differs: %s", got)
				}
				again, err := ReconcileApplication(ctx, source.Root, authorization)
				if err != nil || again == nil || *again != *result {
					t.Fatalf("the repeated receipt changed: %+v, %v", again, err)
				}
			}
		})
	}
}

func TestReconcileApplicationRefusesAnotherAuthorization(t *testing.T) {
	source, authorization := reconcileFixture(t)
	if _, err := Apply(context.Background(), source.Root, authorization); err != nil {
		t.Fatal(err)
	}
	authorization.Candidate.ConflictPaths = []string{"another.txt"}
	result, err := ReconcileApplication(context.Background(), source.Root, authorization)
	if !errors.Is(err, ErrOperatorCheckout) || result != nil {
		t.Fatalf("reconciliation accepted another authorization: %+v, %v", result, err)
	}
}
