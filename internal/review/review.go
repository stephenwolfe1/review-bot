// Package review runs one review pass over a pull request: resolve the bot's
// own threads that new commits have fixed, review what changed, and post.
package review

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"

	"review-bot/internal/diff"
	"review-bot/internal/gh"
	"review-bot/internal/llm"
)

//go:embed prompts/*.md
var promptFS embed.FS

func prompt(name string) string {
	b, err := promptFS.ReadFile("prompts/" + name + ".md")
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Config controls a run.
type Config struct {
	PR            int
	Checks        []string // "bugs", "security"
	MinSeverity   string
	MinConfidence float64
	Verify        bool
	Ignore        []string
	MaxFiles      int
	MaxPatchLines int
	FullFileLines int // send whole file as context when it has at most this many lines
	MaxComments   int
	FailOn        string // severity; "" disables
	ReviewDrafts  bool
	// BotLogins are the accounts whose comments count as the bot's own. Only
	// their hidden markers are trusted, so a PR author can't forge them.
	BotLogins []string
	Force     bool // review even if head was already reviewed
	DryRun    bool
	Logger    *log.Logger
}

// DefaultIgnore skips files that are noise for review.
var DefaultIgnore = []string{
	"**/*.lock", "**/package-lock.json", "**/yarn.lock", "**/pnpm-lock.yaml", "**/go.sum", "**/Cargo.lock",
	"**/Package.resolved", "vendor/**", "**/node_modules/**", "**/*.min.js", "**/*.min.css", "**/*.map",
	"**/*.svg", "**/*.png", "**/*.jpg", "**/*.jpeg", "**/*.gif", "**/*.pdf", "**/*.pbxproj", "**/*.xcassets/**",
}

// Result summarises a run.
type Result struct {
	HeadSHA     string
	Incremental bool
	Resolved    int
	Posted      int
	Open        map[string]map[string]int // category -> severity -> count
	Failed      bool                      // FailOn threshold hit
	Skipped     []string
}

// Finding is one problem reported by the model.
type Finding struct {
	Path       string  `json:"-"`
	Category   string  `json:"-"`
	Line       int     `json:"line"`
	EndLine    int     `json:"end_line"`
	Severity   string  `json:"severity"`
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	Example    string  `json:"example"` // a concrete failing case
	Suggestion string  `json:"suggestion"`
	Confidence float64 `json:"confidence"`
	Model      string  `json:"-"`
}

type botThread struct {
	gh.Thread
	Meta Meta
}

func (t botThread) line() int {
	if t.Line > 0 {
		return t.Line
	}
	if t.Meta.Line > 0 {
		return t.Meta.Line
	}
	return t.OriginalLine
}

// Reviewer runs reviews.
type Reviewer struct {
	GH    *gh.Client
	LLM   *llm.Client
	Cfg   Config
	files map[string]string // content cache: path@sha -> text
}

func (r *Reviewer) logf(format string, args ...any) {
	if r.Cfg.Logger != nil {
		r.Cfg.Logger.Printf(format, args...)
	}
}

// Run performs one pass.
func (r *Reviewer) Run(ctx context.Context) (*Result, error) {
	cfg := r.Cfg
	r.files = map[string]string{}
	pr, err := r.GH.GetPR(ctx, cfg.PR)
	if err != nil {
		return nil, fmt.Errorf("get PR: %w", err)
	}
	res := &Result{HeadSHA: pr.Head.SHA, Open: map[string]map[string]int{}}
	if pr.State != "open" {
		r.logf("PR #%d is %s; nothing to do", pr.Number, pr.State)
		return res, nil
	}
	if pr.Draft && !cfg.ReviewDrafts {
		r.logf("PR #%d is a draft; skipping (set REVIEW_DRAFTS=true to review drafts)", pr.Number)
		return res, nil
	}
	head := pr.Head.SHA

	threads, err := r.GH.ReviewThreads(ctx, cfg.PR)
	if err != nil {
		return nil, fmt.Errorf("list review threads: %w", err)
	}
	var bot []botThread
	for _, t := range threads {
		m, ok := DecodeMeta(t.FirstBody)
		if !ok {
			continue
		}
		if !r.isBot(t.FirstAuthor) {
			r.logf("ignoring a review-bot marker in a thread by %q (not the bot)", t.FirstAuthor)
			continue
		}
		bot = append(bot, botThread{Thread: t, Meta: m})
	}

	comments, err := r.GH.ListIssueComments(ctx, cfg.PR)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	var summaryID int64
	var lastSHA string
	for _, c := range comments {
		sha, ok := ParseSummary(c.Body)
		if !ok {
			continue
		}
		if !r.isBot(c.User.Login) {
			r.logf("ignoring a review-bot summary marker in a comment by %q (not the bot)", c.User.Login)
			continue
		}
		summaryID, lastSHA = c.ID, sha
	}
	if lastSHA == head && !cfg.Force {
		r.logf("head %s already reviewed", short(head))
		res.Open = countOpen(bot, nil)
		res.Failed = r.failed(res.Open)
		return res, nil
	}

	prFiles, err := r.GH.ListPRFiles(ctx, cfg.PR)
	if err != nil {
		return nil, fmt.Errorf("list PR files: %w", err)
	}
	byName := map[string]gh.File{}
	for _, f := range prFiles {
		byName[f.Filename] = f
	}

	// Work out what changed since the last reviewed commit.
	var delta map[string]gh.File // nil = unknown (first run or history rewritten)
	if lastSHA != "" && lastSHA != head {
		cmp, err := r.GH.Compare(ctx, lastSHA, head)
		switch {
		case err != nil:
			r.logf("compare %s...%s failed (%v); doing a full review", short(lastSHA), short(head), err)
		case cmp.Status == "ahead":
			delta = map[string]gh.File{}
			for _, f := range cmp.Files {
				delta[f.Filename] = f
				if f.PreviousFilename != "" {
					delta[f.PreviousFilename] = f
				}
			}
			res.Incremental = true
		default:
			r.logf("compare status %q (force-push?); doing a full review", cmp.Status)
		}
	}

	// 1. Resolve pass.
	resolved := map[string]bool{}
	if lastSHA != "" && lastSHA != head {
		for _, t := range bot {
			if t.IsResolved {
				continue
			}
			ok, reason, err := r.checkResolved(ctx, t, byName, delta, head)
			if err != nil {
				r.logf("resolve check %s:%d: %v", t.Path, t.line(), err)
				continue
			}
			if !ok {
				continue
			}
			r.logf("resolving %s:%d %q: %s", t.Path, t.line(), t.Meta.Title, reason)
			if !cfg.DryRun {
				body := fmt.Sprintf("✅ Addressed in %s. %s", short(head), sanitizeText(oneLine(reason)))
				if err := r.GH.ReplyToThread(ctx, t.ID, body); err != nil {
					r.logf("reply: %v", err)
				}
				if err := r.GH.ResolveThread(ctx, t.ID); err != nil {
					r.logf("resolve: %v", err)
					continue
				}
			}
			resolved[t.ID] = true
			res.Resolved++
		}
	}

	// 2. Review pass.
	var scope []string
	for _, f := range prFiles {
		if delta != nil {
			if _, changed := delta[f.Filename]; !changed {
				continue
			}
		}
		if f.Status == "removed" {
			continue
		}
		if f.Patch == "" {
			res.Skipped = append(res.Skipped, f.Filename+" (binary or too large for a diff)")
			continue
		}
		if ignored(f.Filename, cfg.Ignore) {
			continue
		}
		scope = append(scope, f.Filename)
	}
	sort.Strings(scope)
	if cfg.MaxFiles > 0 && len(scope) > cfg.MaxFiles {
		for _, f := range scope[cfg.MaxFiles:] {
			res.Skipped = append(res.Skipped, f+" (over REVIEW_MAX_FILES)")
		}
		scope = scope[:cfg.MaxFiles]
	}

	var findings []Finding
	patches := map[string]*diff.Patch{}
	for _, name := range scope {
		f := byName[name]
		p := diff.Parse(f.Patch)
		patches[name] = p
		if cfg.MaxPatchLines > 0 && p.LineCount() > cfg.MaxPatchLines {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s (%d diff lines, over REVIEW_MAX_PATCH_LINES)", name, p.LineCount()))
			continue
		}
		content, _ := r.content(ctx, name, head)
		fc := r.fileContext(pr, name, p, content)
		var raw []Finding
		for _, check := range cfg.Checks {
			fs, err := r.reviewFile(ctx, fc, name, check)
			if err != nil {
				r.logf("review %s (%s): %v", name, check, err)
				continue
			}
			raw = append(raw, fs...)
		}
		// Filter and verify this file's findings straight away, while the
		// model server still has this file's prompt cached.
		kept := r.filter(raw, patches, bot)
		if cfg.Verify {
			kept = r.verifyAll(ctx, fc, kept, content)
		}
		findings = append(findings, kept...)
	}

	sort.SliceStable(findings, func(i, j int) bool {
		return SeverityRank(findings[i].Severity) > SeverityRank(findings[j].Severity)
	})
	if cfg.MaxComments > 0 && len(findings) > cfg.MaxComments {
		r.logf("capping %d findings at %d", len(findings), cfg.MaxComments)
		findings = findings[:cfg.MaxComments]
	}

	posted, err := r.post(ctx, head, findings)
	if err != nil {
		return nil, err
	}
	res.Posted = len(posted)

	res.Open = countOpen(bot, resolved)
	for _, f := range posted {
		addCount(res.Open, f.Category, f.Severity)
	}
	res.Failed = r.failed(res.Open)

	summary := r.renderSummary(head, lastSHA, res)
	if cfg.DryRun {
		r.logf("dry run: summary would be:\n%s", summary)
	} else if summaryID != 0 {
		err = r.GH.UpdateIssueComment(ctx, summaryID, summary)
	} else {
		err = r.GH.CreateIssueComment(ctx, cfg.PR, summary)
	}
	if err != nil {
		return res, fmt.Errorf("summary comment: %w", err)
	}
	return res, nil
}

// content returns a file's text at sha, cached.
func (r *Reviewer) content(ctx context.Context, p, sha string) (string, error) {
	key := p + "@" + sha
	if s, ok := r.files[key]; ok {
		return s, nil
	}
	s, err := r.GH.GetContent(ctx, p, sha)
	if err != nil && !errors.Is(err, gh.ErrNotFound) {
		return "", err
	}
	r.files[key] = s
	return s, err
}

// fileContext is the start of every prompt about a file: the PR, the diff
// and, for files up to REVIEW_FULL_FILE_LINES, the whole file. All calls for
// a file (each check, then each verification) start with exactly this text
// after the same system prompt, so a model server that caches prompts
// (Ollama does) reads the file once and reuses it.
func (r *Reviewer) fileContext(pr *gh.PR, name string, p *diff.Patch, content string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pull request title: %s\n", pr.Title)
	if desc := strings.TrimSpace(pr.Body); desc != "" {
		fmt.Fprintf(&b, "Pull request description:\n<<<\n%s\n>>>\n", truncate(desc, 2000))
	}
	fmt.Fprintf(&b, "\nFile: %s\n\nDiff (left gutter = new-file line number):\n<<<\n%s>>>\n", name, p.Annotated())
	if r.fullFile(content) {
		fmt.Fprintf(&b, "\nFull file after the change, for context:\n<<<\n%s>>>\n", diff.NumberLines(content, 1, 0))
	}
	return b.String()
}

func (r *Reviewer) fullFile(content string) bool {
	return content != "" && r.Cfg.FullFileLines > 0 && strings.Count(content, "\n") <= r.Cfg.FullFileLines
}

// messages builds a prompt: shared rules, the file's context, then the task.
func messages(fileContext, task string) []llm.Message {
	return []llm.Message{
		{Role: "system", Content: prompt("common")},
		{Role: "user", Content: fileContext + "\nYour task:\n" + task},
	}
}

func (r *Reviewer) reviewFile(ctx context.Context, fileContext, name, check string) ([]Finding, error) {
	r.logf("%s [%s]: waiting on the model…", name, check)
	msgs := messages(fileContext, prompt(check)+"\n"+prompt("findings"))
	var out struct {
		Findings []Finding `json:"findings"`
	}
	if err := r.LLM.ChatSchema(ctx, msgs, findingsSchema, &out); err != nil {
		return nil, err
	}
	model := r.lastModel()
	for i := range out.Findings {
		out.Findings[i].Path = name
		out.Findings[i].Category = check
		out.Findings[i].Model = model
	}
	r.logf("%s [%s]: %d raw finding(s)", name, check, len(out.Findings))
	return out.Findings, nil
}

// filter validates line anchors, applies thresholds and removes duplicates.
func (r *Reviewer) filter(in []Finding, patches map[string]*diff.Patch, bot []botThread) []Finding {
	var out []Finding
	minSev := SeverityRank(r.Cfg.MinSeverity)
	for _, f := range in {
		f.Severity = NormalizeSeverity(f.Severity)
		f.Title = strings.TrimSpace(f.Title)
		if f.Title == "" || strings.TrimSpace(f.Body) == "" {
			continue
		}
		if strings.TrimSpace(f.Example) == "" {
			r.logf("dropped %s:%d %q: no concrete failing example", f.Path, f.Line, f.Title)
			continue
		}
		if SeverityRank(f.Severity) < minSev || f.Confidence < r.Cfg.MinConfidence {
			continue
		}
		p := patches[f.Path]
		if p == nil {
			continue
		}
		line, ok := p.Nearest(f.Line, 3)
		if !ok {
			r.logf("dropped %s:%d %q: line not in diff", f.Path, f.Line, f.Title)
			continue
		}
		if line != f.Line {
			f.Suggestion = "" // the replacement no longer lines up
			f.EndLine = 0
		}
		f.Line = line
		if f.EndLine < f.Line || f.EndLine-f.Line > 30 || !p.SameHunk(f.Line, f.EndLine) {
			if f.EndLine != f.Line && f.EndLine != 0 {
				f.Suggestion = ""
			}
			f.EndLine = f.Line
		}
		if f.Suggestion != "" && suggestionTooBig(f.Suggestion, f.EndLine-f.Line+1) {
			r.logf("dropped the suggestion for %s:%d: much larger than the lines it replaces", f.Path, f.Line)
			f.Suggestion = ""
		}
		if dupOfThread(f, bot) {
			r.logf("skipped %s:%d %q: already raised", f.Path, f.Line, f.Title)
			continue
		}
		dup := false
		for i, o := range out {
			if similar(f.Path, f.Line, f.Title, f.Category, o.Path, o.Line, o.Title, o.Category) {
				if SeverityRank(f.Severity) > SeverityRank(o.Severity) {
					out[i] = f
				}
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, f)
		}
	}
	return out
}

func dupOfThread(f Finding, bot []botThread) bool {
	for _, t := range bot {
		// Resolved threads count too: if the author resolved it by hand
		// ("won't fix"), don't raise it again.
		if similar(f.Path, f.Line, f.Title, f.Category, t.Path, t.line(), t.Meta.Title, t.Meta.Category) {
			return true
		}
	}
	return false
}

func similar(p1 string, l1 int, t1, c1, p2 string, l2 int, t2, c2 string) bool {
	if p1 != p2 {
		return false
	}
	d := l1 - l2
	if d < 0 {
		d = -d
	}
	if d > 8 {
		return false
	}
	if c1 == c2 && d <= 2 {
		return true
	}
	return jaccard(words(t1), words(t2)) >= 0.4
}

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_')
	}) {
		if len(w) > 2 {
			m[w] = true
		}
	}
	return m
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// verdict is the verify pass's opinion of a finding.
type verdict struct {
	Valid        bool
	SuggestionOK bool
	Severity     string
	Reason       string
}

func (r *Reviewer) verify(ctx context.Context, fileContext string, f Finding, content string) (verdict, error) {
	r.logf("%s:%d: verifying %q…", f.Path, f.Line, f.Title)
	var b strings.Builder
	b.WriteString(prompt("verify"))
	fmt.Fprintf(&b, "\nThe finding (%s, %s) at line %d", f.Category, f.Severity, f.Line)
	if f.EndLine > f.Line {
		fmt.Fprintf(&b, "-%d", f.EndLine)
	}
	fmt.Fprintf(&b, ":\nTitle: %s\nDetails: %s\nExample: %s\n", f.Title, f.Body, f.Example)
	if f.Suggestion != "" {
		// Show exactly what would change: a suggestion anchored to the wrong
		// lines, or one that drops needed code, is easy to spot this way.
		end := f.EndLine
		if end < f.Line {
			end = f.Line
		}
		if content != "" {
			fmt.Fprintf(&b, "\nThe suggested change replaces lines %d-%d, which are currently:\n<<<\n%s>>>\nwith:\n<<<\n%s\n>>>\n",
				f.Line, end, diff.NumberLines(content, f.Line, end), strings.TrimRight(f.Suggestion, "\n"))
		} else {
			fmt.Fprintf(&b, "\nSuggested replacement for lines %d-%d:\n<<<\n%s\n>>>\n", f.Line, end, f.Suggestion)
		}
	}
	if content != "" && !r.fullFile(content) {
		// The file was too big to include in full; show the code around the finding.
		fmt.Fprintf(&b, "\nCode around the finding:\n<<<\n%s>>>\n", diff.NumberLines(content, f.Line-60, f.EndLine+60))
	}
	var out struct {
		Valid        bool   `json:"valid"`
		SuggestionOK *bool  `json:"suggestion_ok"`
		Severity     string `json:"severity"`
		Reason       string `json:"reason"`
	}
	if err := r.LLM.ChatSchema(ctx, messages(fileContext, b.String()), verifySchema, &out); err != nil {
		return verdict{}, err
	}
	return verdict{
		Valid:        out.Valid,
		SuggestionOK: out.SuggestionOK == nil || *out.SuggestionOK,
		Severity:     strings.TrimSpace(out.Severity),
		Reason:       out.Reason,
	}, nil
}

// verifyAll runs the verify pass over one file's findings.
func (r *Reviewer) verifyAll(ctx context.Context, fileContext string, findings []Finding, content string) []Finding {
	var kept []Finding
	for _, f := range findings {
		v, err := r.verify(ctx, fileContext, f, content)
		if err != nil {
			r.logf("verify %s:%d: %v (keeping)", f.Path, f.Line, err)
			kept = append(kept, f)
			continue
		}
		if !v.Valid {
			r.logf("dropped %s:%d %q: %s", f.Path, f.Line, f.Title, v.Reason)
			continue
		}
		if !v.SuggestionOK && f.Suggestion != "" {
			r.logf("dropped the suggestion for %s:%d: %s", f.Path, f.Line, v.Reason)
			f.Suggestion = ""
		}
		// The verifier may lower the severity, never raise it.
		if v.Severity != "" && SeverityRank(v.Severity) < SeverityRank(f.Severity) {
			r.logf("%s:%d %q: severity %s -> %s", f.Path, f.Line, f.Title, f.Severity, NormalizeSeverity(v.Severity))
			f.Severity = NormalizeSeverity(v.Severity)
		}
		if SeverityRank(f.Severity) < SeverityRank(r.Cfg.MinSeverity) {
			continue
		}
		kept = append(kept, f)
	}
	return kept
}

func (r *Reviewer) checkResolved(ctx context.Context, t botThread, byName, delta map[string]gh.File, head string) (bool, string, error) {
	f, inPR := byName[t.Path]
	if !inPR {
		return true, "This file is no longer changed by the pull request.", nil
	}
	if f.Status == "removed" {
		return true, "The file was removed.", nil
	}
	var deltaPatch string
	if delta != nil {
		d, changed := delta[t.Path]
		if !changed {
			return false, "", nil // untouched since the comment: still open
		}
		deltaPatch = d.Patch
	}
	content, err := r.content(ctx, t.Path, head)
	if err != nil {
		return false, "", err
	}
	if content == "" {
		return true, "The file no longer exists.", nil
	}
	line := t.line()
	r.logf("%s:%d: checking whether %q is fixed…", t.Path, line, t.Meta.Title)
	var b strings.Builder
	fmt.Fprintf(&b, "File: %s\n\nOriginal finding (%s, %s) near line %d:\nTitle: %s\nComment:\n<<<\n%s\n>>>\n",
		t.Path, t.Meta.Category, t.Meta.Severity, line, t.Meta.Title, stripMarker(t.FirstBody))
	if deltaPatch != "" {
		fmt.Fprintf(&b, "\nChanges to this file since the comment:\n<<<\n%s>>>\n", diff.Parse(deltaPatch).Annotated())
	}
	lo, hi := line-40, line+40
	if r.Cfg.FullFileLines > 0 && strings.Count(content, "\n") <= r.Cfg.FullFileLines {
		lo, hi = 1, 0
	}
	fmt.Fprintf(&b, "\nCurrent code:\n<<<\n%s>>>\n", diff.NumberLines(content, lo, hi))

	var out struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	msgs := []llm.Message{{Role: "system", Content: prompt("resolve")}, {Role: "user", Content: b.String()}}
	if err := r.LLM.ChatSchema(ctx, msgs, resolveSchema, &out); err != nil {
		return false, "", err
	}
	return strings.EqualFold(out.Status, "fixed"), strings.TrimSpace(out.Reason), nil
}

func (r *Reviewer) post(ctx context.Context, head string, fs []Finding) ([]Finding, error) {
	if len(fs) == 0 {
		return nil, nil
	}
	comments := make([]gh.ReviewComment, len(fs))
	for i, f := range fs {
		comments[i] = toComment(f, head, f.Model)
	}
	if r.Cfg.DryRun {
		for _, c := range comments {
			r.logf("dry run: would comment on %s:%d\n%s", c.Path, c.Line, c.Body)
		}
		return fs, nil
	}
	body := fmt.Sprintf("review-bot found %d issue(s) in %s.", len(fs), short(head))
	err := r.GH.CreateReview(ctx, r.Cfg.PR, head, body, comments)
	if err == nil {
		return fs, nil
	}
	var apiErr *gh.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 422 {
		return nil, fmt.Errorf("create review: %w", err)
	}
	// One bad anchor fails the whole review; post individually instead.
	r.logf("batch review rejected (%v); posting comments one at a time", err)
	var posted []Finding
	for i, c := range comments {
		if err := r.GH.CreateReview(ctx, r.Cfg.PR, head, "", []gh.ReviewComment{c}); err != nil {
			r.logf("comment %s:%d rejected: %v", c.Path, c.Line, err)
			continue
		}
		posted = append(posted, fs[i])
	}
	return posted, nil
}

var sevIcon = map[string]string{"critical": "🛑", "high": "🔴", "medium": "🟠", "low": "🟡"}

func toComment(f Finding, head, model string) gh.ReviewComment {
	var b strings.Builder
	// Title and body come from the model, so they're sanitized; the
	// suggestion sits in a code block, where Markdown isn't rendered.
	fmt.Fprintf(&b, "%s **%s · %s** — %s\n\n%s\n", sevIcon[f.Severity], title(f.Severity), title(f.Category),
		sanitizeText(oneLine(f.Title)), sanitizeText(strings.TrimSpace(f.Body)))
	if ex := strings.TrimSpace(f.Example); ex != "" {
		fmt.Fprintf(&b, "\n**Example:** %s\n", sanitizeText(oneLine(ex)))
	}
	if s := strings.TrimRight(f.Suggestion, "\n"); s != "" && !strings.Contains(s, "```") {
		fmt.Fprintf(&b, "\n```suggestion\n%s\n```\n", s)
		b.WriteString("<sub>⚠️ Written by an AI model. Read the change before applying it.</sub>\n")
	}
	fmt.Fprintf(&b, "\n<sub>review-bot · %s</sub>\n", model)
	b.WriteString(EncodeMeta(Meta{Severity: f.Severity, Category: f.Category, Title: f.Title, Path: f.Path, Line: f.Line, SHA: head}))
	c := gh.ReviewComment{Path: f.Path, Body: b.String(), Line: f.EndLine, Side: "RIGHT"}
	if c.Line < f.Line {
		c.Line = f.Line
	}
	if f.EndLine > f.Line {
		c.StartLine, c.StartSide = f.Line, "RIGHT"
	}
	return c
}

func (r *Reviewer) lastModel() string {
	if u := r.LLM.Used(); len(u) > 0 {
		return u[len(u)-1]
	}
	return r.LLM.Current()
}

// isBot reports whether login is one of the bot's accounts. GitHub shows
// app accounts as "name[bot]" in REST and "name" in GraphQL, so both match.
func (r *Reviewer) isBot(login string) bool {
	norm := func(s string) string { return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "[bot]")) }
	l := norm(login)
	if l == "" {
		return false
	}
	logins := r.Cfg.BotLogins
	if len(logins) == 0 {
		logins = DefaultBotLogins
	}
	for _, b := range logins {
		if norm(b) == l {
			return true
		}
	}
	return false
}

// DefaultBotLogins is the account GITHUB_TOKEN posts as.
var DefaultBotLogins = []string{"github-actions[bot]"}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func countOpen(bot []botThread, resolvedNow map[string]bool) map[string]map[string]int {
	m := map[string]map[string]int{}
	for _, t := range bot {
		if t.IsResolved || resolvedNow[t.ID] {
			continue
		}
		addCount(m, t.Meta.Category, t.Meta.Severity)
	}
	return m
}

func addCount(m map[string]map[string]int, cat, sev string) {
	if m[cat] == nil {
		m[cat] = map[string]int{}
	}
	m[cat][sev]++
}

func (r *Reviewer) failed(open map[string]map[string]int) bool {
	if r.Cfg.FailOn == "" || r.Cfg.FailOn == "none" {
		return false
	}
	min := SeverityRank(r.Cfg.FailOn)
	for _, sevs := range open {
		for sev, n := range sevs {
			if n > 0 && SeverityRank(sev) >= min {
				return true
			}
		}
	}
	return false
}

func (r *Reviewer) renderSummary(head, lastSHA string, res *Result) string {
	var b strings.Builder
	b.WriteString(SummaryMarker(head) + "\n")
	b.WriteString("### 🤖 review-bot\n\n")
	models := strings.Join(r.LLM.Used(), "`, `")
	if models == "" {
		models = r.LLM.Current()
	}
	if res.Incremental {
		fmt.Fprintf(&b, "Reviewed changes in `%s` since `%s` with `%s`.\n\n", short(head), short(lastSHA), models)
	} else {
		fmt.Fprintf(&b, "Reviewed `%s` with `%s`.\n\n", short(head), models)
	}
	sevs := []string{"critical", "high", "medium", "low"}
	b.WriteString("| Open | 🛑 Critical | 🔴 High | 🟠 Medium | 🟡 Low |\n|---|---|---|---|---|\n")
	cats := append([]string{}, r.Cfg.Checks...)
	for c := range res.Open {
		if !contains(cats, c) {
			cats = append(cats, c)
		}
	}
	for _, c := range cats {
		fmt.Fprintf(&b, "| %s |", title(c))
		for _, s := range sevs {
			fmt.Fprintf(&b, " %d |", res.Open[c][s])
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nThis run: %d new, %d resolved.", res.Posted, res.Resolved)
	if r.Cfg.FailOn != "" && r.Cfg.FailOn != "none" {
		if res.Failed {
			fmt.Fprintf(&b, " ❌ Open %s-or-worse issues are blocking this check.", r.Cfg.FailOn)
		} else {
			b.WriteString(" ✅ Nothing blocking.")
		}
	}
	b.WriteString("\n")
	if len(res.Skipped) > 0 {
		b.WriteString("\n<details><summary>Skipped files</summary>\n\n")
		for _, s := range res.Skipped {
			fmt.Fprintf(&b, "- `%s`\n", s)
		}
		b.WriteString("\n</details>\n")
	}
	b.WriteString("\n<sub>Resolve a thread yourself to dismiss it; it won't be raised again.</sub>\n")
	return b.String()
}

func ignored(p string, patterns []string) bool {
	for _, pat := range patterns {
		if matchGlob(pat, p) {
			return true
		}
	}
	return false
}

// matchGlob supports path.Match patterns plus a leading "**/" (any directory)
// and a trailing "/**" (anything below).
func matchGlob(pat, p string) bool {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return false
	}
	if strings.HasSuffix(pat, "/**") {
		prefix := strings.TrimSuffix(pat, "/**")
		if strings.HasPrefix(prefix, "**/") {
			inner := strings.TrimPrefix(prefix, "**/")
			return strings.HasPrefix(p, inner+"/") || strings.Contains(p, "/"+inner+"/") ||
				matchAnyDir(inner, p)
		}
		return strings.HasPrefix(p, prefix+"/")
	}
	if strings.HasPrefix(pat, "**/") {
		inner := strings.TrimPrefix(pat, "**/")
		parts := strings.Split(p, "/")
		for i := range parts {
			if ok, _ := path.Match(inner, strings.Join(parts[i:], "/")); ok {
				return true
			}
		}
		return false
	}
	if ok, _ := path.Match(pat, p); ok {
		return true
	}
	if !strings.Contains(pat, "/") {
		ok, _ := path.Match(pat, path.Base(p))
		return ok
	}
	return false
}

func matchAnyDir(pat, p string) bool {
	parts := strings.Split(p, "/")
	for _, d := range parts[:len(parts)-1] {
		if ok, _ := path.Match(pat, d); ok {
			return true
		}
	}
	return false
}

func stripMarker(s string) string {
	if i := strings.Index(s, findingPrefix); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "<sub>review-bot"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
