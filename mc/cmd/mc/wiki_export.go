package main

import (
	"context"
	"io"
	"os/signal"
	"regexp"
	"syscall"

	"mc/deployment"
	"mc/verbs"
	"mc/wiki"
)

var runnerImageReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)

// runWikiExport runs on the host: it reads Project repositories and starts
// the export command in the runner image.
func runWikiExport(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("mc __wiki-export")
	image := fs.String("image", "", "runner image reference")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *image == "" {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __wiki-export --image <ref>"))
	}
	if !runnerImageReference.MatchString(*image) {
		return writeVerbError(stdout, stderr, verbs.Usagef("the runner image reference is invalid"))
	}
	identity, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(identity, "mc __wiki-export"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("resolve MC_HOME: %v", err))
	}
	frame, err := readHostProjectList()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("read the Projects: %v", err))
	}
	projects := []wiki.ExportProject{}
	for _, row := range frame["projects"].([]map[string]any) {
		id, _ := row["id"].(string)
		root, _ := row["workspace_root"].(string)
		projects = append(projects, wiki.ExportProject{ID: id, Root: root})
	}
	// A stop request of the resident stops and removes the export container.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	exporter := wiki.Exporter{Root: deployment.WikiExportRoot(home), Image: *image, Run: func(ctx context.Context, container wiki.ExportContainer) error {
		release, err := admitProjectWork(home, container.Project)
		if err != nil {
			return err
		}
		defer release()
		if _, err := readProjectTargets(container.Project); err != nil {
			return err
		}
		names, err := deployment.RuntimeNames(home)
		if err != nil {
			return err
		}
		container.Deployment = names.Suffix
		return wiki.DockerExport(ctx, container)
	}}
	results, err := exporter.Export(ctx, projects)
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("%v", err))
	}
	if err := writeJSON(stdout, map[string]any{"projects": results}); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return 0
}
