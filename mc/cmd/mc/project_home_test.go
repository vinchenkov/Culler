package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mc/verbs"
)

func TestProjectHomeCreatesAndPreservesProjectStorage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	t.Setenv("MC_RUN_JSON", filepath.Join(home, "absent.json"))
	prepare := func(project string) verbs.PrivateDispatchPathIdentity {
		t.Helper()
		var out, diagnostics bytes.Buffer
		if code := runProjectHome([]string{project}, &out, &diagnostics); code != 0 {
			t.Fatalf("Project home: %d %s", code, diagnostics.String())
		}
		var root verbs.PrivateDispatchPathIdentity
		if err := json.Unmarshal(out.Bytes(), &root); err != nil {
			t.Fatal(err)
		}
		return root
	}
	first := prepare("fixture")
	tool := filepath.Join(first.Canonical, "tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf retained"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tool", filepath.Join(first.Canonical, "link")); err != nil {
		t.Fatal(err)
	}
	again := prepare("fixture")
	if first != again || first.OwnerUID != os.Getuid() || first.Device == "" || first.Inode == "" {
		t.Fatalf("Project home changed: %+v / %+v", first, again)
	}
	if info, err := os.Stat(tool); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("The executable changed: %v %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(first.Canonical, "link")); err != nil || target != "tool" {
		t.Fatalf("The symbolic link changed: %q %v", target, err)
	}
	if other := prepare("another"); other.Canonical == first.Canonical || other.Inode == first.Inode {
		t.Fatal("Two Projects share a home.")
	}
}

func TestProjectHomeRejectsAgentScopeAndInvalidIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MC_HOME", home)
	identity := filepath.Join(home, "run.json")
	t.Setenv("MC_RUN_JSON", identity)
	if err := os.WriteFile(identity, []byte(`{"tier":"pipeline","role":"worker","run_id":"fixture","project":"fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := runProjectHome([]string{"fixture"}, &out, &diagnostics); code == 0 {
		t.Fatal("An agent created host Project storage.")
	}
	if _, err := os.Stat(filepath.Join(home, "state")); !os.IsNotExist(err) {
		t.Fatal("The refused request created state.")
	}
	t.Setenv("MC_RUN_JSON", filepath.Join(home, "absent.json"))
	for _, project := range []string{"..", "../other", "/absolute", ""} {
		if code := runProjectHome([]string{project}, &out, &diagnostics); code == 0 {
			t.Fatalf("The Project identity %q was accepted.", project)
		}
	}
}

func TestProjectHomeRefusesSymlinkBeforeCreation(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	t.Setenv("MC_HOME", home)
	t.Setenv("MC_RUN_JSON", filepath.Join(home, "absent.json"))
	if err := os.Symlink(outside, filepath.Join(home, "state")); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := runProjectHome([]string{"fixture"}, &out, &diagnostics); code == 0 {
		t.Fatal("The Project home traversed a symbolic link.")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("The refused path changed: %v %v", entries, err)
	}
}
