package review

import (
	"fmt"
	"sort"
	"strings"
)

// Profile is a set of defaults chosen together with REVIEW_PROFILE. Any
// individual setting still overrides its profile value.
type Profile struct {
	FullFileLines int     // REVIEW_FULL_FILE_LINES
	MaxFiles      int     // REVIEW_MAX_FILES
	MaxPatchLines int     // REVIEW_MAX_PATCH_LINES
	MaxComments   int     // REVIEW_MAX_COMMENTS
	MinSeverity   string  // REVIEW_MIN_SEVERITY
	MinConfidence float64 // REVIEW_MIN_CONFIDENCE
	Verify        bool    // REVIEW_VERIFY
	NumCtx        int     // LLM_NUM_CTX (Ollama)
}

// Profiles: lite for laptops and small models, standard for most setups,
// full for a strong model with time to spare.
//
// lite and standard post High and Critical findings only. Measured with the
// seeded test (test/seeded-bugs) and on real PRs: real problems come out High
// or Critical, while findings that are capped (a hedged example) or lowered by
// the verifier land at Medium and are mostly noise. full posts everything
// down to Low, for when you want the speculative findings too.
var Profiles = map[string]Profile{
	"lite": {
		FullFileLines: 200, MaxFiles: 20, MaxPatchLines: 400, MaxComments: 8,
		MinSeverity: "high", MinConfidence: 0.8, Verify: true, NumCtx: 16384,
	},
	"standard": {
		FullFileLines: 500, MaxFiles: 40, MaxPatchLines: 800, MaxComments: 15,
		MinSeverity: "high", MinConfidence: 0.7, Verify: true, NumCtx: 16384,
	},
	"full": {
		FullFileLines: 1500, MaxFiles: 100, MaxPatchLines: 2000, MaxComments: 25,
		MinSeverity: "low", MinConfidence: 0.6, Verify: true, NumCtx: 32768,
	},
}

// LookupProfile returns the named profile ("" means standard).
func LookupProfile(name string) (Profile, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = "standard"
	}
	if p, ok := Profiles[name]; ok {
		return p, nil
	}
	names := make([]string, 0, len(Profiles))
	for n := range Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return Profile{}, fmt.Errorf("REVIEW_PROFILE: unknown profile %q (use %s)", name, strings.Join(names, ", "))
}
