// Command review-bot reviews a GitHub pull request with a local model.
//
// In GitHub Actions it reads the PR from GITHUB_EVENT_PATH. Locally:
//
//	GITHUB_TOKEN=$(gh auth token) review-bot -repo owner/name -pr 12 -dry-run
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"review-bot/internal/gh"
	"review-bot/internal/llm"
	"review-bot/internal/review"
)

var version = "dev"

func main() {
	logger := log.New(os.Stderr, "review-bot: ", 0)
	repo := flag.String("repo", env("GITHUB_REPOSITORY", ""), "owner/name")
	prNum := flag.Int("pr", envInt("PR_NUMBER", 0), "pull request number (default: from GITHUB_EVENT_PATH)")
	dry := flag.Bool("dry-run", envBool("DRY_RUN", false), "print what would be posted instead of posting")
	force := flag.Bool("force", envBool("REVIEW_FORCE", false), "review even if the head commit was already reviewed")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	owner, name, ok := strings.Cut(*repo, "/")
	if !ok || owner == "" || name == "" {
		logger.Fatal("set GITHUB_REPOSITORY or -repo to owner/name")
	}
	if *prNum == 0 {
		n, err := prFromEvent(os.Getenv("GITHUB_EVENT_PATH"))
		if err != nil {
			logger.Fatalf("no PR number: %v (set -pr or PR_NUMBER)", err)
		}
		*prNum = n
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		logger.Fatal("GITHUB_TOKEN is required")
	}

	api := env("GITHUB_API_URL", "https://api.github.com")
	ghc := &gh.Client{
		Token:      token,
		APIURL:     api,
		GraphQLURL: env("GITHUB_GRAPHQL_URL", strings.TrimRight(api, "/")+"/graphql"),
		Owner:      owner,
		Repo:       name,
		HTTP:       &http.Client{Timeout: 60 * time.Second},
	}
	timeout, err := time.ParseDuration(env("LLM_TIMEOUT", "10m"))
	if err != nil {
		logger.Fatalf("LLM_TIMEOUT: %v", err)
	}
	providers, err := loadProviders()
	if err != nil {
		logger.Fatal(err)
	}
	lc := &llm.Client{Providers: providers, HTTP: llm.NewHTTPClient(timeout), Logf: logger.Printf}

	ignore := append([]string{}, review.DefaultIgnore...)
	ignore = append(ignore, splitList(os.Getenv("REVIEW_IGNORE"))...)
	cfg := review.Config{
		PR:            *prNum,
		Checks:        splitList(env("REVIEW_CHECKS", "bugs,security")),
		MinSeverity:   env("REVIEW_MIN_SEVERITY", "low"),
		MinConfidence: envFloat("REVIEW_MIN_CONFIDENCE", 0.7),
		Verify:        envBool("REVIEW_VERIFY", true),
		Ignore:        ignore,
		MaxFiles:      envInt("REVIEW_MAX_FILES", 40),
		MaxPatchLines: envInt("REVIEW_MAX_PATCH_LINES", 800),
		FullFileLines: envInt("REVIEW_FULL_FILE_LINES", 500),
		MaxComments:   envInt("REVIEW_MAX_COMMENTS", 15),
		FailOn:        strings.ToLower(env("REVIEW_FAIL_ON", "")),
		ReviewDrafts:  envBool("REVIEW_DRAFTS", false),
		BotLogins:     splitList(env("REVIEW_BOT_LOGIN", strings.Join(review.DefaultBotLogins, ","))),
		Force:         *force,
		DryRun:        *dry,
		Logger:        logger,
	}
	for _, c := range cfg.Checks {
		if c != "bugs" && c != "security" {
			logger.Fatalf("REVIEW_CHECKS: unknown check %q (use bugs, security)", c)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for i, p := range providers {
		extra := ""
		if p.API == "ollama" {
			extra = fmt.Sprintf(" num_ctx=%d", p.NumCtx)
			if p.Think != nil {
				extra += fmt.Sprintf(" think=%t", *p.Think)
			}
		}
		logger.Printf("provider %d: %s model=%s%s", i+1, p.Name, p.Model, extra)
	}
	if err := lc.Preflight(ctx); err != nil {
		logger.Fatal(err)
	}
	logger.Printf("%s/%s#%d checks=%v", owner, name, cfg.PR, cfg.Checks)
	r := &review.Reviewer{GH: ghc, LLM: lc, Cfg: cfg}
	res, err := r.Run(ctx)
	if err != nil {
		logger.Fatal(err)
	}
	logger.Printf("done: %d new, %d resolved, open=%v", res.Posted, res.Resolved, res.Open)
	writeStepSummary(res)
	if res.Failed {
		logger.Printf("failing: open issues at or above %q", cfg.FailOn)
		os.Exit(1)
	}
}

// loadProviders reads LLM_PROVIDERS (a JSON list, tried in order) or, if
// that is unset, the single-provider LLM_* variables. LLM_BASE_URL may hold
// several comma-separated URLs, which are tried in order with the same model.
func loadProviders() ([]llm.Provider, error) {
	think := envOptBool("LLM_THINK")
	if js := strings.TrimSpace(os.Getenv("LLM_PROVIDERS")); js != "" {
		ps, err := llm.ParseProviders(js, os.Getenv)
		if err != nil {
			return nil, err
		}
		// LLM_THINK applies to Ollama providers that don't set "think" themselves.
		for i := range ps {
			if ps[i].API == "ollama" && ps[i].Think == nil {
				ps[i].Think = think
			}
			if ps[i].MaxTokens == 0 {
				ps[i].MaxTokens = envInt("LLM_MAX_TOKENS", 0)
			}
		}
		return ps, nil
	}
	api := env("LLM_API", "ollama")
	urls := splitList(os.Getenv("LLM_BASE_URL"))
	if len(urls) == 0 {
		urls = []string{""} // preset default
	}
	model := env("LLM_MODEL", "")
	if model == "" && (api == "ollama" || os.Getenv("LLM_API") == "") {
		model = "qwen3-coder:30b"
	}
	var out []llm.Provider
	for _, u := range urls {
		p := llm.Provider{API: api, BaseURL: u, Model: model, APIKey: os.Getenv("LLM_API_KEY"), NumCtx: envInt("LLM_NUM_CTX", 0), Think: think, MaxTokens: envInt("LLM_MAX_TOKENS", 0)}
		if t := os.Getenv("LLM_TEMPERATURE"); t != "" {
			f := envFloat("LLM_TEMPERATURE", 0.1)
			p.Temperature = &f
		}
		r, err := p.Resolve(os.Getenv)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func prFromEvent(p string) (int, error) {
	if p == "" {
		return 0, fmt.Errorf("GITHUB_EVENT_PATH not set")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return 0, err
	}
	var ev struct {
		Number      int `json:"number"`
		PullRequest struct {
			Number int `json:"number"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return 0, err
	}
	if ev.PullRequest.Number != 0 {
		return ev.PullRequest.Number, nil
	}
	if ev.Number != 0 {
		return ev.Number, nil
	}
	return 0, fmt.Errorf("event has no pull_request")
}

func writeStepSummary(res *review.Result) {
	p := os.Getenv("GITHUB_STEP_SUMMARY")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "### review-bot\n\n%d new, %d resolved on `%.7s`.\n", res.Posted, res.Resolved, res.HeadSHA)
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(env(k, "")); err == nil {
		return n
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if f, err := strconv.ParseFloat(env(k, ""), 64); err == nil {
		return f
	}
	return def
}

func envBool(k string, def bool) bool {
	if b, err := strconv.ParseBool(env(k, "")); err == nil {
		return b
	}
	return def
}

// envOptBool returns nil when k is unset or not a boolean.
func envOptBool(k string) *bool {
	b, err := strconv.ParseBool(env(k, ""))
	if err != nil {
		return nil
	}
	return &b
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
