package review

import (
	"testing"

	"review-bot/internal/diff"
)

func TestSecurityTopic(t *testing.T) {
	cases := []struct {
		title, body string
		want        bool
	}{
		// From the seeded run (internal/seeded on test/seeded-bugs).
		{"SQL injection vulnerability in FindUser function", "user input concatenated into the query", true},
		{"Path traversal vulnerability in Download function", "joins user input onto baseDir", true},
		{"Command injection vulnerability in Thumbnail function", "user input in a shell command", true},
		{"Hard-coded admin password", "The admin password is hardcoded in the source code.", true},
		{"TLS certificate verification disabled", "vulnerable to man-in-the-middle attacks", true},
		{"Array bounds error in Total function", "The loop condition `i <= len(items)` reads past the end.", false},
		{"Division by zero in Average function", "len(items) is zero and the division panics.", false},
		{"Nil map dereference in CountNames function", "`counts` is never initialised, so `counts[n]++` panics.", false},
		{"Data race in TotalScore function", "goroutines modify `total` without synchronization", false},
		{"Error ignored in LoadConfig function", "a nil file handle is used, leading to a panic", false},
		// Attack words in the body don't make a crash a security finding.
		{"Nil map write panics", "An attacker could send a request that crashes the server.", false},
		// A vague title with a specific body does.
		{"Unvalidated input in handler", "The value reaches db.Query unescaped: SQL injection.", true},
		{"Token parsing off by one", "The lexer drops the last token.", false},
	}
	for _, c := range cases {
		if got := securityTopic(Finding{Title: c.title, Body: c.body}); got != c.want {
			t.Errorf("securityTopic(%q) = %v, want %v", c.title, got, c.want)
		}
	}
}

// The seeded run's failure: the security check's copy of a hedged bug won the
// merge on its label and dodged the bugs check's Medium cap.
func TestFilterRelabelsNonSecurity(t *testing.T) {
	r := &Reviewer{Cfg: Config{MinSeverity: "high"}}
	p := map[string]*diff.Patch{"a.go": diff.Parse("@@ -0,0 +1,3 @@\n+a\n+b\n+c")}
	race := Finding{Path: "a.go", Line: 2, Severity: "high", Confidence: 1,
		Title: "Data race in TotalScore", Body: "goroutines write total unsynchronised",
		Example: "TotalScore with 2 users could return 1"}
	bug, sec := race, race
	bug.Category, sec.Category = "bugs", "security"
	if got := r.filter([]Finding{bug, sec}, p, nil); len(got) != 0 {
		t.Fatalf("hedged bug survived as %+v", got)
	}

	inj := Finding{Path: "a.go", Line: 2, Severity: "high", Confidence: 1, Category: "security",
		Title: "SQL injection in FindUser", Body: "name is concatenated into the query",
		Example: "an attacker could pass a crafted name"}
	got := r.filter([]Finding{bug, inj}, p, nil)
	if len(got) != 1 || got[0].Category != "security" || got[0].Severity != "high" {
		t.Fatalf("got %+v, want the security finding", got)
	}
}
