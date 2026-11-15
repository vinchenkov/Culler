package verbs

import (
	"encoding/json"
	"strings"
	"testing"
)

// renderedBrief is the text of a run brief, read back the way an agent reads
// it: a header, a role directive, then parts under `## ` headings.
type renderedBrief struct {
	Header    string
	Title     string
	Directive string
	Parts     []renderedPart
}

type renderedPart struct {
	Heading     string
	Explanation string
	Facts       map[string]string
	Text        string
	Rows        string
}

func (b renderedBrief) part(heading string) *renderedPart {
	for i := range b.Parts {
		if b.Parts[i].Heading == heading {
			return &b.Parts[i]
		}
	}
	return nil
}

func (b renderedBrief) headings() []string {
	out := make([]string, 0, len(b.Parts))
	for _, p := range b.Parts {
		out = append(out, p.Heading)
	}
	return out
}

// rows decodes the JSON block of a part. An absent part decodes nothing.
func (p *renderedPart) rows(t *testing.T, into any) {
	t.Helper()
	if p == nil || p.Rows == "" {
		return
	}
	if err := json.Unmarshal([]byte(p.Rows), into); err != nil {
		t.Fatalf("part %q holds no valid JSON block: %v\n%s", p.Heading, err, p.Rows)
	}
}

func parseRenderedBrief(t *testing.T, text string) renderedBrief {
	t.Helper()
	lines := strings.Split(text, "\n")
	var b renderedBrief
	if len(lines) < 3 || !strings.HasPrefix(lines[0], spawnBriefHeader) {
		t.Fatalf("the run brief has no header line:\n%s", text)
	}
	b.Header = lines[0]
	var part *renderedPart
	var block []string
	closing := ""
	flush := func(tag string) {
		body := strings.Join(block, "\n")
		switch {
		case tag == "</role_directive>":
			b.Directive = body
		case tag == "```":
			part.Rows = body
		default:
			part.Text = body
		}
		block, closing = nil, ""
	}
	for _, line := range lines[1:] {
		switch {
		case closing != "":
			if line == closing {
				flush(closing)
			} else {
				block = append(block, line)
			}
		case strings.HasPrefix(line, "# "):
			b.Title = strings.TrimPrefix(line, "# ")
		case strings.HasPrefix(line, "## "):
			b.Parts = append(b.Parts, renderedPart{Heading: strings.TrimPrefix(line, "## "), Facts: map[string]string{}})
			part = &b.Parts[len(b.Parts)-1]
		case line == "```json":
			closing = "```"
		case strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") && !strings.HasPrefix(line, "</"):
			closing = "</" + strings.TrimPrefix(line, "<")
		case part == nil || line == "":
		case strings.HasPrefix(line, "- "):
			label, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), ": ")
			if !ok {
				t.Fatalf("part %q holds a fact without a label: %q", part.Heading, line)
			}
			part.Facts[label] = value
		default:
			part.Explanation = strings.TrimSpace(part.Explanation + " " + line)
		}
	}
	if closing != "" {
		t.Fatalf("the run brief leaves a block open, expected %q:\n%s", closing, text)
	}
	return b
}
