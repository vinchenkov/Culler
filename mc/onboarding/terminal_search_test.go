package onboarding

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"
)

var searchFixture = []Choice{
	{ID: "anthropic", Label: "Anthropic", Detail: "https://api.anthropic.com/v1"},
	{ID: "kimi-cn", Label: "Kimi For Coding (kimi.com)", Detail: "https://api.kimi.com/coding/v1"},
	{ID: "kimi-global", Label: "Kimi For Coding (kimi.ai)", Detail: "https://api.kimi.ai/coding/v1"},
	{ID: "moonshot", Label: "Moonshot AI", Detail: "https://api.moonshot.ai/v1"},
	{ID: "mistral", Label: "Mistral", Detail: "https://api.mistral.ai/v1"},
	{ID: "minimax", Label: "MiniMax", Detail: "https://api.minimax.io/v1"},
}

func searchFixtureFind(query string) []Choice {
	if query == "" {
		return searchFixture[:1]
	}
	var found []Choice
	for _, choice := range searchFixture {
		if strings.Contains(strings.ToLower(choice.Label), strings.ToLower(query)) {
			found = append(found, choice)
		}
	}
	return found
}

func searchFixturePrompt() Search {
	return Search{Label: "Provider", Hints: []string{"Search by name."}, QueryHints: []string{"Leave the search empty."}, Initial: "Current release integrations.", Find: searchFixtureFind,
		Empty: Correction{Title: "No provider found", Lines: []string{"Enter another name."}}, Choose: "Choose the provider"}
}

func TestSearchUpdatesMatchesAfterEachKey(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		err               error
	}{
		{name: "empty query", input: "\r", want: "anthropic"},
		{name: "first match", input: "kim\r", want: "kimi-cn"},
		{name: "down", input: "kim\x1b[B\r", want: "kimi-global"},
		{name: "up wraps", input: "kim\x1b[A\r", want: "kimi-global"},
		{name: "delete refilters", input: "kimz\x7f\r", want: "kimi-cn"},
		{name: "clear refilters", input: "kim\x15moon\r", want: "moonshot"},
		{name: "cursor edits", input: "mon\x1b[D\x1b[Do\r", want: "moonshot"},
		{name: "enter without match", input: "zzz\r\x15moon\r", want: "moonshot"},
		{name: "letters are text", input: "qb\x15mistral\r", want: "mistral"},
		{name: "back", input: "kim\x02", err: ErrBack},
		{name: "escape", input: "\x1b", err: ErrBack},
		{name: "interrupt", input: "kim\x03", err: ErrExit},
		{name: "closed input", input: "kim", err: ErrExit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			terminal := NewTerminal(strings.NewReader(tc.input), &out)
			terminal.columns, terminal.ansi = 40, true
			got, err := terminal.Search(searchFixturePrompt())
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("search = %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
			plain := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`).ReplaceAllString(out.String(), "\n")
			for _, line := range strings.Split(plain, "\n") {
				if displayWidth(strings.TrimLeft(line, "\r")) > 40 {
					t.Fatalf("the search exceeds the terminal width: %q", line)
				}
			}
			if !strings.Contains(plain, "↑↓ move  enter select  ctrl+b back") {
				t.Fatalf("the search lacks its controls: %q", plain)
			}
		})
	}
}

func TestSearchShowsMatchesAndTheSelectedDetail(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(strings.NewReader("m"), &out)
	terminal.columns, terminal.ansi = 80, true
	frames := []string{}
	search := searchFixturePrompt()
	find := search.Find
	search.Find = func(query string) []Choice {
		frames = append(frames, out.String())
		return find(query)
	}
	_, _ = terminal.Search(search)
	frames = append(frames, out.String())
	initial, typed := frames[1], strings.TrimPrefix(frames[2], frames[1])
	for _, text := range []string{"Current release integrations.", "› Anthropic", "https://api.anthropic.com/v1"} {
		if !strings.Contains(initial, text) {
			t.Fatalf("the empty query lacks %q: %q", text, initial)
		}
	}
	if strings.Contains(initial, "Leave the search empty.") || strings.Contains(typed, "Current release integrations.") {
		t.Fatalf("the search shows a query hint or the initial label with a query: %q", out.String())
	}
	for _, text := range []string{"Choices 1 to 4 of 5.", "› Kimi For Coding (kimi.com)", "https://api.kimi.com/coding/v1", "  Moonshot AI", "> m"} {
		if !strings.Contains(typed, text) {
			t.Fatalf("the matches lack %q: %q", text, typed)
		}
	}
	if strings.Contains(typed, "https://api.moonshot.ai/v1") || strings.Contains(typed, "MiniMax") {
		t.Fatalf("the matches show an unselected detail or a later page: %q", typed)
	}
}

func TestSearchLabelsTheMatchesOfASpaceOnlyQuery(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(strings.NewReader(" "), &out)
	terminal.columns, terminal.ansi = 80, true
	search := searchFixturePrompt()
	search.Find = func(string) []Choice { return searchFixture[:1] }
	_, _ = terminal.Search(search)
	if strings.Count(out.String(), "Current release integrations.") != 2 {
		t.Fatalf("the space-only query lacks the initial label: %q", out.String())
	}
}

func TestSearchShowsTheNextPageOfMatches(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(strings.NewReader("m"+strings.Repeat("\x1b[B", 4)+"\r"), &out)
	terminal.columns, terminal.ansi = 80, true
	got, err := terminal.Search(searchFixturePrompt())
	if got != "minimax" || err != nil {
		t.Fatalf("search = %q, %v", got, err)
	}
	if !strings.Contains(out.String(), "Choices 5 to 5 of 5.") || !strings.Contains(out.String(), "› Provider: MiniMax") {
		t.Fatalf("the search lacks the next page or its answer: %q", out.String())
	}
}

func TestSearchNamesAQueryWithoutMatches(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(strings.NewReader("zzz"), &out)
	terminal.columns, terminal.ansi = 80, true
	_, _ = terminal.Search(searchFixturePrompt())
	if !strings.Contains(out.String(), "! No provider found") || !strings.Contains(out.String(), "Enter another name.") {
		t.Fatalf("the search does not name the missing match: %q", out.String())
	}
	last := out.String()[strings.LastIndex(out.String(), "! No provider found"):]
	if !strings.Contains(last, "ctrl+b back  ctrl+c exit") || strings.Contains(last, "enter select") || strings.Contains(last, "↑↓") {
		t.Fatalf("the controls name keys that do nothing without a match: %q", last)
	}
}

func TestSearchWithoutScreenControlsAsksForTheQuery(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		err               error
	}{
		{name: "number", input: "kim\n2\n", want: "kimi-global"},
		{name: "query without match", input: "zzz\nmoon\n\n", want: "moonshot"},
		{name: "back to the query", input: "kim\nbmoon\n\n", want: "moonshot"},
		{name: "exit", input: ":exit\n", err: ErrExit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			terminal := NewTerminal(strings.NewReader(tc.input), &out)
			got, err := terminal.Search(searchFixturePrompt())
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("search = %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
			if strings.Contains(out.String(), "\x1b[") {
				t.Fatalf("the search wrote screen controls: %q", out.String())
			}
		})
	}
	var out bytes.Buffer
	terminal := NewTerminal(strings.NewReader("zzz\nkim\n1\n"), &out)
	if _, err := terminal.Search(searchFixturePrompt()); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Provider search", "Leave the search empty.", "! No provider found", "Choose the provider", "› 1. Kimi For Coding (kimi.com)"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("the search lacks %q: %q", text, out.String())
		}
	}
}
