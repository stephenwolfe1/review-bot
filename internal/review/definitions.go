package review

import (
	"fmt"
	"regexp"
	"strings"

	"review-bot/internal/diff"
)

// When a file is too big to send whole, the verifier sees only the diff and
// the code around a finding. Findings often hinge on a function or type
// defined elsewhere in the same file ("the call doesn't match verify's
// signature"), so the verifier is also given those definitions, found by
// name. It's a plain text search for common definition forms, not a parser:
// good enough to put the right code in front of the model.

var (
	// Identifier-looking words: `in backticks`, followed by "(", camelCase,
	// PascalCase with an inner capital, or snake_case.
	backtickIdentRE = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_.]*)\\(?\\)?`")
	callIdentRE     = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\(`)
	codeyIdentRE    = regexp.MustCompile(`\b([a-z]+[A-Z][A-Za-z0-9]*|[A-Z][a-z0-9]+[A-Z][A-Za-z0-9]*|[a-z0-9]+_[a-z0-9_]+)\b`)
	// Plain words named as code: "the verify function", "method parse".
	// Words that aren't real definitions find nothing, so they cost nothing.
	kindBeforeRE = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s+(?:function|method|func|type|struct|class)\b`)
	kindAfterRE  = regexp.MustCompile(`\b(?:function|method|func|type|struct|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

const (
	maxDefinitions    = 3
	maxDefinitionLine = 40
)

// identifiersIn returns the identifiers a finding mentions, in order.
func identifiersIn(texts ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if i := strings.LastIndexByte(id, '.'); i >= 0 { // r.verify -> verify
			id = id[i+1:]
		}
		if len(id) < 3 || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, t := range texts {
		for _, re := range []*regexp.Regexp{backtickIdentRE, callIdentRE, kindBeforeRE, kindAfterRE, codeyIdentRE} {
			for _, m := range re.FindAllStringSubmatch(t, -1) {
				add(m[1])
			}
		}
	}
	return out
}

// definitionPattern matches a line that defines name in common languages:
// Go func and method, type; Python def/class; JS/TS function, class, const;
// Rust fn, struct, enum, trait; Swift/Kotlin func/fun, class, struct.
func definitionPattern(name string) *regexp.Regexp {
	n := regexp.QuoteMeta(name)
	return regexp.MustCompile(`^\s*(?:(?:pub(?:\([^)]*\))?|export|async|static|public|private|protected|internal|open|override|final|inline)\s+)*` +
		`(?:func\s*(?:\([^)]*\)\s*)?` + n + `\b|fun\s+` + n + `\b|fn\s+` + n + `\b|def\s+` + n + `\b|function\s*\*?\s*` + n + `\b|` +
		`(?:class|struct|enum|trait|interface|protocol|type)\s+` + n + `\b|(?:const|let|var)\s+` + n + `\s*[:=])`)
}

// definitionsFor finds the definitions of identifiers a finding mentions,
// skipping any that start inside the lines [shownFrom, shownTo], which the
// verifier already sees. It returns them as numbered code, or "".
func definitionsFor(f Finding, content string, shownFrom, shownTo int) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	var b strings.Builder
	found := 0
	for _, id := range identifiersIn(f.Title, f.Body, f.Example) {
		if found == maxDefinitions {
			break
		}
		re := definitionPattern(id)
		for i, l := range lines {
			n := i + 1
			if !re.MatchString(l) {
				continue
			}
			if n >= shownFrom && n <= shownTo {
				break // already visible
			}
			end := definitionEnd(lines, i)
			fmt.Fprintf(&b, "%s (line %d):\n<<<\n%s>>>\n", id, n, diff.NumberLines(content, n, end))
			found++
			break
		}
	}
	return b.String()
}

// definitionEnd finds the last line of the definition starting at index
// start: a closing brace at the definition's indentation, or the end of an
// indented block, capped at maxDefinitionLine lines.
func definitionEnd(lines []string, start int) int {
	indent := leadingSpace(lines[start])
	last := start + maxDefinitionLine - 1
	if last >= len(lines) {
		last = len(lines) - 1
	}
	braces := strings.Contains(lines[start], "{")
	for i := start + 1; i <= last; i++ {
		l := lines[i]
		if braces {
			if strings.TrimSpace(l) == "}" && leadingSpace(l) == indent {
				return i + 1
			}
			continue
		}
		// Indentation-based (Python): ends before the next line at or
		// left of the definition's indentation.
		if strings.TrimSpace(l) != "" && len(leadingSpace(l)) <= len(indent) {
			return i
		}
	}
	return last + 1
}

func leadingSpace(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}
