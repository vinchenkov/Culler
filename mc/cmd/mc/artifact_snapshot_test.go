package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func requireArtifactSnapshot(t *testing.T, source, stored string) {
	t.Helper()
	if stored == source || filepath.Ext(stored) != filepath.Ext(source) {
		t.Fatalf("the terminal requires a separate report with the same file type: %q", stored)
	}
	want, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(stored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("the recorded report differs from the submitted report")
	}
}
