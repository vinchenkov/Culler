package main

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"mc/verbs"
	"mc/wiki"
)

func runWikiRead(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "show" {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc wiki show --project <id>"))
	}
	fs := newFlags("mc wiki show")
	project := fs.String("project", "", "Project identifier")
	if err := fs.Parse(args[1:]); err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc wiki show --project <id>"))
	}
	if *project == "" || fs.NArg() != 0 {
		return writeVerbError(stdout, stderr, verbs.Usagef("the wiki read requires one Project"))
	}
	identity, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(identity, "wiki"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	frame, err := readHostProjectList()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	for _, row := range frame["projects"].([]map[string]any) {
		if row["id"] != *project {
			continue
		}
		root, _ := row["workspace_root"].(string)
		if !filepath.IsAbs(root) || root == "/" {
			return writeVerbError(stdout, stderr, verbs.Domainf("the Project requires an accepted repository"))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		view, err := wiki.Read(ctx, root, *project)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("read the accepted wiki: %v", err))
		}
		if err := writeJSON(stdout, view); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		return 0
	}
	return writeVerbError(stdout, stderr, verbs.Domainf("the Project is absent"))
}
