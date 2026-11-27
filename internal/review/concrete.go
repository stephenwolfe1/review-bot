package review

import (
	"regexp"
	"strings"
)

// Models are told every finding needs a concrete failing example, but a model
// grading its own claims lets vague ones through ("this causes the LLM to lack
// context"). So the example is checked in code:
//
//   - A bug's example must name a specific value, or the finding is dropped.
//   - A bug whose example is hedged ("may", "could", "likely") is capped at
//     Medium rather than dropped: real bugs are sometimes worded that way.
//   - Security examples are naturally hedged and value-free ("an attacker
//     could intercept…"), so a security finding only needs an example at all.
//
// These are heuristics, measured against a seeded test (internal/seeded on
// the test/seeded-bugs branch): dropping on hedges alone cost two real
// findings out of ten.

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
	// A real failing case is usually definite ("Div(1, 0) panics"). Hedged
	// ones describe something that might happen.
	hedgeRE = regexp.MustCompile(`(?i)\b(could|may|might|likely|probably|potentially|possibly|perhaps|in a way that)\b`)
)

// exampleHasValue reports whether an example names at least one specific
// value: a number, a quoted string, code in backticks that isn't just a
// name, or an edge value such as nil, empty, zero or negative.
func exampleHasValue(ex string) bool {
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

// exampleHedged reports whether an example describes something that might
// happen ("could", "may", "likely", …) rather than something that does.
func exampleHedged(ex string) bool { return hedgeRE.MatchString(ex) }
