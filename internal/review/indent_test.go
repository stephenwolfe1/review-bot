package review

import "testing"

func TestReindent(t *testing.T) {
	content := "func f() {\n\tif x {\n\t\treturn a\n\t}\n}\n"
	cases := []struct {
		name, sugg string
		line       int
		want       string
	}{
		// The cell-notes test: a one-line fix written flush left.
		{"flush left", "return b", 3, "\t\treturn b"},
		{"block flush left keeps relative indent", "if y {\n\treturn b\n}", 2, "\tif y {\n\t\treturn b\n\t}"},
		{"already indented", "\t\treturn b", 3, "\t\treturn b"},
		{"partly indented is left alone", " return b", 3, " return b"},
		{"blank lines stay blank", "a\n\nb", 3, "\t\ta\n\n\t\tb"},
		{"unindented original", "func g() {", 1, "func g() {"},
		{"line out of range", "x", 99, "x"},
		{"no content", "x", 3, "x"},
	}
	for _, c := range cases {
		src := content
		if c.name == "no content" {
			src = ""
		}
		if got := reindent(c.sugg, src, c.line); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
