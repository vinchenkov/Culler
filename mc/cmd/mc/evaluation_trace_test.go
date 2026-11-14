package main

import (
	"os"
	"path/filepath"
	"testing"

	"mc/evalcase"
	"mc/trace"
)

func TestEvaluationCapturesMaskedNativeTrace(t *testing.T) {
	home := t.TempDir()
	store := evalcase.Store{Root: filepath.Join(home, "cases")}
	m := evalcase.Manifest{FormatVersion: 1, CaseID: "native-case", Authorized: true,
		Identity: evalcase.Identity{Run: "packager"}, Absences: map[string]string{}}
	record := map[string]any{"session_path": "sessions/packager", "trace_filename": trace.OpenCodeDatabase}
	original := filepath.Join(home, "sessions/packager", trace.OpenCodeDatabase)
	copy := filepath.Join(home, "agentsview/inspection/packager", trace.OpenCodeDatabase)
	for path, text := range map[string]string{original: "private original", copy: "masked native database"} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := captureEvaluationTraceRecord(home, store, &m, record); err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 1 || m.Artifacts[0].Path != "execution/trace.db" || m.Artifacts[0].MediaType != "application/vnd.sqlite3" {
		t.Fatalf("native artifact: %+v", m.Artifacts)
	}
	body, err := os.ReadFile(filepath.Join(store.Root, m.CaseID, "execution/trace.db"))
	if err != nil || string(body) != "masked native database" {
		t.Fatalf("captured trace: %q, %v", body, err)
	}
	body, err = os.ReadFile(original)
	if err != nil || string(body) != "private original" {
		t.Fatalf("original trace changed: %q, %v", body, err)
	}
	if err := os.Remove(copy); err != nil {
		t.Fatal(err)
	}
	m.Artifacts = nil
	if err := captureEvaluationTraceRecord(home, store, &m, record); err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 0 || m.Absences["execution_trace"] == "" {
		t.Fatal("an absent masked copy exposed the original")
	}
}
