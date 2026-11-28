package review

import "strings"

// reindent fixes a suggestion the model wrote flush left. Models often drop
// the leading whitespace of the code they replace, and GitHub applies a
// suggestion verbatim, so accepting it would break the file's indentation.
//
// When the suggestion's first non-blank line has no indentation but the line
// it replaces does, every non-blank line gets that indentation, so a block
// keeps its inner indentation relative to the first line. A suggestion whose
// first line is indented is left alone: the model either got it right or
// changed it on purpose.
func reindent(suggestion, content string, line int) string {
	if suggestion == "" || content == "" || line < 1 {
		return suggestion
	}
	lines := strings.Split(content, "\n")
	if line > len(lines) {
		return suggestion
	}
	orig := lines[line-1]
	indent := orig[:len(orig)-len(strings.TrimLeft(orig, " \t"))]
	if indent == "" {
		return suggestion
	}
	out := strings.Split(suggestion, "\n")
	for _, l := range out {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if l[0] == ' ' || l[0] == '\t' {
			return suggestion
		}
		break
	}
	for i, l := range out {
		if strings.TrimSpace(l) != "" {
			out[i] = indent + l
		}
	}
	return strings.Join(out, "\n")
}
