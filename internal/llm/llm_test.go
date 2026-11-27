package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func ollamaServer(t *testing.T, model, reply string, hits *int32) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]string{"name": model, "model": model}}})
		case "/api/chat":
			if hits != nil {
				atomic.AddInt32(hits, 1)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": reply}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func deadURL(t *testing.T) string {
	s := httptest.NewServer(http.NotFoundHandler())
	u := s.URL
	s.Close()
	return u
}

func resolve(t *testing.T, ps ...Provider) []Provider {
	t.Helper()
	env := map[string]string{"ANTHROPIC_API_KEY": "sk-ant", "OPENAI_API_KEY": "sk-oai"}
	var out []Provider
	for _, p := range ps {
		r, err := p.Resolve(func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestFailoverToBackupOllama(t *testing.T) {
	var hits int32
	backup := ollamaServer(t, "small:9b", `{"ok":1}`, &hits)
	c := &Client{Providers: resolve(t,
		Provider{API: "ollama", BaseURL: deadURL(t), Model: "big:30b"},
		Provider{API: "ollama", BaseURL: backup.URL, Model: "small:9b"},
	)}
	ctx := context.Background()
	if err := c.Preflight(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Current() != "small:9b" {
		t.Fatalf("current = %s", c.Current())
	}
	for i := 0; i < 2; i++ {
		var out struct{ OK int }
		if err := c.ChatJSON(ctx, []Message{{Role: "user", Content: "hi"}}, &out); err != nil || out.OK != 1 {
			t.Fatalf("chat: %v %+v", err, out)
		}
	}
	if hits != 2 || strings.Join(c.Used(), ",") != "small:9b" {
		t.Fatalf("hits=%d used=%v", hits, c.Used())
	}
}

func TestFailoverWithoutPreflight(t *testing.T) {
	backup := ollamaServer(t, "m", `{"ok":1}`, nil)
	c := &Client{Providers: resolve(t,
		Provider{API: "ollama", BaseURL: deadURL(t), Model: "m"},
		Provider{API: "ollama", BaseURL: backup.URL, Model: "m"},
	)}
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightModelNotPulled(t *testing.T) {
	s := ollamaServer(t, "other:7b", "{}", nil)
	c := &Client{Providers: resolve(t, Provider{API: "ollama", BaseURL: s.URL, Model: "qwen3-coder:30b"})}
	if err := c.Preflight(context.Background()); err == nil {
		t.Fatal("expected no usable provider")
	}
}

func TestPreflightSkipsMissingKey(t *testing.T) {
	p, err := Provider{API: "gemini", Model: "gemini-pro"}.Resolve(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	backup := ollamaServer(t, "m", "{}", nil)
	c := &Client{Providers: append([]Provider{p}, resolve(t, Provider{API: "ollama", BaseURL: backup.URL, Model: "m"})...)}
	if err := c.Preflight(context.Background()); err != nil || c.Current() != "m" {
		t.Fatalf("err=%v current=%s", err, c.Current())
	}
}

func TestAnthropic(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "sk-ant" || r.Header.Get("anthropic-version") == "" {
			http.Error(w, "bad request", 400)
			return
		}
		var body struct {
			System    string    `json:"system"`
			MaxTokens int       `json:"max_tokens"`
			Messages  []Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.System != "be brief" || body.MaxTokens == 0 || len(body.Messages) != 1 || body.Messages[0].Role != "user" {
			http.Error(w, "bad body", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]string{"type": "text", "text": `{"ok":1}`}}})
	}))
	defer s.Close()
	c := &Client{Providers: resolve(t, Provider{API: "anthropic", BaseURL: s.URL, Model: "claude-x"})}
	out, err := c.Chat(context.Background(), []Message{{Role: "system", Content: "be brief"}, {Role: "user", Content: "hi"}})
	if err != nil || out != `{"ok":1}` {
		t.Fatalf("%q %v", out, err)
	}
}

func TestOpenAIRetriesWithoutJSONMode(t *testing.T) {
	var calls int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "response_format") {
			http.Error(w, `{"error":"response_format is not supported"}`, 400)
			return
		}
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "custom server should get no key", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "{}"}}}})
	}))
	defer s.Close()
	c := &Client{Providers: resolve(t, Provider{API: "openai", BaseURL: s.URL + "/v1", Model: "local"})}
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestRequestErrorTriesNextWithoutMarkingDown(t *testing.T) {
	var first int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&first, 1)
		http.Error(w, "prompt too long", 400)
	}))
	defer bad.Close()
	good := ollamaServer(t, "m", "{}", nil)
	c := &Client{Providers: resolve(t,
		Provider{API: "ollama", BaseURL: bad.URL, Model: "m"},
		Provider{API: "ollama", BaseURL: good.URL, Model: "m"},
	)}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := c.Chat(ctx, []Message{{Role: "user", Content: "hi"}}); err != nil {
			t.Fatal(err)
		}
	}
	// The first provider answered with a 400 and wasn't marked down, but
	// after the second one succeeded it became the active provider.
	if first != 1 {
		t.Fatalf("first provider calls = %d", first)
	}
}

func TestParseProviders(t *testing.T) {
	env := map[string]string{"OPENROUTER_API_KEY": "or-key"}
	ps, err := ParseProviders(`[
	  {"api":"ollama","base_url":"http://a:11434/","model":"qwen3-coder:30b"},
	  {"api":"openrouter","model":"some/model"},
	  {"api":"anthropic","model":"claude-x","name":"cloud"}
	]`, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if ps[0].BaseURL != "http://a:11434" || ps[0].NumCtx != 16384 || ps[0].Name != "ollama@a:11434" {
		t.Errorf("ollama: %+v", ps[0])
	}
	if ps[1].API != "openai" || ps[1].BaseURL != "https://openrouter.ai/api/v1" || ps[1].APIKey != "or-key" {
		t.Errorf("openrouter: %+v", ps[1])
	}
	if ps[2].APIKeyEnv != "ANTHROPIC_API_KEY" || ps[2].APIKey != "" || ps[2].Name != "cloud" {
		t.Errorf("anthropic: %+v", ps[2])
	}
	if _, err := ParseProviders(`[{"api":"openai"}]`, env2); err == nil {
		t.Error("missing model should fail")
	}
	if _, err := ParseProviders(`[{"api":"bogus","model":"x"}]`, env2); err == nil {
		t.Error("unknown api should fail")
	}
}

func env2(string) string { return "" }

func TestExtractJSON(t *testing.T) {
	if got := ExtractJSON("<think>x</think>```json\n{\"a\":1}\n```"); got != `{"a":1}` {
		t.Errorf("got %q", got)
	}
}

func TestOllamaThinkAndStats(t *testing.T) {
	var gotThink any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotThink = body["think"]
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":           map[string]string{"role": "assistant", "content": "{}", "thinking": "hmm"},
			"prompt_eval_count": 2000, "prompt_eval_duration": int64(2e9),
			"eval_count": 100, "eval_duration": int64(4e9),
		})
	}))
	defer s.Close()
	var logs []string
	off := false
	c := &Client{Providers: resolve(t, Provider{API: "ollama", BaseURL: s.URL, Model: "m", Think: &off, NumCtx: 4096}),
		Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if gotThink != false {
		t.Errorf("think = %v", gotThink)
	}
	all := strings.Join(logs, "\n")
	for _, want := range []string{"prompt 2000 tok (1000 tok/s)", "output 100 tok (25 tok/s)", "3 chars of thinking"} {
		if !strings.Contains(all, want) {
			t.Errorf("log missing %q:\n%s", want, all)
		}
	}

	// Unset: no think field, so the model's default applies.
	c2 := &Client{Providers: resolve(t, Provider{API: "ollama", BaseURL: s.URL, Model: "m"})}
	gotThink = "unset"
	_, _ = c2.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if gotThink != nil {
		t.Errorf("think should be omitted, got %v", gotThink)
	}
}

func TestContextOverflowWarnings(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": "{}"}, "prompt_eval_count": 1024})
	}))
	defer s.Close()
	var logs []string
	c := &Client{Providers: resolve(t, Provider{API: "ollama", BaseURL: s.URL, Model: "m", NumCtx: 1024}),
		Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}
	_, _ = c.Chat(context.Background(), []Message{{Role: "user", Content: strings.Repeat("x", 8000)}})
	all := strings.Join(logs, "\n")
	if !strings.Contains(all, "will drop the start") || !strings.Contains(all, "filled") {
		t.Errorf("missing overflow warnings:\n%s", all)
	}
}

func TestOutputLimit(t *testing.T) {
	var gotLimit any
	reply := ""
	done := ""
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Options map[string]any `json:"options"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotLimit = body.Options["num_predict"]
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": reply}, "done_reason": done})
	}))
	defer s.Close()
	off := false
	c := &Client{Providers: resolve(t, Provider{API: "ollama", BaseURL: s.URL, Model: "m", Think: &off})}
	ctx := context.Background()
	msgs := []Message{{Role: "user", Content: "hi"}}

	reply, done = `{"findings":[]}`, "stop"
	if _, err := c.Chat(ctx, msgs); err != nil || gotLimit != float64(2048) {
		t.Fatalf("limit %v, err %v", gotLimit, err)
	}
	// Runaway after a complete answer: keep the answer.
	reply, done = `{"findings":[]}`+strings.Repeat("\n", 50), "length"
	if out, err := c.Chat(ctx, msgs); err != nil || !json.Valid([]byte(ExtractJSON(out))) {
		t.Fatalf("complete JSON should be kept: %q %v", out, err)
	}
	// Cut off mid-answer: fail this call rather than hang or guess.
	reply, done = `{"findings":[{"line":3,"title":"x`, "length"
	if _, err := c.Chat(ctx, msgs); err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("truncated reply should fail: %v", err)
	}
	// Thinking left on (or unknown) gets room to think; an explicit limit wins.
	c2 := &Client{Providers: resolve(t, Provider{API: "ollama", BaseURL: s.URL, Model: "m"})}
	reply, done = `{}`, "stop"
	_, _ = c2.Chat(ctx, msgs)
	if gotLimit != float64(8192) {
		t.Fatalf("default limit with thinking = %v", gotLimit)
	}
	c3 := &Client{Providers: resolve(t, Provider{API: "ollama", BaseURL: s.URL, Model: "m", MaxTokens: 500})}
	_, _ = c3.Chat(ctx, msgs)
	if gotLimit != float64(500) {
		t.Fatalf("explicit limit = %v", gotLimit)
	}
}

func TestSchemaFormatAndFallback(t *testing.T) {
	var formats []any
	var batch any
	rejectSchema := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Format  any            `json:"format"`
			Options map[string]any `json:"options"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		formats = append(formats, body.Format)
		batch = body.Options["num_batch"]
		if _, isObj := body.Format.(map[string]any); isObj && rejectSchema {
			http.Error(w, `{"error":"invalid format: expected \"json\" or a JSON schema"}`, 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": `{"ok":true}`}})
	}))
	defer s.Close()
	schema := map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"}}
	var out struct{ OK bool }
	ctx := context.Background()
	msgs := []Message{{Role: "user", Content: "hi"}}

	c := &Client{Providers: resolve(t, Provider{API: "ollama", BaseURL: s.URL, Model: "m", NumBatch: 1024})}
	if err := c.ChatSchema(ctx, msgs, schema, &out); err != nil || !out.OK {
		t.Fatalf("%v %+v", err, out)
	}
	if f, ok := formats[0].(map[string]any); !ok || f["type"] != "object" {
		t.Fatalf("schema not sent as format: %#v", formats[0])
	}
	if batch != float64(1024) {
		t.Fatalf("num_batch = %v", batch)
	}
	// Plain ChatJSON still sends "json".
	formats = nil
	_ = c.ChatJSON(ctx, msgs, &out)
	if formats[0] != "json" {
		t.Fatalf("format = %#v", formats[0])
	}

	// A server that rejects schemas: retry at once with "json", and remember.
	formats = nil
	rejectSchema = true
	c2 := &Client{Providers: resolve(t, Provider{API: "ollama", BaseURL: s.URL, Model: "m"})}
	for i := 0; i < 2; i++ {
		if err := c2.ChatSchema(ctx, msgs, schema, &out); err != nil {
			t.Fatal(err)
		}
	}
	if len(formats) != 3 || formats[1] != "json" || formats[2] != "json" {
		t.Fatalf("formats = %#v", formats)
	}
}
