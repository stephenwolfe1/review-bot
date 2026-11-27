package review

import "testing"

func TestExampleHasValue(t *testing.T) {
	withValue := []string{
		"Div(1, 0) panics; it should return 0",
		`ParseRatio("3/0") panics; it should return ErrZeroDivisor`,
		"an empty list -> items[0] panics with index out of range",
		"user is nil when the session expired, so user.ID panics",
		"filename `../../etc/passwd` is read from outside the upload directory",
		"name `x' OR '1'='1` returns every row",
		"a timeout of -1 is treated as no timeout",
		"passing 'abc' as the port crashes the parser",
	}
	without := []string{
		// False positives from PR 7, as the model wrote them.
		"When verifyAll is called with a fileContext containing full file content, each individual verification call will receive this context but the verify function's logic assumes it's already part of the user message. This can cause the LLM to misinterpret what code it's reviewing.",
		"When a file has more than REVIEW_FULL_FILE_LINES lines and is not included in full, the verifyAll function calls verify with fileContext but the verify function tries to show code around the finding without including the actual file content.",
		"`verifyAll` calls `r.verify()` with the wrong context",
		"see line 289: this breaks",
		"",
	}
	for _, ex := range withValue {
		if !exampleHasValue(ex) {
			t.Errorf("should have a value: %q", ex)
		}
	}
	for _, ex := range without {
		if exampleHasValue(ex) {
			t.Errorf("should have no value: %q", ex)
		}
	}
}

func TestExampleHedged(t *testing.T) {
	hedged := []string{
		"a timeout of -1 might be treated as no timeout",
		"Calling `TotalScore([]User{{\"a\", 1}, {\"b\", 2}})` may result in incorrect totals",
		`If REVIEW_PROFILE is set to "invalid_profile", LookupProfile will likely return an error`,
	}
	for _, ex := range hedged {
		if !exampleHedged(ex) {
			t.Errorf("should be hedged: %q", ex)
		}
	}
	if exampleHedged("Div(1, 0) panics; it should return 0") {
		t.Error("a definite example isn't hedged")
	}
}

// The rules from the seeded test: a hedged bug is kept at Medium, a bug with
// no value is dropped, and a value-free security finding is kept as it is.
func TestExampleRules(t *testing.T) {
	r := &Reviewer{}
	race := Finding{Category: "bugs", Severity: "high", Title: "Data race",
		Example: "Calling `TotalScore([]User{{\"a\", 1}, {\"b\", 2}})` may result in incorrect totals"}
	if !r.exampleOK(&race) || race.Severity != "medium" {
		t.Errorf("hedged bug should be kept at medium: %+v", race)
	}
	vague := Finding{Category: "bugs", Severity: "high", Example: "verifyAll passes the wrong context to verify"}
	if r.exampleOK(&vague) {
		t.Error("a bug with no specific value should be dropped")
	}
	tls := Finding{Category: "security", Severity: "high",
		Example: "An attacker could intercept communications with the payment provider, potentially stealing card data"}
	if !r.exampleOK(&tls) || tls.Severity != "high" {
		t.Errorf("security finding should be kept as it is: %+v", tls)
	}
	none := Finding{Category: "security", Severity: "high"}
	if r.exampleOK(&none) {
		t.Error("a finding with no example at all should be dropped")
	}
}
