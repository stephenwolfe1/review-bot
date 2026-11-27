package review

import "regexp"

// The security check also reports plain bugs (an off-by-one, a nil map), and
// those would skip the bugs check's example rules and win duplicate merges on
// the Security label. So a security finding stays Security only if it names a
// security problem; otherwise it is relabelled as a bug before filtering, and
// the hedge cap and severity floor apply to it like any other bug.
//
// Titles are short and specific, so they are matched against the broad list.
// Bodies are matched only against terms that can't describe a plain bug: a
// nil-map write-up may well say "an attacker could crash the server".
var (
	securityStrongRE = regexp.MustCompile(`(?i)\b(` +
		`(sql|command|shell|code|template|header|log|ldap|xpath|prompt)[- ]injection|injection (attack|vulnerabilit\w*)|` +
		`(path|directory) traversal|xss|cross[- ]site|csrf|ssrf|xxe|remote code execution|rce|zip slip|` +
		`hard-?coded (admin )?(key|secret|credential|password|token)s?|credentials?|passwords?|api[- ]?keys?|private keys?|` +
		`(access|auth|bearer|session) tokens?|insecureskipverify|certificate (verification|validation)|man[- ]in[- ]the[- ]middle|mitm|` +
		`authentication|authori[sz]ation|privilege escalation|access control|deseriali[sz]\w*|open redirect|` +
		`weak (hash|crypto\w*|cipher|random\w*)|(insecure|predictable) random\w*` +
		`)\b`)
	securityTitleRE = regexp.MustCompile(`(?i)\b(` +
		`injection|traversal|secrets?|tls|ssl|certificates?|auth\w*|unauthori[sz]ed|privileges?|permissions?|` +
		`bypass|impersonat\w*|spoof\w*|crypto\w*|encrypt\w*|md5|sha-?1|redirect|sanitiz\w*|untrusted|` +
		`exposure|exposed|leak\w*|sensitive|denial of service|dos|toctou|symlink|vulnerab\w*|insecure|unsafe (input|user)` +
		`)\b`)
)

func securityTopic(f Finding) bool {
	return securityStrongRE.MatchString(f.Title) || securityTitleRE.MatchString(f.Title) ||
		securityStrongRE.MatchString(f.Body)
}
