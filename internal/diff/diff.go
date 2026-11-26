// Package diff parses GitHub unified-diff patches so findings can be anchored
// to lines GitHub will accept for inline review comments.
package diff

import (
	"fmt"
	"strconv"
	"strings"
)

// Line is one line of a hunk. Old/New are 0 when the line does not exist on
// that side.
type Line struct {
	Kind byte // '+', '-', or ' '
	Old  int
	New  int
	Text string
}

// Hunk is one @@ section of a patch.
type Hunk struct {
	Header string
	Lines  []Line
}

// Patch is a parsed file patch.
type Patch struct {
	Hunks []Hunk
	// hunkOf maps a new-file line number that can carry a comment (added or
	// context line) to the index of its hunk.
	hunkOf map[int]int
	added  map[int]bool
}

// Parse parses the "patch" field GitHub returns for a pull request file.
func Parse(patch string) *Patch {
	p := &Patch{hunkOf: map[int]int{}, added: map[int]bool{}}
	var oldN, newN int
	cur := -1
	rawLines := strings.Split(patch, "\n")
	for i, raw := range rawLines {
		if raw == "" && i < len(rawLines)-1 {
			raw = " " // blank context line whose leading space was stripped
		}
		if strings.HasPrefix(raw, "@@") {
			oldN, newN = parseHeader(raw)
			p.Hunks = append(p.Hunks, Hunk{Header: raw})
			cur = len(p.Hunks) - 1
			continue
		}
		if cur < 0 || raw == "" {
			continue
		}
		h := &p.Hunks[cur]
		switch raw[0] {
		case '+':
			h.Lines = append(h.Lines, Line{Kind: '+', New: newN, Text: raw[1:]})
			p.hunkOf[newN] = cur
			p.added[newN] = true
			newN++
		case '-':
			h.Lines = append(h.Lines, Line{Kind: '-', Old: oldN, Text: raw[1:]})
			oldN++
		case ' ':
			h.Lines = append(h.Lines, Line{Kind: ' ', Old: oldN, New: newN, Text: raw[1:]})
			p.hunkOf[newN] = cur
			oldN++
			newN++
		}
		// '\' (no newline at end of file) and anything else is ignored.
	}
	return p
}

// parseHeader reads "@@ -a,b +c,d @@ ..." and returns a and c.
func parseHeader(h string) (oldStart, newStart int) {
	f := strings.Fields(h)
	if len(f) < 3 {
		return 1, 1
	}
	return leadingInt(strings.TrimPrefix(f[1], "-")), leadingInt(strings.TrimPrefix(f[2], "+"))
}

func leadingInt(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 1
	}
	return n
}

// Commentable reports whether GitHub accepts an inline comment on new-file
// line n (RIGHT side).
func (p *Patch) Commentable(n int) bool {
	_, ok := p.hunkOf[n]
	return ok
}

// Added reports whether new-file line n was added by this patch.
func (p *Patch) Added(n int) bool { return p.added[n] }

// SameHunk reports whether both lines are commentable and in the same hunk,
// which GitHub requires for multi-line comments.
func (p *Patch) SameHunk(a, b int) bool {
	ha, ok1 := p.hunkOf[a]
	hb, ok2 := p.hunkOf[b]
	return ok1 && ok2 && ha == hb
}

// Nearest returns the commentable line closest to n within maxDist,
// preferring added lines.
func (p *Patch) Nearest(n, maxDist int) (int, bool) {
	if p.Commentable(n) {
		return n, true
	}
	for d := 1; d <= maxDist; d++ {
		for _, c := range []int{n - d, n + d} {
			if p.added[c] {
				return c, true
			}
		}
	}
	for d := 1; d <= maxDist; d++ {
		for _, c := range []int{n - d, n + d} {
			if p.Commentable(c) {
				return c, true
			}
		}
	}
	return 0, false
}

// LineCount is the number of lines in the patch body.
func (p *Patch) LineCount() int {
	n := 0
	for _, h := range p.Hunks {
		n += len(h.Lines)
	}
	return n
}

// Annotated renders the patch with new-file line numbers in a left gutter so
// the model can cite exact lines. Removed lines have no number.
func (p *Patch) Annotated() string {
	var b strings.Builder
	for _, h := range p.Hunks {
		b.WriteString(h.Header)
		b.WriteByte('\n')
		for _, l := range h.Lines {
			switch l.Kind {
			case '+':
				fmt.Fprintf(&b, "%6d + %s\n", l.New, l.Text)
			case ' ':
				fmt.Fprintf(&b, "%6d   %s\n", l.New, l.Text)
			case '-':
				fmt.Fprintf(&b, "%6s - %s\n", "", l.Text)
			}
		}
	}
	return b.String()
}

// NumberLines renders text with 1-based line numbers, limited to [from, to]
// (inclusive, clamped). Pass from=1,to=0 for the whole text.
func NumberLines(text string, from, to int) string {
	lines := strings.Split(text, "\n")
	if from < 1 {
		from = 1
	}
	if to <= 0 || to > len(lines) {
		to = len(lines)
	}
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "%6d  %s\n", i, lines[i-1])
	}
	return b.String()
}
