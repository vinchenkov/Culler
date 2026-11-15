package onboarding

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/width"
)

var (
	ErrBack           = errors.New("return to the previous setup choice")
	ErrExit           = errors.New("exit setup and preserve completed choices")
	ErrActionRequired = errors.New("the setup operation requires an operator action")
)

type Choice struct {
	ID           string
	Label        string
	Detail       string
	AlwaysDetail bool
}

type Terminal struct {
	in          *bufio.Reader
	out         io.Writer
	input       *os.File
	output      *os.File
	ansi        bool
	color       bool
	columns     int
	identity    bool
	stageTitles []string
	// answered is the output sequence of the latest answer.
	answered uint64
	// spaced is the output sequence of the latest output that ends with a blank line.
	spaced uint64
}

func NewTerminal(in io.Reader, out io.Writer) *Terminal {
	if in == nil {
		in = strings.NewReader("")
	}
	if out == nil {
		out = io.Discard
	}
	t := &Terminal{in: bufio.NewReader(in), out: &terminalOutput{writer: out}}
	if file, ok := in.(*os.File); ok && terminalFile(file) {
		t.input = file
	}
	if file, ok := out.(*os.File); ok && terminalFile(file) {
		t.output = file
	}
	t.ansi = t.input != nil && t.output != nil && os.Getenv("TERM") != "dumb"
	_, noColor := os.LookupEnv("NO_COLOR")
	t.color = t.ansi && !noColor
	return t
}

func (t *Terminal) Stage(index int, title string, completed []string) {
	t.StageWithNext(index, title, completed, "")
}

func (t *Terminal) StageWithNext(index int, title string, completed []string, next string) {
	t.writeLines(t.stageLines(index, title, completed, next))
}

func (t *Terminal) Panel(title string, lines []string) {
	t.writeLines(t.panelBlock(title, lines, false))
}

// Highlight shows a panel without a title in a style that menus do not use.
func (t *Terminal) Highlight(lines []string) {
	var block []terminalLine
	for _, line := range lines {
		block = append(block, t.hangingLines(line, textNotice, 2, 2)...)
	}
	t.writeLines(append(block, terminalLine{}))
}

// Problem shows a panel that names a failure or a rejected answer.
func (t *Terminal) Problem(title string, lines []string) {
	t.writeLines(t.panelBlock(title, lines, true))
}

// Write preserves complete addresses and coordinates output with active progress.
func (t *Terminal) Write(body []byte) (int, error) {
	out, ok := t.out.(*terminalOutput)
	if !ok {
		return t.out.Write(body)
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	n, err := out.write(body)
	if strings.HasSuffix(string(body), "\n\n") {
		t.spaced = out.sequence
	}
	return n, err
}

func (t *Terminal) Choose(prompt string, choices []Choice, selected int) (value string, retErr error) {
	if len(choices) == 0 {
		return "", fmt.Errorf("the setup choice list is empty")
	}
	if selected < 0 || selected >= len(choices) {
		selected = 0
	}
	keys, err := t.openKeys()
	if err != nil {
		return "", err
	}
	defer func() { retErr = errors.Join(retErr, keys.close()) }()
	frame := terminalFrame{terminal: t}
	var correction []terminalLine
	paint := func() {
		frame.paint(append(slices.Clone(correction), t.choiceLines(prompt, choices, selected)...), false, 0)
	}
	columns := t.Width()
	keys.refresh = func() {
		if t.Width() != columns {
			columns = t.Width()
			paint()
		}
	}
	defer func() {
		if retErr != nil && t.ansi {
			frame.paint(nil, false, 0)
		}
	}()
	paint()
	number := ""
	for {
		key, err := keys.read()
		if err != nil {
			return "", err
		}
		switch key {
		case keyUp:
			selected = (selected + len(choices) - 1) % len(choices)
			number = ""
			paint()
		case keyDown:
			selected = (selected + 1) % len(choices)
			number = ""
			paint()
		case keyEnter, keyRight:
			if number != "" {
				parsed, _ := strconv.Atoi(number)
				number = ""
				if parsed < 1 || parsed > len(choices) {
					instruction := "Enter one of the displayed choice numbers."
					if t.ansi {
						instruction = "Use the arrow keys to select an option."
					}
					correction = t.sectionLines("Choose an available option", []string{instruction}, true)
					paint()
					continue
				}
				selected = parsed - 1
			}
			frame.answer(t.answerLines("", choices[selected].Label))
			return choices[selected].ID, nil
		case keyLeft, keyBack, "b", "B":
			keys.discardLineBreak()
			return "", ErrBack
		case keyExit, "q", "Q":
			return "", ErrExit
		case keyDelete:
			if len(number) > 0 {
				number = number[:len(number)-1]
			}
		default:
			if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && len(number) < 4 {
				number += key
			}
		}
	}
}

// Prompt describes one text answer.
// Progress, Hints, and a correction show only while the prompt waits for the answer.
type Prompt struct {
	Label      string
	Progress   string
	Hints      []string
	Value      string
	Secret     bool
	CancelOnly bool
	// Check returns a correction for a rejected answer, or nil for an accepted answer.
	Check func(string) *Correction
	// Correction names the problem of an earlier answer until the operator enters an accepted answer.
	Correction *Correction
}

// Correction names the problem of a rejected answer.
type Correction struct {
	Title string
	Lines []string
}

func (t *Terminal) Input(prompt, value string, secret bool) (string, error) {
	return t.Ask(Prompt{Label: prompt, Value: value, Secret: secret})
}

// Ask shows a correction above the prompt until the operator enters an accepted answer.
// Only the accepted answer remains on the screen.
func (t *Terminal) Ask(question Prompt) (answer string, retErr error) {
	keys, err := t.openKeys()
	if err != nil {
		return "", err
	}
	defer func() { retErr = errors.Join(retErr, keys.close()) }()
	frame := terminalFrame{terminal: t}
	initial := []rune{}
	if t.input != nil {
		initial = []rune(question.Value)
	}
	runes := slices.Clone(initial)
	cursor := len(runes)
	var correction []terminalLine
	if question.Correction != nil {
		correction = t.sectionLines(question.Correction.Title, question.Correction.Lines, true)
	}
	paint := func() {
		lines, back := t.inputLines(question, runes, cursor)
		frame.paint(append(slices.Clone(correction), lines...), true, back)
	}
	paint()
	columns := t.Width()
	keys.refresh = func() {
		if t.ansi && t.Width() != columns {
			columns = t.Width()
			paint()
		}
	}
	defer func() {
		if retErr != nil {
			if t.ansi {
				frame.paint(nil, false, 0)
			} else {
				fmt.Fprintln(t.out)
			}
			return
		}
		shown := answer
		if shown == "" {
			shown = "None"
		} else if question.Secret {
			shown = "Concealed"
		}
		if !t.ansi {
			fmt.Fprintln(t.out)
		}
		frame.answer(t.answerLines(question.Label, shown))
	}()
	for {
		key, err := keys.read()
		if err != nil {
			return "", err
		}
		switch key {
		case keyEnter:
			answer := string(runes)
			switch answer {
			case ":back":
				if question.CancelOnly {
					return "", ErrExit
				}
				return "", ErrBack
			case ":exit":
				return "", ErrExit
			case ":clear":
				answer = ""
			case "":
				if t.input == nil {
					answer = question.Value
				}
			}
			if question.Check != nil {
				if problem := question.Check(answer); problem != nil {
					correction = t.sectionLines(problem.Title, problem.Lines, true)
					runes = slices.Clone(initial)
					cursor = len(runes)
					if !t.ansi {
						fmt.Fprintln(t.out)
					}
					paint()
					continue
				}
			}
			return answer, nil
		case keyBack:
			if question.CancelOnly {
				return "", ErrExit
			}
			return "", ErrBack
		case keyExit:
			return "", ErrExit
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
				runes = append(runes[:cursor-1], runes[cursor:]...)
				cursor--
			}
		case keyClear:
			runes = nil
			cursor = 0
		case keyUp, keyDown:
		default:
			for _, r := range key {
				if unicode.IsControl(r) || len(runes) >= 4096 {
					continue
				}
				runes = append(runes, 0)
				copy(runes[cursor+1:], runes[cursor:])
				runes[cursor] = r
				cursor++
				if t.input != nil && !t.ansi && !question.Secret {
					fmt.Fprint(t.out, string(r))
				}
			}
		}
		if t.ansi {
			text, offset := inputView(runes, cursor, t.Width()-3, question.Secret)
			frame.inputValue(terminalLine{"> " + text, textCurrent}, displayWidth(text)-offset)
		}
	}
}

func (t *Terminal) Activity(label string, action func() error) (retErr error) {
	if action == nil {
		return fmt.Errorf("the setup operation is absent")
	}
	return t.ActivityProgress(label, func(func(string)) error { return action() })
}

func (t *Terminal) activityResult(label string, err error) {
	t.writeLines(t.resultLines(label, err))
}

func (t *Terminal) Width() int {
	if t.columns > 0 {
		return t.columns
	}
	for _, file := range []*os.File{t.output, t.input} {
		if columns := terminalWidth(file); columns > 0 {
			return min(columns, 120)
		}
	}
	if columns, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && columns > 0 {
		return min(columns, 120)
	}
	return 80
}

func (t *Terminal) panelLines(title string, lines []string) []string {
	var result []string
	for _, line := range t.sectionLines(title, lines, false) {
		result = append(result, line.text)
	}
	return result
}

func (t *Terminal) wrap(text string) []string {
	return wrapText(text, t.Width())
}

func cleanText(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, text)
}

func wrapText(text string, columns int) []string {
	return wrapHanging(text, columns, columns)
}

// wrapHanging fits the first line in first columns and each later line in rest columns.
func wrapHanging(text string, first, rest int) []string {
	first, rest = max(first, 1), max(rest, 1)
	if min(first, rest) == 1 {
		text = strings.Map(func(r rune) rune {
			if runeWidth(r) > 1 {
				return '?'
			}
			return r
		}, text)
	}
	result := []string{}
	columns := first
	for _, paragraph := range strings.Split(cleanText(text), "\n") {
		runes := []rune(paragraph)
		for displayWidth(string(runes)) > columns {
			used, end, space, path := 0, 0, -1, -1
			for index, r := range runes {
				if used+runeWidth(r) > columns {
					break
				}
				used += runeWidth(r)
				end = index + 1
				if r == ' ' && index > 0 {
					space = index
				}
				if r == '/' && index > 0 {
					path = index + 1
				}
			}
			if end == 0 {
				end = 1
			}
			cut := end
			if space > 0 {
				cut = space
			} else if path > 0 {
				cut = path
			}
			result = append(result, strings.TrimRight(string(runes[:cut]), " "))
			runes = []rune(strings.TrimLeft(string(runes[cut:]), " "))
			columns = rest
		}
		result = append(result, string(runes))
		columns = rest
	}
	return result
}

func runeWidth(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == '\u200d' {
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	if r >= 0x1f300 && r <= 0x1faff {
		return 2
	}
	return 1
}

func displayWidth(text string) int {
	columns := 0
	for _, r := range text {
		columns += runeWidth(r)
	}
	return columns
}

func inputView(value []rune, cursor, columns int, secret bool) (string, int) {
	columns = max(columns, 1)
	if secret {
		value = []rune(strings.Repeat("*", len(value)))
	}
	start := 0
	for start < cursor && displayWidth(string(value[start:cursor])) >= columns {
		start++
	}
	end := cursor
	for end < len(value) && displayWidth(string(value[start:end+1])) <= columns {
		end++
	}
	return string(value[start:end]), displayWidth(string(value[start:cursor]))
}

const (
	keyUp     = "\x00up"
	keyDown   = "\x00down"
	keyLeft   = "\x00left"
	keyRight  = "\x00right"
	keyEnter  = "\x00enter"
	keyBack   = "\x00back"
	keyExit   = "\x00exit"
	keyDelete = "\x00delete"
	keyClear  = "\x00clear"
)

type keyReader struct {
	terminal *Terminal
	restore  func() error
	signals  <-chan os.Signal
	stop     func()
	refresh  func()
}

func (k *keyReader) close() error {
	var err error
	if k.restore != nil {
		err = k.restore()
	}
	if k.stop != nil {
		k.stop()
	}
	return err
}

func (k *keyReader) discardLineBreak() {
	if k.terminal.input != nil {
		return
	}
	for k.terminal.in.Buffered() > 0 {
		next, _ := k.terminal.in.Peek(1)
		if len(next) != 1 || (next[0] != '\r' && next[0] != '\n') {
			return
		}
		_, _ = k.terminal.in.ReadByte()
	}
}

func (k *keyReader) read() (string, error) {
	b, err := k.readByte(false)
	if errors.Is(err, io.EOF) {
		return "", ErrExit
	}
	if err != nil {
		return "", err
	}
	switch b {
	case '\r', '\n':
		if b == '\r' && k.terminal.in.Buffered() > 0 {
			if next, _ := k.terminal.in.Peek(1); len(next) == 1 && next[0] == '\n' {
				_, _ = k.terminal.in.ReadByte()
			}
		}
		return keyEnter, nil
	case 2:
		return keyBack, nil
	case 3:
		return "", errors.Join(ErrExit, context.Canceled)
	case 4:
		return keyExit, nil
	case 8, 127:
		return keyDelete, nil
	case 21:
		return keyClear, nil
	case 27:
		next, err := k.readByte(true)
		if err != nil {
			if errors.Is(err, ErrExit) {
				return "", err
			}
			return keyBack, nil
		}
		if next != '[' && next != 'O' {
			_ = k.terminal.in.UnreadByte()
			return keyBack, nil
		}
		code, err := k.readByte(true)
		if err != nil {
			return keyBack, nil
		}
		switch code {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		case 'C':
			return keyRight, nil
		case 'D':
			return keyLeft, nil
		}
		return "", nil
	}
	body := []byte{b}
	for b >= utf8.RuneSelf && !utf8.FullRune(body) && len(body) < utf8.UTFMax {
		next, err := k.readByte(false)
		if err != nil {
			return "", err
		}
		body = append(body, next)
	}
	return string(body), nil
}
