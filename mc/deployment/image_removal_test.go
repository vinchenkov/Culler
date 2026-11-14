package deployment

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const imageTestRepository = "ghcr.io/test/project"

func imageTestDigest(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }

func TestRemoveSupersededImagesRemovesUnneededReferences(t *testing.T) {
	previous := "ghcr.io/test/culler@" + imageTestDigest('e')
	installed := imageTestRepository + "@" + imageTestDigest('a')
	selected := imageTestRepository + "@" + imageTestDigest('b')
	unused := imageTestRepository + "@" + imageTestDigest('c')
	inUse := imageTestRepository + "@" + imageTestDigest('d')
	unusedBase := ProjectBaseReference(imageTestDigest('3'))
	inUseBase := ProjectBaseReference(imageTestDigest('4'))
	list := installed + "\t" + imageTestDigest('1') + "\n" + selected + "\t" + imageTestDigest('2') + "\n" +
		unused + "\t" + imageTestDigest('3') + "\n" + inUse + "\t" + imageTestDigest('4') + "\n"
	var calls []string
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "image rm " + previous, "image rm " + unusedBase:
			return []byte("Untagged: " + args[2] + "\n"), nil
		case "image rm " + unused:
			return []byte("Untagged: " + unused + "\nDeleted: " + imageTestDigest('3') + "\n"), nil
		case "image rm " + inUse:
			return []byte(`Error response from daemon: conflict: unable to remove repository reference "` + inUse + `" (must force) - container ac337021da3a is using its referenced image 56868084a44f` + "\n"), errors.New("exit status 1")
		case "image ls --digests --no-trunc --format {{.Repository}}@{{.Digest}}\\t{{.ID}} " + imageTestRepository:
			return []byte(list), nil
		case "image inspect --format {{.Id}} " + unusedBase:
			return []byte(imageTestDigest('3') + "\n"), nil
		case "image inspect --format {{.Id}} " + inUseBase:
			return []byte("Error response from daemon: No such image: " + inUseBase + "\n"), errors.New("exit status 1")
		}
		t.Fatalf("unexpected Docker command: %s", call)
		return nil, nil
	}
	var log []string
	result := RemoveSupersededImages(context.Background(), previous, imageTestRepository, []string{installed, imageTestDigest('2')}, run, func(line string) { log = append(log, line) })
	wantCalls := []string{
		"image rm " + previous,
		"image ls --digests --no-trunc --format {{.Repository}}@{{.Digest}}\\t{{.ID}} " + imageTestRepository,
		"image inspect --format {{.Id}} " + unusedBase,
		"image rm " + unusedBase,
		"image rm " + unused,
		"image inspect --format {{.Id}} " + inUseBase,
		"image rm " + inUse,
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("Docker commands:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(wantCalls, "\n"))
	}
	want := ImageRemoval{
		Removed: []string{previous, unusedBase, unused},
		Kept: []KeptImage{
			{Reference: installed, Reason: imageKeptNeeded},
			{Reference: selected, Reason: imageKeptNeeded},
			{Reference: inUse, Reason: imageKeptInUse},
		},
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result: %#v", result)
	}
	wantLog := []string{
		"Removed image " + previous + ".",
		"Kept image " + installed + ": " + imageKeptNeeded + ".",
		"Kept image " + selected + ": " + imageKeptNeeded + ".",
		"Removed image " + unusedBase + ".",
		"Removed image " + unused + ".",
		"Kept image " + inUse + ": " + imageKeptInUse + ".",
	}
	if !reflect.DeepEqual(log, wantLog) {
		t.Fatalf("log:\n%s", strings.Join(log, "\n"))
	}
}

func TestRemoveSupersededImagesKeepsEveryImageWhenTheListFails(t *testing.T) {
	var calls []string
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[1] == "ls" {
			return []byte("Cannot connect to the Docker daemon\n"), errors.New("exit status 1")
		}
		t.Fatalf("unexpected Docker command: %v", args)
		return nil, nil
	}
	var log []string
	result := RemoveSupersededImages(context.Background(), "", imageTestRepository, nil, run, func(line string) { log = append(log, line) })
	if len(calls) != 1 || len(result.Removed) != 0 || len(result.Kept) != 0 {
		t.Fatalf("calls %v result %#v", calls, result)
	}
	if !reflect.DeepEqual(log, []string{"The default Project images stay: the image list of " + imageTestRepository + " failed: Cannot connect to the Docker daemon"}) {
		t.Fatalf("log: %v", log)
	}
}

func TestRemoveSupersededImagesWithoutRepositoryTouchesOnlyThePreviousImage(t *testing.T) {
	previous := "ghcr.io/test/culler@" + imageTestDigest('e')
	for _, tc := range []struct {
		name    string
		out     string
		err     error
		removed []string
		kept    []KeptImage
		log     string
	}{
		{name: "removed", out: "Untagged: " + previous + "\n", removed: []string{previous}, log: "Removed image " + previous + "."},
		{name: "absent", out: "Error response from daemon: No such image: " + previous + "\n", err: errors.New("exit status 1"), log: "The image " + previous + " is absent."},
		{name: "running", out: "Error response from daemon: conflict: unable to delete 56868084a44f (cannot be forced) - image is being used by running container ac337021da3a\n", err: errors.New("exit status 1"),
			kept: []KeptImage{{Reference: previous, Reason: imageKeptInUse}}, log: "Kept image " + previous + ": " + imageKeptInUse + "."},
		{name: "failed", out: "", err: errors.New("exit status 125"),
			kept: []KeptImage{{Reference: previous, Reason: "the removal failed: exit status 125"}}, log: "Kept image " + previous + ": the removal failed: exit status 125."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
				calls = append(calls, strings.Join(args, " "))
				return []byte(tc.out), tc.err
			}
			var log []string
			result := RemoveSupersededImages(context.Background(), previous, "", nil, run, func(line string) { log = append(log, line) })
			if !reflect.DeepEqual(calls, []string{"image rm " + previous}) {
				t.Fatalf("calls: %v", calls)
			}
			if len(result.Removed) != len(tc.removed) || len(result.Kept) != len(tc.kept) || (len(tc.removed) > 0 && !reflect.DeepEqual(result.Removed, tc.removed)) || (len(tc.kept) > 0 && !reflect.DeepEqual(result.Kept, tc.kept)) {
				t.Fatalf("result: %#v", result)
			}
			if !reflect.DeepEqual(log, []string{tc.log}) {
				t.Fatalf("log: %v", log)
			}
		})
	}
}

func TestRemoveSupersededImagesSkipsRepeatedAndUndigestedLines(t *testing.T) {
	tagged := imageTestRepository + "@<none>"
	unused := imageTestRepository + "@" + imageTestDigest('c')
	var calls []string
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch args[1] {
		case "ls":
			return []byte(unused + "\t" + imageTestDigest('3') + "\n" + unused + "\t" + imageTestDigest('3') + "\n" + tagged + "\t" + imageTestDigest('5') + "\n\n"), nil
		case "inspect":
			return []byte("Error response from daemon: No such image\n"), errors.New("exit status 1")
		case "rm":
			return []byte("Deleted\n"), nil
		}
		t.Fatalf("unexpected Docker command: %s", call)
		return nil, nil
	}
	result := RemoveSupersededImages(context.Background(), "", imageTestRepository, nil, run, nil)
	wantCalls := []string{
		"image ls --digests --no-trunc --format {{.Repository}}@{{.Digest}}\\t{{.ID}} " + imageTestRepository,
		"image inspect --format {{.Id}} " + ProjectBaseReference(imageTestDigest('3')),
		"image rm " + unused,
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls:\n%s", strings.Join(calls, "\n"))
	}
	want := ImageRemoval{Removed: []string{unused}, Kept: []KeptImage{{Reference: tagged, Reason: imageKeptNoDigest}}}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result: %#v", result)
	}
}
