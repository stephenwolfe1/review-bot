package review

import (
	"regexp"
	"strings"
)

// Models are told every finding needs a concrete failing example, and the
// verifier is told to reject vague ones, but a model grading its own claims
// lets vague ones through ("this causes the LLM to lack context"). This check
// is done in code instead: an example has to contain at least one specific
// value. It's a heuristic, and can drop a real finding written in prose; for
// a reviewer whose main problem is noise, that's the right trade.

var (
	// "line 12" and "lines 3-9" are locations, not values.
	lineRefRE = regexp.MustCompile(`(?i)\blines?\s+\d+(\s*[-–]\s*\d+)?`)
	numberRE  = regexp.MustCompile(`(^|[^\w.])-?\d+(\.\d+)?\b`)
	// Single quotes count only as quotes, not apostrophes: "it's" isn't a string.
	quotedRE = regexp.MustCompile(`"[^"]*"|(^|[^\w])'[^'\n]+'($|[^\w])`)
	codeRE   = regexp.MustCompile("`([^`]+)`")
	// A plain name in backticks (`verifyAll`, `r.verify()`) isn't a value.
	plainNameRE = regexp.MustCompile(`^[A-Za-z_][\w.]*(\(\))?$`)
	edgeWordRE  = regexp.MustCompile(`(?i)\b(nil|null|none|undefined|nan|empty|zero|negative|true|false)\b`)
	// A real failing case is definite ("Div(1, 0) panics"). Hedged ones
	// describe something that might happen, which is how vague findings
	// slip a quoted value past the check above.
	hedgeRE = regexp.MustCompile(`(?i)\b(could|may|might|potentially|possibly|perhaps|in a way that)\b`)
)

// concreteExample reports whether an example is definite (no "could",
// "may", "might", …) and names at least one specific value: a number, a
// quoted string, code in backticks that isn't just a name, or an edge value
// such as nil, empty, zero or negative.
func concreteExample(ex string) bool {
	if hedgeRE.MatchString(ex) {
		return false
	}
	ex = lineRefRE.ReplaceAllString(ex, "")
	if numberRE.MatchString(ex) || quotedRE.MatchString(ex) || edgeWordRE.MatchString(ex) {
		return true
	}
	for _, m := range codeRE.FindAllStringSubmatch(ex, -1) {
		if !plainNameRE.MatchString(strings.TrimSpace(m[1])) {
			return true
		}
	}
	return false
}
