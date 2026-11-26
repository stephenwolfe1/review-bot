package review

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
)

// The bot keeps no database: everything it needs to remember lives in hidden
// HTML comments in the comments it posts, so GitHub is the source of truth.

const (
	findingPrefix = "<!-- review-bot:finding "
	summaryPrefix = "<!-- review-bot:summary "
	markerSuffix  = " -->"
)

// Meta is stored in each inline comment the bot posts.
type Meta struct {
	V        int    `json:"v"`
	Severity string `json:"sev"`
	Category string `json:"cat"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	SHA      string `json:"sha"`
}

// EncodeMeta renders the hidden marker for a finding.
func EncodeMeta(m Meta) string {
	m.V = 1
	b, _ := json.Marshal(m)
	return findingPrefix + base64.RawURLEncoding.EncodeToString(b) + markerSuffix
}

// DecodeMeta finds and decodes a finding marker in a comment body.
func DecodeMeta(body string) (Meta, bool) {
	i := strings.Index(body, findingPrefix)
	if i < 0 {
		return Meta{}, false
	}
	rest := body[i+len(findingPrefix):]
	j := strings.Index(rest, markerSuffix)
	if j < 0 {
		return Meta{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(rest[:j]))
	if err != nil {
		return Meta{}, false
	}
	var m Meta
	if json.Unmarshal(raw, &m) != nil {
		return Meta{}, false
	}
	return m, true
}

var summaryRE = regexp.MustCompile(`<!-- review-bot:summary sha=([0-9a-f]{7,40}) -->`)

// SummaryMarker is the hidden marker for the summary comment.
func SummaryMarker(sha string) string { return summaryPrefix + "sha=" + sha + markerSuffix }

// ParseSummary returns the last reviewed SHA from a summary comment.
func ParseSummary(body string) (sha string, ok bool) {
	if !strings.Contains(body, summaryPrefix) {
		return "", false
	}
	if m := summaryRE.FindStringSubmatch(body); m != nil {
		return m[1], true
	}
	return "", true
}

var sevRank = map[string]int{"info": 0, "low": 1, "medium": 2, "high": 3, "critical": 4}

// SeverityRank orders severities; unknown values rank as low.
func SeverityRank(s string) int {
	if r, ok := sevRank[strings.ToLower(s)]; ok {
		return r
	}
	return 1
}

// NormalizeSeverity maps model output onto the known set.
func NormalizeSeverity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "critical", "high", "medium", "low":
		return s
	case "blocker":
		return "critical"
	case "major", "error":
		return "high"
	case "moderate", "warning", "minor":
		return "medium"
	default:
		return "low"
	}
}
