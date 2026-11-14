package verbs

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"mc/dispatch"
)

func TestRepositoryOperationsConcurrentInOneRepository(t *testing.T) {
	f := newDeliveryFixture(t)
	merged := f.add("Merge while other operations run")
	f.pass(merged, "merged.txt", "merged\n")
	candidate := f.merge(merged, "prepare")
	f.approve(merged)
	before := srcGit(t, f.workspace, "rev-parse", "culler")
	apply := f.envelope(startOperation(t, f.db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: merged, Operation: dispatch.OperationApply}))

	captured := f.add("Capture while other operations run")
	run, root, id := f.run(captured, "worker")
	commit := f.commit(root, "captured.txt", "captured\n")
	if _, err := Complete(f.db, id, CompleteArgs{Task: captured, Run: run, Status: "worked", Commit: commit}); err != nil {
		t.Fatal(err)
	}
	capture := f.envelope(startOperation(t, f.db, dispatch.RepositoryOperation{Project: "ws-test", TaskID: captured, Operation: dispatch.OperationCapture, RunID: run}))

	allocated := f.add("Allocate while other operations run")
	allocate := RepositoryEnvelope{Operation: "allocate", TaskID: allocated, RunID: "allocate-run", WorkspaceRoot: f.workspace,
		RunRoot: filepath.Join(f.root, "allocate-run"), DestinationBranch: "culler"}

	envelopes := []RepositoryEnvelope{apply, capture, allocate}
	results := make([]RepositoryResult, len(envelopes))
	errs := make([]error, len(envelopes))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, envelope := range envelopes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = executeRepository(context.Background(), envelope)
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("the concurrent %s operation failed: %v", envelopes[i].Operation, err)
		}
	}

	if results[0].Applied == nil || results[0].Applied.Commit != candidate.Commit {
		t.Fatalf("the merge returned %+v", results[0])
	}
	if main := srcGit(t, f.workspace, "rev-parse", "culler"); main != candidate.Commit {
		t.Fatalf("culler is %s, want the candidate %s", main, candidate.Commit)
	}
	version := fmt.Sprintf("refs/mc/tasks/%d/versions/%s", captured, run)
	if results[1].RetainedRef != version || srcGit(t, f.workspace, "rev-parse", version) != commit {
		t.Fatalf("the capture retained %s", results[1].RetainedRef)
	}
	input := srcGit(t, f.workspace, "rev-parse", fmt.Sprintf("refs/mc/tasks/%d/inputs/allocate-run", allocated))
	if input != results[2].InputCommit || (input != before && input != candidate.Commit) {
		t.Fatalf("the allocation retained %s, the result names %s", input, results[2].InputCommit)
	}
}
