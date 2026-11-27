package review

import (
	"regexp"
	"strings"
)

// Everything the model writes is untrusted: a PR can steer it with prompt
// injection. Before posting model text, the bot defuses the Markdown features
// that would let that text act under the bot's name:
//
//   - raw HTML, including <!-- review-bot:... --> markers the bot relies on
//   - @mentions, which notify people
//   - links and images, which would look like the bot's own advice
//
// Text inside `code spans` is left as it is: GitHub renders it literally.

var (
	// <https://...> autolinks, [text](url) and ![alt](url), and bare URLs.
	linkRE    = regexp.MustCompile(`<(https?://[^>\s]+)>|!?\[([^\]]*)\]\(([^)\s]*)[^)]*\)|https?://[^\s<>()\x60]+`)
	mentionRE = regexp.MustCompile(`(^|[^\w\x60])@([A-Za-z0-9][\w-]*)`)
)

// sanitizeText makes model text safe to post as Markdown.
func sanitizeText(s string) string {
	var b strings.Builder
	for i, part := range splitCodeSpans(s) {
		if i%2 == 1 {
			b.WriteString(part) // a code span, backticks included
			continue
		}
		b.WriteString(sanitizePlain(part))
	}
	return b.String()
}

// splitCodeSpans splits s into alternating plain text and code spans
// (even indexes plain, odd indexes code). An unmatched backtick is plain text.
func splitCodeSpans(s string) []string {
	var parts []string
	for {
		start := strings.IndexByte(s, '`')
		if start < 0 {
			break
		}
		n := 1
		for start+n < len(s) && s[start+n] == '`' {
			n++
		}
		fence := s[start : start+n]
		end := strings.Index(s[start+n:], fence)
		if end < 0 {
			break
		}
		end += start + n + n
		parts = append(parts, s[:start], s[start:end])
		s = s[end:]
	}
	return append(parts, s)
}

func sanitizePlain(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range linkRE.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(escapeHTML(s[last:m[0]]))
		switch {
		case m[2] >= 0: // <url>
			b.WriteString(codeURL(s[m[2]:m[3]]))
		case m[4] >= 0: // [text](url) or ![alt](url)
			b.WriteString(escapeHTML(s[m[4]:m[5]]))
			if u := s[m[6]:m[7]]; u != "" {
				b.WriteString(" (" + codeURL(u) + ")")
			}
		default: // bare URL
			b.WriteString(codeURL(s[m[0]:m[1]]))
		}
		last = m[1]
	}
	b.WriteString(escapeHTML(s[last:]))
	// A zero-width space after @ keeps the text readable but stops the mention.
	return mentionRE.ReplaceAllString(b.String(), "$1@​$2")
}

// codeURL shows a URL as inline code, so it's visible but not clickable.
func codeURL(u string) string {
	return "`" + strings.ReplaceAll(u, "`", "") + "`"
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

// suggestionTooBig reports whether a suggested replacement is out of
// proportion to the lines it replaces. A large rewrite under the bot's name
// is easy to apply without reading; drop it and keep the comment.
func suggestionTooBig(suggestion string, replacedLines int) bool {
	if replacedLines < 1 {
		replacedLines = 1
	}
	lines := strings.Count(strings.TrimRight(suggestion, "\n"), "\n") + 1
	limit := replacedLines * 3
	if limit < replacedLines+10 {
		limit = replacedLines + 10
	}
	return lines > limit
}
