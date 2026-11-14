package onboarding

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

const (
	textHeading = "1"
	textCurrent = "1;36"
	textDone    = "32"
	textAction  = "33"
	textMuted   = "2"
	textNotice  = "1;32"
)

var stageIDs = []string{"welcome", "prepare", "agents", "review", "start", "dashboard"}
var defaultStageTitles = []string{
	"Welcome", "Prepare this Mac", "Connect an agent",
	"Review your setup", "Start Culler", "Open your dashboard",
}

type terminalLine struct{ text, style string }

func (t *Terminal) SetStageTitles(titles []string) {
	if len(titles) == len(stageIDs) {
		t.stageTitles = slices.Clone(titles)
	}
}

func (t *Terminal) Complete() {
	t.writeLines(t.stageLines(len(stageIDs)+1, "Mac setup complete", stageIDs, ""))
}

func (t *Terminal) styled(text, style string) string {
	if !t.color || style == "" || text == "" {
		return text
	}
	return "\x1b[" + style + "m" + text + "\x1b[0m"
}

func (t *Terminal) lines(text, style string, indent int) []terminalLine {
	return t.hangingLines(text, style, indent, 0)
}

// hangingLines indents each wrapped continuation line by hang more columns than the first line.
func (t *Terminal) hangingLines(text, style string, indent, hang int) []terminalLine {
	indent = min(indent, max(0, t.Width()-4))
	hang = min(hang, max(0, t.Width()-indent-4))
	var lines []terminalLine
	for index, line := range wrapHanging(text, t.Width()-indent, t.Width()-indent-hang) {
		lead := indent
		if index > 0 {
			lead += hang
		}
		lines = append(lines, terminalLine{strings.Repeat(" ", lead) + line, style})
	}
	return lines
}

func (t *Terminal) writeLines(lines []terminalLine) {
	var text strings.Builder
	for _, line := range lines {
		text.WriteString(t.styled(line.text, line.style))
		text.WriteByte('\n')
	}
	out, ok := t.out.(*terminalOutput)
	if !ok {
		fmt.Fprint(t.out, text.String())
		return
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	_, _ = out.write([]byte(text.String()))
	if len(lines) > 0 && lines[len(lines)-1].text == "" {
		t.spaced = out.sequence
	}
}

// afterBlank reports that the latest output ends with a blank line, or that no output exists.
func (t *Terminal) afterBlank() bool {
	out, ok := t.out.(*terminalOutput)
	if !ok {
		return true
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	return t.spaced == out.sequence
}

// panelBlock puts one blank line between a titled panel and the output above it.
// A panel without a title continues the output above it.
func (t *Terminal) panelBlock(title string, content []string, problem bool) []terminalLine {
	lines := t.sectionLines(title, content, problem)
	if title == "" || t.afterBlank() {
		return lines
	}
	return append([]terminalLine{{}}, lines...)
}

func (t *Terminal) stageLines(index int, title string, completed []string, next string) []terminalLine {
	if len(t.stageTitles) == 0 {
		t.stageTitles = slices.Clone(defaultStageTitles)
	}
	index = max(1, min(index, len(stageIDs)+1))
	var lines []terminalLine
	// One blank line separates the progress from the output above it.
	if !t.afterBlank() {
		lines = append(lines, terminalLine{})
	}
	if !t.identity {
		lines = append(lines, t.lines("Culler", textHeading, 0)...)
		lines = append(lines, terminalLine{})
		t.identity = true
	}
	var progress strings.Builder
	var plain strings.Builder
	var pending []string
	for i, id := range stageIDs {
		mark, style := "·", textMuted
		complete := slices.Contains(completed, id) || slices.Contains(completed, t.stageTitles[i]) || slices.Contains(completed, defaultStageTitles[i])
		switch {
		case i == index-1:
			mark, style = "●", textCurrent
		case complete:
			mark, style = "✓", textDone
		case i < index-1:
			mark, style = "○", textAction
			pending = append(pending, t.stageTitles[i])
		}
		if i > 0 {
			progress.WriteString("  ")
			plain.WriteString("  ")
		}
		progress.WriteString(t.styled(mark, style))
		plain.WriteString(mark)
	}
	if displayWidth(plain.String()) <= t.Width() {
		// Each mark has its own state color. Its width excludes color controls.
		lines = append(lines, terminalLine{progress.String(), ""})
	} else {
		lines = append(lines, t.lines(plain.String(), "", 0)...)
	}
	if index > len(stageIDs) {
		lines = append(lines, t.lines("Mac setup complete", textDone, 0)...)
	} else {
		lines = append(lines, t.lines(fmt.Sprintf("%d of %d  %s", index, len(stageIDs), progressText(title)), textCurrent, 0)...)
	}
	if len(pending) > 0 {
		lines = append(lines, t.lines("Pending: "+strings.Join(pending, ", "), textAction, 0)...)
	}
	if next == "" && index < len(t.stageTitles) {
		next = t.stageTitles[index]
	}
	if next != "" {
		lines = append(lines, t.lines("Next: "+progressText(next), textMuted, 0)...)
	}
	return append(lines, terminalLine{})
}

// sectionLines marks the title of a problem panel with "!" and the action style.
// A line that holds only a path or an address stays on one line.
func (t *Terminal) sectionLines(title string, content []string, problem bool) []terminalLine {
	var lines []terminalLine
	switch {
	case title != "" && problem:
		lines = append(lines, t.prefixedLines("! ", title, textAction)...)
	case title != "":
		lines = append(lines, t.lines(title, textHeading, 0)...)
	}
	for _, line := range content {
		value := cleanText(line)
		switch {
		case !copyValue(value):
			lines = append(lines, t.hangingLines(line, "", 2, 2)...)
		case displayWidth(value)+2 <= t.Width():
			lines = append(lines, terminalLine{"  " + value, ""})
		default:
			// The terminal wraps the value, so that a copy of the value holds no line break.
			lines = append(lines, terminalLine{value, ""})
		}
	}
	return append(lines, terminalLine{})
}

// copyValue reports a line that holds only a path or an address.
func copyValue(line string) bool {
	return line != "" && !strings.ContainsFunc(line, unicode.IsSpace) &&
		(strings.HasPrefix(line, "/") || strings.HasPrefix(line, "~/") || strings.Contains(line, "://"))
}

func (t *Terminal) choiceLines(prompt string, choices []Choice, selected int) []terminalLine {
	lines := t.lines(prompt, textHeading, 0)
	start, end := 0, len(choices)
	if t.ansi && slices.ContainsFunc(choices, func(choice Choice) bool { return choice.AlwaysDetail }) {
		start = selected / 2 * 2
		end = min(start+2, len(choices))
		if len(choices) > 2 {
			lines = append(lines, t.lines(fmt.Sprintf("Choices %d to %d of %d.", start+1, end, len(choices)), textMuted, 0)...)
		}
	}
	for i, choice := range choices {
		if i < start || i >= end {
			continue
		}
		prefix, style := "  ", ""
		if i == selected {
			prefix, style = "› ", textCurrent
		}
		if !t.ansi {
			prefix += fmt.Sprintf("%d. ", i+1)
		}
		lines = append(lines, t.prefixedLines(prefix, progressText(choice.Label), style)...)
		if (i == selected || choice.AlwaysDetail) && choice.Detail != "" {
			// A deeper indent than the label text marks the description without color.
			lines = append(lines, t.lines(choice.Detail, textMuted, displayWidth(prefix)+2)...)
		}
	}
	lines = append(lines, terminalLine{})
	help := "↑↓ move  enter select  b back  q exit"
	if !t.ansi {
		help = "Number/Enter select  b back  q exit"
	}
	return append(lines, t.lines(help, textMuted, 0)...)
}

func (t *Terminal) answerLines(prompt, answer string) []terminalLine {
	text := cleanText(answer)
	if prompt != "" {
		text = progressText(prompt) + ": " + text
	}
	lines := t.prefixedLines("› ", text, "")
	return append(lines, terminalLine{})
}

func (t *Terminal) prefixedLines(prefix, text, style string) []terminalLine {
	if t.Width() <= displayWidth(prefix) {
		return t.lines(prefix+text, style, 0)
	}
	var result []terminalLine
	for index, line := range wrapText(text, t.Width()-displayWidth(prefix)) {
		lead := prefix
		if index > 0 {
			lead = strings.Repeat(" ", displayWidth(prefix))
		}
		result = append(result, terminalLine{lead + line, style})
	}
	return result
}

func (t *Terminal) inputLines(prompt Prompt, runes []rune, cursor int) ([]terminalLine, int) {
	heading := prompt.Label
	if prompt.Progress != "" {
		heading += " (" + prompt.Progress + ")"
	}
	lines := t.lines(heading, textHeading, 0)
	hints := slices.Clone(prompt.Hints)
	secret, value := prompt.Secret, prompt.Value
	if secret {
		hints = append(hints, "Your input stays concealed.")
	}
	if !t.ansi && value != "" {
		if !secret {
			hints = append(hints, "Current: "+value)
		}
		hints = append(hints, "Enter keeps the current value. :clear removes it.")
	}
	for _, hint := range hints {
		lines = append(lines, t.hangingLines(hint, textMuted, 2, 2)...)
	}
	controls := "enter confirm  ctrl+b back  ctrl+c exit"
	if prompt.CancelOnly {
		controls = "enter confirm  ctrl+c cancel"
	}
	lines = append(lines, t.lines(controls, textMuted, 0)...)
	if !t.ansi {
		hint := ":back or :exit also work."
		if prompt.CancelOnly {
			hint = ":exit also cancels."
		}
		lines = append(lines, t.lines(hint, textMuted, 0)...)
		return append(lines, terminalLine{"> ", ""}), 0
	}
	text, offset := inputView(runes, cursor, t.Width()-3, secret)
	return append(lines, terminalLine{"> " + text, textCurrent}), displayWidth(text) - offset
}

func (t *Terminal) resultLines(label string, err error) []terminalLine {
	mark, status, style := "✓", "Complete: ", textDone
	switch {
	case errors.Is(err, ErrActionRequired):
		return t.lines("! "+label+" requires attention", textAction, 0)
	case errors.Is(err, context.Canceled):
		mark, status, style = "!", "Cancelled: ", textAction
	case errors.Is(err, ErrExit), errors.Is(err, ErrBack):
		mark, status, style = "!", "Stopped: ", textAction
	case err != nil:
		mark, status, style = "!", "Failed: ", textAction
	case t.ansi:
		status = ""
	}
	return t.lines(mark+" "+status+label, style, 0)
}

type terminalFrame struct {
	terminal *Terminal
	lines    []terminalLine
	input    bool
	sequence uint64
	// joined is true when the frame starts directly below the blank line of an answer.
	joined bool
	// spaced is true when the frame starts directly below a blank line.
	spaced bool
}

func (frame *terminalFrame) paint(lines []terminalLine, input bool, back int) {
	frame.draw(lines, input, back, false)
}

// answer replaces the frame with an answer.
// An answer directly below another answer replaces the blank line between them.
func (frame *terminalFrame) answer(lines []terminalLine) {
	frame.draw(lines, false, 0, true)
}

func (frame *terminalFrame) draw(lines []terminalLine, input bool, back int, answer bool) {
	t := frame.terminal
	out := t.out.(*terminalOutput)
	out.mu.Lock()
	defer out.mu.Unlock()
	if len(frame.lines) == 0 && frame.sequence == 0 {
		frame.joined = t.answered != 0 && t.answered == out.sequence
		frame.spaced = t.spaced == out.sequence
	}
	var text strings.Builder
	erase := t.ansi && len(frame.lines) > 0 && frame.sequence == out.sequence
	if erase {
		rows := 0
		for _, line := range frame.lines {
			rows += max(1, (displayWidth(line.text)+t.Width()-1)/max(1, t.Width()))
		}
		if frame.input {
			rows--
		}
		if answer && frame.joined {
			rows++
		}
		text.WriteByte('\r')
		if rows > 0 {
			fmt.Fprintf(&text, "\x1b[%dA", rows)
		}
		text.WriteString("\x1b[J")
	}
	for i, line := range lines {
		text.WriteString(t.styled(line.text, line.style))
		if !input || i < len(lines)-1 {
			text.WriteByte('\n')
		}
	}
	if t.ansi && back > 0 {
		fmt.Fprintf(&text, "\x1b[%dD", back)
	}
	_, _ = out.write([]byte(text.String()))
	frame.lines, frame.input, frame.sequence = lines, input, out.sequence
	if answer {
		t.answered = out.sequence
	}
	// An erased frame leaves the output above it as the latest output.
	if !input && len(lines) > 0 && lines[len(lines)-1].text == "" || len(lines) == 0 && erase && frame.spaced {
		t.spaced = out.sequence
	}
}

func (frame *terminalFrame) inputValue(line terminalLine, back int) {
	out := frame.terminal.out.(*terminalOutput)
	out.mu.Lock()
	defer out.mu.Unlock()
	text := "\r\x1b[2K" + frame.terminal.styled(line.text, line.style)
	if back > 0 {
		text += fmt.Sprintf("\x1b[%dD", back)
	}
	_, _ = out.write([]byte(text))
	frame.lines[len(frame.lines)-1] = line
	frame.sequence = out.sequence
}
