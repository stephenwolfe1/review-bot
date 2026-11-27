package review

import "testing"

func TestProfiles(t *testing.T) {
	std, err := LookupProfile("")
	if err != nil || std.FullFileLines != 500 || std.MaxComments != 15 || std.NumCtx != 16384 {
		t.Fatalf("default should be standard: %+v %v", std, err)
	}
	lite, _ := LookupProfile("Lite")
	full, _ := LookupProfile("full")
	if !(lite.FullFileLines < std.FullFileLines && std.FullFileLines < full.FullFileLines) {
		t.Error("full-file context should grow lite < standard < full")
	}
	if SeverityRank(lite.MinSeverity) < SeverityRank(std.MinSeverity) || lite.MinConfidence <= std.MinConfidence {
		t.Error("lite should be at least as strict as standard, and want more confidence")
	}
	if std.MinSeverity != "high" || SeverityRank(full.MinSeverity) >= SeverityRank(std.MinSeverity) {
		t.Error("standard posts high and critical only; full posts more")
	}
	if full.NumCtx <= std.NumCtx {
		t.Error("full needs a bigger context for 1500-line files")
	}
	for name, p := range Profiles {
		if !p.Verify {
			t.Errorf("%s: verification should stay on", name)
		}
	}
	if _, err := LookupProfile("turbo"); err == nil {
		t.Error("unknown profile should fail")
	}
}
