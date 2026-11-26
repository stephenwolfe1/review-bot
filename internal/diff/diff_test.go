package diff

import (
	"strings"
	"testing"
)

const patch = `@@ -1,4 +1,5 @@
 package main
-import "fmt"
+import (
+	"fmt"
+)

 func main() {
@@ -20,3 +21,3 @@ func other() {
 	a := 1
-	b := 2
+	b := 3
 	return
\ No newline at end of file`

func TestParse(t *testing.T) {
	p := Parse(patch)
	if len(p.Hunks) != 2 {
		t.Fatalf("hunks = %d", len(p.Hunks))
	}
	for _, n := range []int{1, 2, 3, 4, 5, 6, 21, 22, 23} {
		if !p.Commentable(n) {
			t.Errorf("line %d should be commentable", n)
		}
	}
	for _, n := range []int{7, 20, 24} {
		if p.Commentable(n) {
			t.Errorf("line %d should not be commentable", n)
		}
	}
	if !p.Added(2) || !p.Added(22) || p.Added(1) {
		t.Error("added lines wrong")
	}
	if !p.SameHunk(2, 4) || p.SameHunk(4, 22) {
		t.Error("SameHunk wrong")
	}
	if n, ok := p.Nearest(8, 3); !ok || n != 6 {
		t.Errorf("Nearest(8) = %d, %v", n, ok)
	}
	if _, ok := p.Nearest(14, 3); ok {
		t.Error("Nearest(14) should fail")
	}
	a := p.Annotated()
	if !strings.Contains(a, "    22 + \tb := 3") || !strings.Contains(a, "       - \tb := 2") {
		t.Errorf("annotated:\n%s", a)
	}
}

func TestNumberLines(t *testing.T) {
	got := NumberLines("a\nb\nc", 2, 9)
	if got != "     2  b\n     3  c\n" {
		t.Errorf("got %q", got)
	}
}
