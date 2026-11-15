package onboarding

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Search describes a text search that selects one of the matching choices.
type Search struct {
	Label string
	Hints []string
	// QueryHints show only on a terminal that cannot redraw the list after each key.
	QueryHints []string
	// Initial describes the choices of an empty query above the list.
	Initial string
	// Find returns the choices that match a query, the best match first.
	Find func(string) []Choice
	// Empty names a query that matches no choice.
	Empty Correction
	// Choose labels the list of matches on a terminal that cannot redraw the list after each key.
	Choose string
}

// searchPage is the number of matches that the list shows at one time.
const searchPage = 4

// Search updates the list of matches after each key.
// A terminal without screen controls asks for the query and then shows the matches.
func (t *Terminal) Search(search Search) (value string, retErr error) {
	if !t.ansi {
		return t.searchByQuery(search)
	}
	keys, err := t.openKeys()
	if err != nil {
		return "", err
	}
	defer func() { retErr = errors.Join(retErr, keys.close()) }()
	frame := terminalFrame{terminal: t}
	var runes []rune
	cursor, selected := 0, 0
	choices := search.Find("")
	paint := func() {
		lines, back := t.searchLines(search, choices, selected, runes, cursor)
		frame.paint(lines, true, back)
	}
	columns := t.Width()
	keys.refresh = func() {
		if t.Width() != columns {
			columns = t.Width()
			paint()
		}
	}
	defer func() {
		if retErr != nil {
			frame.paint(nil, false, 0)
		}
	}()
	paint()
	for {
		key, err := keys.read()
		if err != nil {
			return "", err
		}
		changed := false
		switch key {
		case keyEnter:
			if len(choices) == 0 {
				continue
			}
			frame.answer(t.answerLines(search.Label, choices[selected].Label))
			return choices[selected].ID, nil
		case keyBack:
			return "", ErrBack
		case keyExit:
			return "", ErrExit
		case keyUp:
			if len(choices) > 0 {
				selected = (selected + len(choices) - 1) % len(choices)
			}
		case keyDown:
			if len(choices) > 0 {
				selected = (selected + 1) % len(choices)
			}
		case keyLeft:
			if cursor > 0 {
				cursor--
			}
		case keyRight:
			if cursor < len(runes) {
				cursor++
			}
		case keyDelete:
			if cursor > 0 {
				runes = slices.Delete(runes, cursor-1, cursor)
				cursor--
				changed = true
			}
		case keyClear:
			runes, cursor, changed = nil, 0, true
		default:
			for _, r := range key {
				if unicode.IsControl(r) || len(runes) >= 256 {
					continue
				}
				runes = slices.Insert(runes, cursor, r)
				cursor++
				changed = true
			}
		}
		if changed {
			choices, selected = search.Find(string(runes)), 0
		}
		paint()
	}
}

// searchByQuery returns to the query when the operator goes back from the list of matches.
func (t *Terminal) searchByQuery(search Search) (string, error) {
	for {
		var choices []Choice
		hints := append(slices.Clone(search.Hints), search.QueryHints...)
		_, err := t.Ask(Prompt{Label: search.Label + " search", Hints: hints, Check: func(query string) *Correction {
			if choices = search.Find(query); len(choices) == 0 {
				return &search.Empty
			}
			return nil
		}})
		if err != nil {
			return "", err
		}
		value, err := t.Choose(search.Choose, choices, 0)
		if !errors.Is(err, ErrBack) {
			return value, err
		}
	}
}

// searchLines shows the details of the selected match only, so that the list fits a small terminal.
func (t *Terminal) searchLines(search Search, choices []Choice, selected int, runes []rune, cursor int) ([]terminalLine, int) {
	lines := t.lines(search.Label, textHeading, 0)
	for _, hint := range search.Hints {
		lines = append(lines, t.hangingLines(hint, textMuted, 2, 2)...)
	}
	lines = append(lines, terminalLine{})
	controls := "↑↓ move  enter select  ctrl+b back  ctrl+c exit"
	if len(choices) == 0 {
		lines = append(lines, t.sectionLines(search.Empty.Title, search.Empty.Lines, true)...)
		controls = "ctrl+b back  ctrl+c exit"
	} else {
		if strings.TrimSpace(string(runes)) == "" && search.Initial != "" {
			lines = append(lines, t.lines(search.Initial, textMuted, 0)...)
		}
		start := selected / searchPage * searchPage
		end := min(start+searchPage, len(choices))
		if len(choices) > searchPage {
			lines = append(lines, t.lines(fmt.Sprintf("Choices %d to %d of %d.", start+1, end, len(choices)), textMuted, 0)...)
		}
		for i := start; i < end; i++ {
			prefix, style := "  ", ""
			if i == selected {
				prefix, style = "› ", textCurrent
			}
			lines = append(lines, t.prefixedLines(prefix, progressText(choices[i].Label), style)...)
			if i == selected && choices[i].Detail != "" {
				lines = append(lines, t.lines(choices[i].Detail, textMuted, displayWidth(prefix)+2)...)
			}
		}
		lines = append(lines, terminalLine{})
	}
	lines = append(lines, t.lines(controls, textMuted, 0)...)
	text, offset := inputView(runes, cursor, t.Width()-3, false)
	return append(lines, terminalLine{"> " + text, textCurrent}), displayWidth(text) - offset
}
