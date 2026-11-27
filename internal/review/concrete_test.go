package review

import "testing"

func TestConcreteExample(t *testing.T) {
	concrete := []string{
		"Div(1, 0) panics; it should return 0",
		`ParseRatio("3/0") panics; it should return ErrZeroDivisor`,
		"an empty list -> items[0] panics with index out of range",
		"user is nil when the session expired, so user.ID panics",
		"filename `../../etc/passwd` is read from outside the upload directory",
		"name `x' OR '1'='1` returns every row",
		"a timeout of -1 is treated as no timeout",
		"passing 'abc' as the port crashes the parser",
	}
	vague := []string{
		// The two false positives from PR 7, as the model wrote them.
		"When verifyAll is called with a fileContext containing full file content, each individual verification call will receive this context but the verify function's logic assumes it's already part of the user message. This can cause the LLM to misinterpret what code it's reviewing.",
		"When a file has more than REVIEW_FULL_FILE_LINES lines and is not included in full, the verifyAll function calls verify with fileContext but the verify function tries to show code around the finding without including the actual file content.",
		"`verifyAll` calls `r.verify()` with the wrong context",
		"see line 289: this could lead to problems",
		"",
	}
	for _, ex := range concrete {
		if !concreteExample(ex) {
			t.Errorf("should be concrete: %q", ex)
		}
	}
	for _, ex := range vague {
		if concreteExample(ex) {
			t.Errorf("should be vague: %q", ex)
		}
	}
}
