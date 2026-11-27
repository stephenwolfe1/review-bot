package review

import (
	"context"
	"strings"
	"testing"
)

func TestIdentifiersIn(t *testing.T) {
	got := strings.Join(identifiersIn(
		"Incorrect verification context in verifyAll function",
		"The verifyAll function passes fileContext to `r.verify`, but parse_header() and the Reviewer's FileContext differ.",
	), ",")
	for _, want := range []string{"verifyAll", "fileContext", "verify", "parse_header", "FileContext"} {
		if !strings.Contains(","+got+",", ","+want+",") {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	for _, unwanted := range []string{"Incorrect", "function", "The"} {
		if strings.Contains(","+got+",", ","+unwanted+",") {
			t.Errorf("%s should not count as an identifier: %s", unwanted, got)
		}
	}
}

// A big Go file where the finding (line ~10) talks about verify, defined
// much further down: the definition is found, with its whole body.
func goFile() string {
	var b strings.Builder
	b.WriteString("package review\n\nfunc (r *Reviewer) verifyAll(ctx context.Context, fileContext string) {\n")
	b.WriteString("\tfor _, f := range fs {\n\t\tv, err := r.verify(ctx, fileContext, f, content)\n\t}\n}\n")
	for i := 0; i < 150; i++ {
		b.WriteString("// filler\n")
	}
	b.WriteString("func (r *Reviewer) verify(ctx context.Context, fileContext string, f Finding, content string) (verdict, error) {\n")
	b.WriteString("\treturn verdict{}, nil\n}\n\nfunc other() {}\n")
	return b.String()
}

func TestDefinitionsFor(t *testing.T) {
	content := goFile()
	f := Finding{Line: 5, Title: "Incorrect verification context in verifyAll function",
		Body: "verifyAll passes fileContext, but the verify function expects it as its first argument."}
	defs := definitionsFor(f, content, 1, 65)
	if !strings.Contains(defs, "verify (line 158)") || !strings.Contains(defs, "fileContext string, f Finding, content string) (verdict, error)") {
		t.Fatalf("verify's definition missing:\n%s", defs)
	}
	if !strings.Contains(defs, "return verdict{}, nil") || strings.Contains(defs, "func other") {
		t.Errorf("definition should end at its closing brace:\n%s", defs)
	}
	if strings.Contains(defs, "verifyAll (line") {
		t.Errorf("verifyAll is already visible and shouldn't be repeated:\n%s", defs)
	}
	if definitionsFor(f, "", 1, 10) != "" {
		t.Error("no content, no definitions")
	}
}

func TestDefinitionsOtherLanguages(t *testing.T) {
	py := "import os\n\ndef parse_header(s):\n    a, b = s.split()\n    return a\n\nclass Thing:\n    pass\n"
	defs := definitionsFor(Finding{Body: "parse_header() returns the wrong field of Thing"}, py, 0, 0)
	if !strings.Contains(defs, "parse_header (line 3)") || !strings.Contains(defs, "return a") || strings.Contains(defs, "class Thing:\n") && !strings.Contains(defs, "Thing (line 7)") {
		t.Errorf("python definitions:\n%s", defs)
	}
	if strings.Contains(strings.SplitN(defs, "Thing (line", 2)[0], "class Thing") {
		t.Errorf("parse_header's block should stop before class Thing:\n%s", defs)
	}
	ts := "export async function loadUser(id: string) {\n  return db.get(id)\n}\n"
	if !strings.Contains(definitionsFor(Finding{Body: "loadUser(id) never awaits"}, ts, 0, 0), "loadUser (line 1)") {
		t.Error("TS function not found")
	}
}

// End to end: for a file too big to send whole, the verify prompt carries
// the definition the finding refers to.
func TestVerifyGetsDefinitions(t *testing.T) {
	_, fl, r := setup(t)
	r.Cfg.FullFileLines = 50
	f := Finding{Path: "internal/review/review.go", Line: 5, EndLine: 5, Category: "bugs", Severity: "high",
		Title: "Incorrect verification context in verifyAll function", Body: "the verify function expects fileContext first",
		Example: "verifyAll(ctx, fc) -> verify gets the wrong argument"}
	if _, err := r.verify(context.Background(), "File: internal/review/review.go\n", f, goFile()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Definitions the finding refers to", "verify (line 158)", "(verdict, error)"} {
		if !strings.Contains(fl.lastVerify, want) {
			t.Errorf("verify prompt missing %q", want)
		}
	}
}
