package wiki

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

var exportNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// exportRun records each export run and writes the five export files.
type exportRun struct {
	t          *testing.T
	containers []ExportContainer
	inputs     []map[string]string
	fail       error
	write      []string
}

func (r *exportRun) run(ctx context.Context, container ExportContainer) error {
	r.t.Helper()
	if _, ok := ctx.Deadline(); !ok {
		r.t.Error("the export run has no deadline")
	}
	r.containers = append(r.containers, container)
	input := map[string]string{}
	err := filepath.WalkDir(container.Input, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(container.Input, path)
		switch {
		case entry.IsDir() && info.Mode().Perm() != 0o755:
			r.t.Errorf("input folder %s has mode %v", rel, info.Mode().Perm())
		case !entry.IsDir() && (!info.Mode().IsRegular() || info.Mode().Perm() != 0o644):
			r.t.Errorf("input file %s has mode %v", rel, info.Mode())
		case !entry.IsDir():
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			input[filepath.ToSlash(rel)] = string(body)
		}
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.inputs = append(r.inputs, input)
	info, err := os.Stat(container.Output)
	if err != nil || info.Mode().Perm() != 0o777 {
		r.t.Errorf("the output folder is not writable for the container user: %v %v", info, err)
	}
	if entries, _ := os.ReadDir(container.Output); len(entries) != 0 {
		r.t.Errorf("the output folder is not empty: %v", entries)
	}
	names := r.write
	if names == nil {
		names = ExportFiles
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(container.Output, name), []byte("export of "+name), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	return r.fail
}

func newExporter(t *testing.T, run *exportRun) Exporter {
	t.Helper()
	return Exporter{Root: filepath.Join(t.TempDir(), "wiki"), Image: "mc-test-image", Run: run.run,
		Now: func() time.Time { return exportNow }}
}

func exportOnce(t *testing.T, exporter Exporter, projects ...ExportProject) []ExportResult {
	t.Helper()
	results, err := exporter.Export(context.Background(), projects)
	if err != nil {
		t.Fatal(err)
	}
	return results
}

func requireResult(t *testing.T, got ExportResult, project, status, commit, errorText string) {
	t.Helper()
	gotCommit := ""
	if got.Commit != nil {
		gotCommit = *got.Commit
	}
	if got.Project != project || got.Status != status || gotCommit != commit || !strings.Contains(got.Error, errorText) ||
		(errorText == "" && got.Error != "") {
		t.Fatalf("result = %+v (commit %q), want %s %s %q with error %q", got, gotCommit, project, status, commit, errorText)
	}
}

func folderNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func sorted(names ...string) []string {
	sort.Strings(names)
	return names
}

func readRecord(t *testing.T, exporter Exporter, project string) ExportRecord {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(exporter.Root, project, ExportRecordName))
	if err != nil {
		t.Fatal(err)
	}
	var record ExportRecord
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestExportUsesTheAcceptedOpenWikiTreeOnly(t *testing.T) {
	r := newWikiRepository(t)
	r.write("README.md", "# Project\n")
	r.write("openwiki/index.md", "# Index\n")
	r.write("openwiki/guide/start.md", "# Start\n")
	r.write("openwiki/.claims/guide/start.json", "{}\n")
	if err := os.Symlink("../README.md", filepath.Join(r.root, "openwiki", "readme.md")); err != nil {
		t.Fatal(err)
	}
	accepted := r.commit("Accept the wiki")
	tree := r.git("rev-parse", accepted+":openwiki")
	r.git("checkout", "--quiet", "-b", "candidate")
	r.write("openwiki/candidate.md", "# Candidate\n")
	r.commit("Prepare a candidate page")
	r.write("openwiki/index.md", "# Dirty index\n")
	r.write("openwiki/untracked.md", "# Untracked\n")

	run := &exportRun{t: t}
	exporter := newExporter(t, run)
	results := exportOnce(t, exporter, ExportProject{ID: "ws-alpha", Root: r.root})
	requireResult(t, results[0], "ws-alpha", ExportExported, accepted, "")

	wantInput := map[string]string{
		"index.md":                 "# Index\n",
		"guide/start.md":           "# Start\n",
		".claims/guide/start.json": "{}\n",
	}
	if len(run.inputs) != 1 || !reflect.DeepEqual(run.inputs[0], wantInput) {
		t.Fatalf("export input = %v, want %v", run.inputs, wantInput)
	}
	container := run.containers[0]
	if container.Image != "mc-test-image" || container.Project != "ws-alpha" ||
		!strings.HasPrefix(container.Name, "mc-wiki-export-") || filepath.Base(container.Input) != "openwiki" {
		t.Fatalf("container = %+v", container)
	}
	if _, err := os.Stat(container.Input); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the private input remains after the export: %v", err)
	}

	dir := filepath.Join(exporter.Root, "ws-alpha")
	if names := folderNames(t, dir); !reflect.DeepEqual(names, sorted(accepted, ExportRecordName)) {
		t.Fatalf("Project folder = %v", names)
	}
	want := ExportRecord{Project: "ws-alpha", Commit: accepted, Tree: tree, Directory: accepted,
		ExportedAt: "2026-09-29T12:00:00Z"}
	if record := readRecord(t, exporter, "ws-alpha"); record != want {
		t.Fatalf("export record = %+v, want %+v", record, want)
	}
	if record, ok := ReadExport(exporter.Root, "ws-alpha"); !ok || record != want {
		t.Fatalf("ReadExport = %+v %v", record, ok)
	}
	folder := filepath.Join(dir, accepted)
	if names := folderNames(t, folder); !reflect.DeepEqual(names, ExportFiles) {
		t.Fatalf("export folder = %v", names)
	}
	for path, mode := range map[string]os.FileMode{
		exporter.Root: 0o700, dir: 0o700, folder: 0o700,
		filepath.Join(folder, "index.html"): 0o600, filepath.Join(dir, ExportRecordName): 0o600,
	} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s has mode %v (%v), want %v", path, info.Mode().Perm(), err, mode)
		}
	}
}

func TestExportRunsOnlyForANewAcceptedCommit(t *testing.T) {
	r := newWikiRepository(t)
	r.write("openwiki/index.md", "# Index\n")
	first := r.commit("Accept the wiki")
	run := &exportRun{t: t}
	exporter := newExporter(t, run)
	project := ExportProject{ID: "ws-alpha", Root: r.root}
	requireResult(t, exportOnce(t, exporter, project)[0], "ws-alpha", ExportExported, first, "")

	stale := filepath.Join(exporter.Root, "ws-alpha", ".stage-left")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	requireResult(t, exportOnce(t, exporter, project)[0], "ws-alpha", ExportCurrent, first, "")
	if len(run.containers) != 1 {
		t.Fatalf("a current export ran the export command again: %d runs", len(run.containers))
	}
	if names := folderNames(t, filepath.Join(exporter.Root, "ws-alpha")); !reflect.DeepEqual(names, sorted(first, ExportRecordName)) {
		t.Fatalf("a current export kept other entries: %v", names)
	}

	r.write("openwiki/index.md", "# Index\n\nA new line.\n")
	second := r.commit("Change the wiki")
	requireResult(t, exportOnce(t, exporter, project)[0], "ws-alpha", ExportExported, second, "")
	if names := folderNames(t, filepath.Join(exporter.Root, "ws-alpha")); !reflect.DeepEqual(names, sorted(second, ExportRecordName)) {
		t.Fatalf("the older export remains: %v", names)
	}
	if len(run.inputs) != 2 || run.inputs[1]["index.md"] != "# Index\n\nA new line.\n" {
		t.Fatalf("the second export read %v", run.inputs)
	}
	if record := readRecord(t, exporter, "ws-alpha"); record.Commit != second || record.Directory != second {
		t.Fatalf("export record = %+v", record)
	}
}

func TestAFailedExportKeepsThePreviousExport(t *testing.T) {
	r := newWikiRepository(t)
	r.write("openwiki/index.md", "# Index\n")
	first := r.commit("Accept the wiki")
	run := &exportRun{t: t}
	exporter := newExporter(t, run)
	clock := exportNow
	exporter.Now = func() time.Time { return clock }
	project := ExportProject{ID: "ws-alpha", Root: r.root}
	exportOnce(t, exporter, project)
	previous, err := os.ReadFile(filepath.Join(exporter.Root, "ws-alpha", ExportRecordName))
	if err != nil {
		t.Fatal(err)
	}
	r.write("openwiki/index.md", "# Changed index\n")
	second := r.commit("Change the wiki")

	for name, fault := range map[string]struct {
		fail  error
		write []string
		text  string
	}{
		"command failure": {errors.New("exit status 1: ENOENT: no such file or directory"), nil,
			"the export command failed: exit status 1: ENOENT"},
		"missing file": {nil, []string{"index.html", "client.js", "client-lib.js", "styles.css"},
			"the export command did not write graph.json"},
		"unexpected file": {nil, append(append([]string{}, ExportFiles...), "extra.txt"),
			"the export command wrote the unexpected file extra.txt"},
	} {
		t.Run(name, func(t *testing.T) {
			clock = clock.Add(ExportRetryDelay)
			run.fail, run.write = fault.fail, fault.write
			requireResult(t, exportOnce(t, exporter, project)[0], "ws-alpha", ExportFailed, second, fault.text)
			current, err := os.ReadFile(filepath.Join(exporter.Root, "ws-alpha", ExportRecordName))
			if err != nil || string(current) != string(previous) {
				t.Fatalf("the failure changed the export record: %s %v", current, err)
			}
			if names := folderNames(t, filepath.Join(exporter.Root, "ws-alpha")); !reflect.DeepEqual(names, sorted(first, ExportRecordName, ExportFailureName)) {
				t.Fatalf("the failure changed the Project folder: %v", names)
			}
			if _, ok := ReadExport(exporter.Root, "ws-alpha"); !ok {
				t.Fatal("the previous export is no longer complete")
			}
		})
	}
	clock = clock.Add(ExportRetryDelay)
	run.fail, run.write = nil, nil
	requireResult(t, exportOnce(t, exporter, project)[0], "ws-alpha", ExportExported, second, "")
	if names := folderNames(t, filepath.Join(exporter.Root, "ws-alpha")); !reflect.DeepEqual(names, sorted(second, ExportRecordName)) {
		t.Fatalf("the export kept the failure record: %v", names)
	}
}

// A failed export records its commit, tree, time, and cause. The export
// command runs again for that commit and tree only after the retry delay. A
// new commit runs at once.
func TestAFailedExportWaitsBeforeItRunsAgain(t *testing.T) {
	r := newWikiRepository(t)
	r.write("openwiki/index.md", "# Index\n")
	first := r.commit("Accept the wiki")
	tree := r.git("rev-parse", first+":openwiki")
	run := &exportRun{t: t, fail: errors.New("exit status 1: the export hangs")}
	exporter := newExporter(t, run)
	clock := exportNow
	exporter.Now = func() time.Time { return clock }
	project := ExportProject{ID: "ws-alpha", Root: r.root}
	const cause = "the export command failed: exit status 1: the export hangs"

	requireResult(t, exportOnce(t, exporter, project)[0], "ws-alpha", ExportFailed, first, cause)
	failure, ok := ReadExportFailure(exporter.Root, "ws-alpha")
	want := ExportFailure{Project: "ws-alpha", Commit: first, Tree: tree, FailedAt: "2026-09-29T12:00:00Z", Error: cause}
	if !ok || failure != want {
		t.Fatalf("failure record = %+v %v, want %+v", failure, ok, want)
	}
	if info, err := os.Stat(filepath.Join(exporter.Root, "ws-alpha", ExportFailureName)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("failure record mode = %v %v", info, err)
	}

	clock = exportNow.Add(ExportRetryDelay - time.Second)
	requireResult(t, exportOnce(t, exporter, project)[0], "ws-alpha", ExportFailed, first, cause)
	if len(run.containers) != 1 {
		t.Fatalf("the export command ran again within the retry delay: %d runs", len(run.containers))
	}

	clock = exportNow.Add(ExportRetryDelay)
	requireResult(t, exportOnce(t, exporter, project)[0], "ws-alpha", ExportFailed, first, cause)
	if len(run.containers) != 2 {
		t.Fatalf("the export command did not run after the retry delay: %d runs", len(run.containers))
	}
	if failure, _ := ReadExportFailure(exporter.Root, "ws-alpha"); failure.FailedAt != "2026-09-29T12:10:00Z" {
		t.Fatalf("the retry did not record its time: %+v", failure)
	}

	r.write("README.md", "# Project\n")
	second := r.commit("Change the Project outside the wiki")
	requireResult(t, exportOnce(t, exporter, project)[0], "ws-alpha", ExportFailed, second, cause)
	if len(run.containers) != 3 {
		t.Fatalf("a new commit did not run at once: %d runs", len(run.containers))
	}

	run.fail = nil
	r.write("openwiki/index.md", "# Changed index\n")
	third := r.commit("Change the wiki")
	requireResult(t, exportOnce(t, exporter, project)[0], "ws-alpha", ExportExported, third, "")
	if _, ok := ReadExportFailure(exporter.Root, "ws-alpha"); ok {
		t.Fatal("the export kept the failure record")
	}
}

// A stop request cancels the export. The cancel is not a failure of the
// commit and tree, so the next export runs at once.
func TestAStoppedExportRecordsNoFailure(t *testing.T) {
	r := newWikiRepository(t)
	r.write("openwiki/index.md", "# Index\n")
	r.commit("Accept the wiki")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exporter := Exporter{Root: filepath.Join(t.TempDir(), "wiki"), Image: "mc-test-image",
		Now: func() time.Time { return exportNow },
		Run: func(context.Context, ExportContainer) error {
			cancel()
			return context.Canceled
		}}
	results, err := exporter.Export(ctx, []ExportProject{{ID: "ws-alpha", Root: r.root}})
	if err != nil || len(results) != 1 || results[0].Status != ExportFailed {
		t.Fatalf("stopped export = %+v %v", results, err)
	}
	if _, ok := ReadExportFailure(exporter.Root, "ws-alpha"); ok {
		t.Fatal("a stop request recorded a failure")
	}
}

// A Project whose last export failed runs after the other Projects. The
// results keep the order of the Project list.
func TestAProjectThatFailedBeforeRunsLast(t *testing.T) {
	alpha := newWikiRepository(t)
	alpha.write("openwiki/index.md", "# Alpha\n")
	alpha.commit("Accept the wiki")
	beta := newWikiRepository(t)
	beta.write("openwiki/index.md", "# Beta\n")
	beta.commit("Accept the wiki")
	run := &exportRun{t: t, fail: errors.New("exit status 1")}
	exporter := newExporter(t, run)
	exportOnce(t, exporter, ExportProject{ID: "ws-alpha", Root: alpha.root})

	run.fail = nil
	alpha.write("openwiki/index.md", "# Alpha changed\n")
	alpha.commit("Change the wiki")
	results := exportOnce(t, exporter,
		ExportProject{ID: "ws-alpha", Root: alpha.root},
		ExportProject{ID: "ws-beta", Root: beta.root},
	)
	if results[0].Project != "ws-alpha" || results[0].Status != ExportExported ||
		results[1].Project != "ws-beta" || results[1].Status != ExportExported {
		t.Fatalf("results = %+v", results)
	}
	if len(run.containers) != 3 || run.containers[1].Project != "ws-beta" || run.containers[2].Project != "ws-alpha" {
		t.Fatalf("export order = %+v", run.containers)
	}
}

func TestExportRemovesExportsWithoutAWiki(t *testing.T) {
	withWiki := newWikiRepository(t)
	withWiki.write("openwiki/index.md", "# Index\n")
	withWiki.commit("Accept the wiki")
	withoutWiki := newWikiRepository(t)
	withoutWiki.write("README.md", "# Project\n")
	withoutWiki.write("openwiki", "A file, not a folder.\n")
	plain := withoutWiki.commit("Accept a Project without a wiki")
	noMain := newWikiRepository(t)

	run := &exportRun{t: t}
	exporter := newExporter(t, run)
	exportOnce(t, exporter,
		ExportProject{ID: "ws-alpha", Root: withWiki.root},
		ExportProject{ID: "ws-beta", Root: withWiki.root},
		ExportProject{ID: "ws-gone", Root: withWiki.root},
		ExportProject{ID: "ws-plain", Root: withWiki.root},
		ExportProject{ID: "ws-unset", Root: withWiki.root},
	)
	if err := os.WriteFile(filepath.Join(exporter.Root, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	results := exportOnce(t, exporter,
		ExportProject{ID: "ws-alpha", Root: withWiki.root},
		ExportProject{ID: "ws-beta", Root: noMain.root},
		ExportProject{ID: "ws-plain", Root: withoutWiki.root},
		ExportProject{ID: "ws-unset", Root: ""},
		ExportProject{ID: "Ws-Case", Root: withWiki.root},
		ExportProject{ID: "ws-case", Root: withWiki.root},
		ExportProject{ID: ".hidden", Root: withWiki.root},
	)
	requireResult(t, results[0], "ws-alpha", ExportCurrent, results[0].commitText(), "")
	requireResult(t, results[1], "ws-beta", ExportFailed, "", "read the accepted commit")
	requireResult(t, results[2], "ws-plain", ExportAbsent, plain, "")
	requireResult(t, results[3], "ws-unset", ExportAbsent, "", "")
	requireResult(t, results[4], "Ws-Case", ExportFailed, "", "differs from this ID only in letter case")
	requireResult(t, results[5], "ws-case", ExportFailed, "", "differs from this ID only in letter case")
	requireResult(t, results[6], ".hidden", ExportFailed, "", "not a valid folder name")
	if names := folderNames(t, exporter.Root); !reflect.DeepEqual(names, []string{exportLockName, "ws-alpha", "ws-beta"}) {
		t.Fatalf("export root = %v", names)
	}
	if _, ok := ReadExport(exporter.Root, "ws-beta"); !ok {
		t.Fatal("a failed Project lost its previous export")
	}
	body, err := json.Marshal(results[1])
	if err != nil || !strings.HasPrefix(string(body), `{"project":"ws-beta","status":"failed","commit":null,"error":"read the accepted commit: `) {
		t.Fatalf("failed result JSON = %s %v", body, err)
	}
	body, err = json.Marshal(results[2])
	if err != nil || string(body) != `{"project":"ws-plain","status":"absent","commit":"`+plain+`"}` {
		t.Fatalf("absent result JSON = %s %v", body, err)
	}
}

func (r ExportResult) commitText() string {
	if r.Commit == nil {
		return ""
	}
	return *r.Commit
}

func TestOneExportRunsAtATime(t *testing.T) {
	run := &exportRun{t: t}
	exporter := newExporter(t, run)
	if err := privateDirectory(exporter.Root); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockExports(exporter.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exporter.Export(context.Background(), nil); err == nil || err.Error() != "another wiki export is in progress" {
		t.Fatalf("a second export = %v", err)
	}
	unlock()
	if _, err := exporter.Export(context.Background(), nil); err != nil {
		t.Fatalf("the export after the lock: %v", err)
	}
}

func TestReadExportRequiresAValidRecordAndCompleteFolder(t *testing.T) {
	commit := strings.Repeat("a", 40)
	valid := map[string]any{"project": "ws-alpha", "commit": commit, "tree": strings.Repeat("b", 40),
		"directory": commit, "exported_at": "2026-09-29T12:00:00Z"}
	for name, change := range map[string]func(root string, record map[string]any) []byte{
		"valid": nil,
		"other Project": func(_ string, record map[string]any) []byte {
			record["project"] = "ws-beta"
			return nil
		},
		"other folder": func(_ string, record map[string]any) []byte {
			record["directory"] = strings.Repeat("c", 40)
			return nil
		},
		"short commit": func(_ string, record map[string]any) []byte {
			record["commit"], record["directory"] = "abc", "abc"
			return nil
		},
		"unknown key": func(_ string, record map[string]any) []byte {
			record["url"] = "http://127.0.0.1:7335/ws-alpha/"
			return nil
		},
		"invalid time": func(_ string, record map[string]any) []byte {
			record["exported_at"] = "today"
			return nil
		},
		"trailing data": func(_ string, record map[string]any) []byte {
			body, _ := json.Marshal(record)
			return append(body, []byte(" {}")...)
		},
		"missing file": func(root string, _ map[string]any) []byte {
			if err := os.Remove(filepath.Join(root, "ws-alpha", commit, "graph.json")); err != nil {
				t.Fatal(err)
			}
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			folder := filepath.Join(root, "ws-alpha", commit)
			if err := os.MkdirAll(folder, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, file := range ExportFiles {
				if err := os.WriteFile(filepath.Join(folder, file), []byte(file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			record := map[string]any{}
			for key, value := range valid {
				record[key] = value
			}
			var body []byte
			if change != nil {
				body = change(root, record)
			}
			if body == nil {
				body, _ = json.Marshal(record)
			}
			if err := os.WriteFile(filepath.Join(root, "ws-alpha", ExportRecordName), body, 0o600); err != nil {
				t.Fatal(err)
			}
			_, ok := ReadExport(root, "ws-alpha")
			if ok != (name == "valid") {
				t.Fatalf("ReadExport accepted = %v", ok)
			}
			if exports := ReadExports(root); (len(exports) == 1) != ok {
				t.Fatalf("ReadExports = %v", exports)
			}
		})
	}
}

func TestExportDockerArgsIsolateTheContainer(t *testing.T) {
	got := ExportDockerArgs(ExportContainer{Name: "mc-wiki-export-0123456789abcdef", Image: "mc-prod",
		Project: "ws-alpha", Input: "/home/wiki/ws-alpha/.stage-1/input/openwiki", Output: "/home/wiki/ws-alpha/.stage-1/output"})
	want := strings.Fields(`run --rm --name mc-wiki-export-0123456789abcdef --pull never --network none
		--user 10002:10002 --cap-drop ALL --security-opt no-new-privileges=true --read-only
		--cpus 1 --memory 1024m --pids-limit 128
		--label mc-managed=true --label mc-deployment= --label mc-tier=wiki-export --label mc-project=ws-alpha
		-v /home/wiki/ws-alpha/.stage-1/input/openwiki:/input/openwiki:ro -v /home/wiki/ws-alpha/.stage-1/output:/output
		mc-prod /usr/bin/env -i PATH=/usr/local/bin:/usr/bin:/bin
		/usr/local/bin/node /opt/openwiki/node_modules/openwiki/dist/cli/cli.js visualize /input/openwiki --export /output`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("docker arguments =\n%q\nwant\n%q", got, want)
	}
	for _, argument := range got {
		if argument == "-e" || argument == "--env" || argument == "--env-file" || strings.HasPrefix(argument, "--mount") {
			t.Fatalf("the export container receives %q", argument)
		}
	}
}

// fakeDocker puts a docker executable on PATH that records its arguments.
func fakeDocker(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "docker.log")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n" + script
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func dockerCalls(t *testing.T, log string) []string {
	t.Helper()
	body, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(body)), "\n")
}

func TestDockerExportRemovesAFailedContainer(t *testing.T) {
	log := fakeDocker(t, `[ "$1" = run ] || exit 0
echo "docker: Error response from daemon: No such image: mc-absent:latest." >&2
echo "See 'docker run --help'." >&2
exit 125
`)
	container := ExportContainer{Name: "mc-wiki-export-0123456789abcdef", Image: "mc-absent", Project: "ws-alpha",
		Input: t.TempDir(), Output: t.TempDir()}
	err := DockerExport(context.Background(), container)
	if err == nil || err.Error() != "exit status 125: docker: Error response from daemon: No such image: mc-absent:latest." {
		t.Fatalf("export error = %v", err)
	}
	calls := dockerCalls(t, log)
	if len(calls) != 2 || calls[0] != strings.Join(ExportDockerArgs(container), " ") ||
		calls[1] != "rm -f mc-wiki-export-0123456789abcdef" {
		t.Fatalf("docker calls = %q", calls)
	}
}

func TestDockerExportStopsAtItsDeadline(t *testing.T) {
	log := fakeDocker(t, `[ "$1" = run ] || exit 0
exec sleep 30
`)
	container := ExportContainer{Name: "mc-wiki-export-fedcba9876543210", Image: "mc-prod", Project: "ws-alpha",
		Input: t.TempDir(), Output: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	err := DockerExport(ctx, container)
	if err == nil || err.Error() != "the container did not finish within 120 seconds" || time.Since(started) > 15*time.Second {
		t.Fatalf("export error = %v after %s", err, time.Since(started))
	}
	// A loaded host can stop the run before the fake records it, so only the
	// removal is certain.
	calls := dockerCalls(t, log)
	if len(calls) > 2 || calls[len(calls)-1] != "rm -f mc-wiki-export-fedcba9876543210" {
		t.Fatalf("docker calls = %q", calls)
	}
}

func TestDockerExportRefusesAColonInAMountPath(t *testing.T) {
	log := fakeDocker(t, "exit 0\n")
	err := DockerExport(context.Background(), ExportContainer{Name: "mc-wiki-export-0", Image: "mc-prod",
		Project: "ws-alpha", Input: "/tmp/a:b", Output: t.TempDir()})
	if err == nil {
		t.Fatal("the export accepted a colon in a mount path")
	}
	if _, err := os.Stat(log); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the export started docker")
	}
}
