package main

import (
	"fmt"
	"os"
	"path/filepath"

	"mc/boundary"
)

func try(label, path string) {
	if path == "" {
		fmt.Printf("SKIP  %-16s (empty)\n", label)
		return
	}
	_, err := boundary.ResolveSource(path)
	if err != nil {
		fmt.Printf("FAIL  %-16s %s\n        -> %v\n", label, path, err)
		return
	}
	fmt.Printf("ok    %-16s %s\n", label, path)
}

func main() {
	home := os.Getenv("HOME")
	for _, rel := range []string{
		".ssh", ".aws", ".azure", ".config", ".docker", ".gnupg", ".kube", ".codex", ".claude",
		"Library/Keychains", ".netrc", ".npmrc", ".pypirc", ".git-credentials",
	} {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			continue
		}
		try("home:"+rel, p)
	}
	for _, p := range os.Args[1:] {
		try("arg", p)
	}
}
