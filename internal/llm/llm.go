// Package llm is a chat client with an ordered list of model providers. It
// uses the first provider that answers and fails over to the next one when a
// provider is unreachable or erroring.
//
// Supported APIs:
//   - "ollama": Ollama's native API (lets us set the context window)
//   - "openai": any OpenAI-compatible chat completions API (OpenAI, vLLM,
//     llama.cpp, LM Studio, ...)
//   - "anthropic": the Anthropic Messages API
//
// Presets fill in the base URL and API-key variable for common hosted
// providers that speak the OpenAI API: gemini, openrouter, groq, mistral,
// deepseek, xai.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Message is a chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Provider is one model endpoint.
type Provider struct {
	Name        string   `json:"name,omitempty"` // label for logs; defaults to api@host
	API         string   `json:"api"`            // ollama, openai, anthropic, or a preset name
	BaseURL     string   `json:"base_url,omitempty"`
	Model       string   `json:"model"`
	APIKey      string   `json:"api_key,omitempty"`     // prefer api_key_env
	APIKeyEnv   string   `json:"api_key_env,omitempty"` // env var holding the key
	NumCtx      int      `json:"num_ctx,omitempty"`     // ollama only
	Temperature *float64 `json:"temperature,omitempty"` // default 0.1 for ollama, provider default otherwise
	MaxTokens   int      `json:"max_tokens,omitempty"`  // anthropic: default 8192
	JSONMode    *bool    `json:"json_mode,omitempty"`   // openai: send response_format (default true)
}

type preset struct{ api, baseURL, keyEnv string }

var presets = map[string]preset{
	"ollama":     {"ollama", "http://localhost:11434", ""},
	"openai":     {"openai", "https://api.openai.com/v1", "OPENAI_API_KEY"},
	"anthropic":  {"anthropic", "https://api.anthropic.com", "ANTHROPIC_API_KEY"},
	"gemini":     {"openai", "https://generativelanguage.googleapis.com/v1beta/openai", "GEMINI_API_KEY"},
	"openrouter": {"openai", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"},
	"groq":       {"openai", "https://api.groq.com/openai/v1", "GROQ_API_KEY"},
	"mistral":    {"openai", "https://api.mistral.ai/v1", "MISTRAL_API_KEY"},
	"deepseek":   {"openai", "https://api.deepseek.com/v1", "DEEPSEEK_API_KEY"},
	"xai":        {"openai", "https://api.x.ai/v1", "XAI_API_KEY"},
}

// Resolve applies preset defaults and reads the API key from the
// environment. It does not fail on a missing key: a provider without one is
// skipped at run time so the rest of the chain still works.
func (p Provider) Resolve(getenv func(string) string) (Provider, error) {
	kind := strings.ToLower(strings.TrimSpace(p.API))
	if kind == "" {
		kind = "ollama"
	}
	pr, ok := presets[kind]
	if !ok {
		return p, fmt.Errorf("unknown api %q (use ollama, openai, anthropic, gemini, openrouter, groq, mistral, deepseek or xai)", p.API)
	}
	customURL := p.BaseURL != ""
	p.API = pr.api
	if !customURL {
		p.BaseURL = pr.baseURL
	}
	p.BaseURL = strings.TrimRight(p.BaseURL, "/")
	if p.APIKeyEnv == "" && !(customURL && kind == "openai") {
		// A custom OpenAI-compatible server (vLLM etc.) usually has no key.
		p.APIKeyEnv = pr.keyEnv
	}
	if p.APIKey == "" && p.APIKeyEnv != "" {
		p.APIKey = strings.TrimSpace(getenv(p.APIKeyEnv))
	}
	if p.Model == "" {
		return p, fmt.Errorf("provider %s: model is required", p.label(kind))
	}
	if p.Name == "" {
		p.Name = p.label(kind)
	}
	if p.API == "ollama" && p.NumCtx == 0 {
		p.NumCtx = 16384
	}
	if p.API == "ollama" && p.Temperature == nil {
		t := 0.1
		p.Temperature = &t
	}
	return p, nil
}

func (p Provider) label(kind string) string {
	host := p.BaseURL
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return kind
	}
	return kind + "@" + host
}

func (p Provider) needsKey() bool {
	return p.APIKeyEnv != "" || p.API == "anthropic"
}

// ParseProviders reads a JSON array of providers (the LLM_PROVIDERS format).
func ParseProviders(js string, getenv func(string) string) ([]Provider, error) {
	var raw []Provider
	if err := json.Unmarshal([]byte(js), &raw); err != nil {
		return nil, fmt.Errorf("LLM_PROVIDERS: %w", err)
	}
	if len(raw) == 0 {
		return nil, errors.New("LLM_PROVIDERS: empty list")
	}
	out := make([]Provider, 0, len(raw))
	for _, p := range raw {
		r, err := p.Resolve(getenv)
		if err != nil {
			return nil, fmt.Errorf("LLM_PROVIDERS: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// NewHTTPClient returns an HTTP client that fails fast when a host is down
// (short connect timeout) but allows long generations (overall timeout).
func NewHTTPClient(timeout time.Duration) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	t.TLSHandshakeTimeout = 10 * time.Second
	return &http.Client{Timeout: timeout, Transport: t}
}

// Client chats with the first healthy provider.
type Client struct {
	Providers []Provider
	HTTP      *http.Client
	Logf      func(format string, args ...any)

	mu     sync.Mutex
	active int
	down   map[int]string // index -> reason
	noJSON map[int]bool   // openai providers that rejected response_format
	used   []string
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) markDown(i int, reason string) {
	if c.down == nil {
		c.down = map[int]string{}
	}
	c.down[i] = reason
}

// Current is the model of the provider that will be tried first.
func (c *Client) Current() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := c.active; i < len(c.Providers); i++ {
		if _, isDown := c.down[i]; !isDown {
			return c.Providers[i].Model
		}
	}
	if len(c.Providers) > 0 {
		return c.Providers[c.active].Model
	}
	return ""
}

// Used lists the models that answered at least once, in order.
func (c *Client) Used() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.used...)
}

// Preflight checks providers before the run so an unreachable server costs
// one quick probe instead of a failed review call. Ollama servers are probed
// and must have the model pulled; hosted providers must have an API key.
// It returns an error only if no provider is usable.
func (c *Client) Preflight(ctx context.Context) error {
	usable := 0
	for i, p := range c.Providers {
		var reason string
		switch {
		case p.needsKey() && p.APIKey == "":
			reason = fmt.Sprintf("no API key (set %s)", p.APIKeyEnv)
		case p.API == "ollama":
			reason = c.probeOllama(ctx, p)
		}
		c.mu.Lock()
		if reason != "" {
			c.markDown(i, reason)
		}
		c.mu.Unlock()
		if reason != "" {
			c.logf("provider %s (%s): skipping: %s", p.Name, p.Model, reason)
			continue
		}
		usable++
	}
	if usable == 0 {
		return errors.New("no usable model provider")
	}
	return nil
}

func (c *Client) probeOllama(ctx context.Context, p Provider) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/api/tags", nil)
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return "unreachable: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "unexpected status " + resp.Status
	}
	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "bad /api/tags response"
	}
	want := p.Model
	if !strings.Contains(want, ":") {
		want += ":latest"
	}
	for _, m := range tags.Models {
		if m.Name == want || m.Model == want || m.Name == p.Model {
			return ""
		}
	}
	return fmt.Sprintf("model %q is not pulled on this server", p.Model)
}

// Chat sends messages to the first working provider.
func (c *Client) Chat(ctx context.Context, msgs []Message) (string, error) {
	if len(c.Providers) == 0 {
		return "", errors.New("llm: no providers configured")
	}
	c.mu.Lock()
	start := c.active
	c.mu.Unlock()
	var errs []error
	for i := start; i < len(c.Providers); i++ {
		c.mu.Lock()
		_, isDown := c.down[i]
		c.mu.Unlock()
		if isDown {
			continue
		}
		p := c.Providers[i]
		out, err := c.withRetries(ctx, i, p, msgs)
		if err == nil {
			c.mu.Lock()
			if i != c.active {
				c.logf("now using provider %s (%s)", p.Name, p.Model)
			}
			c.active = i
			if len(c.used) == 0 || c.used[len(c.used)-1] != p.Model {
				c.used = append(c.used, p.Model)
			}
			c.mu.Unlock()
			return out, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
		var re *requestError
		if errors.As(err, &re) {
			// This request failed but the provider works: try the next
			// provider for this call only.
			c.logf("provider %s rejected the request (%v); trying the next provider", p.Name, err)
			continue
		}
		c.logf("provider %s failed (%v); failing over", p.Name, err)
		c.mu.Lock()
		c.markDown(i, err.Error())
		c.mu.Unlock()
	}
	return "", fmt.Errorf("llm: all providers failed: %w", errors.Join(errs...))
}

// requestError is a 4xx other than auth/not-found/rate-limit: a problem with
// this request rather than the provider.
type requestError struct{ msg string }

func (e *requestError) Error() string { return e.msg }

// errRetryNow marks an error that should be retried without a backoff.
var errRetryNow = errors.New("retrying with adjusted request")

func (c *Client) withRetries(ctx context.Context, i int, p Provider, msgs []Message) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 && !errors.Is(lastErr, errRetryNow) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt*3) * time.Second):
			}
		}
		out, retry, err := c.once(ctx, i, p, msgs)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return "", lastErr
}

func (c *Client) once(ctx context.Context, i int, p Provider, msgs []Message) (string, bool, error) {
	var url string
	var body map[string]any
	headers := map[string]string{"Content-Type": "application/json"}
	jsonMode := false
	switch p.API {
	case "anthropic":
		url = p.BaseURL + "/v1/messages"
		var system []string
		var conv []Message
		for _, m := range msgs {
			if m.Role == "system" {
				system = append(system, m.Content)
			} else {
				conv = append(conv, m)
			}
		}
		maxTok := p.MaxTokens
		if maxTok == 0 {
			maxTok = 8192
		}
		body = map[string]any{"model": p.Model, "max_tokens": maxTok, "messages": conv}
		if len(system) > 0 {
			body["system"] = strings.Join(system, "\n\n")
		}
		if p.Temperature != nil {
			body["temperature"] = *p.Temperature
		}
		headers["x-api-key"] = p.APIKey
		headers["anthropic-version"] = "2023-06-01"
	case "openai":
		url = p.BaseURL + "/chat/completions"
		body = map[string]any{"model": p.Model, "messages": msgs}
		if p.Temperature != nil {
			body["temperature"] = *p.Temperature
		}
		c.mu.Lock()
		jsonMode = (p.JSONMode == nil || *p.JSONMode) && !c.noJSON[i]
		c.mu.Unlock()
		if jsonMode {
			body["response_format"] = map[string]string{"type": "json_object"}
		}
		if p.APIKey != "" {
			headers["Authorization"] = "Bearer " + p.APIKey
		}
	default: // ollama
		url = p.BaseURL + "/api/chat"
		opts := map[string]any{}
		if p.NumCtx > 0 {
			opts["num_ctx"] = p.NumCtx
		}
		if p.Temperature != nil {
			opts["temperature"] = *p.Temperature
		}
		body = map[string]any{"model": p.Model, "messages": msgs, "stream": false, "format": "json", "options": opts}
		if p.APIKey != "" {
			headers["Authorization"] = "Bearer " + p.APIKey
		}
	}

	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return "", false, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return "", ctx.Err() == nil && !isConnRefused(err), fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))

	if resp.StatusCode >= 300 {
		msg := fmt.Sprintf("%s: %s", resp.Status, truncate(string(data), 300))
		switch {
		case resp.StatusCode == 400 && jsonMode && strings.Contains(strings.ToLower(string(data)), "response_format"):
			// Some OpenAI-compatible servers don't support JSON mode; the
			// prompts ask for JSON anyway.
			c.mu.Lock()
			if c.noJSON == nil {
				c.noJSON = map[int]bool{}
			}
			c.noJSON[i] = true
			c.mu.Unlock()
			c.logf("provider %s does not support response_format; retrying without it", p.Name)
			return "", true, fmt.Errorf("%w: %s", errRetryNow, msg)
		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			return "", true, errors.New(msg)
		case resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404:
			return "", false, errors.New(msg) // misconfigured: fail over
		default:
			return "", false, &requestError{msg}
		}
	}

	switch p.API {
	case "anthropic":
		var r struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", false, fmt.Errorf("unexpected response: %s", truncate(string(data), 300))
		}
		var b strings.Builder
		for _, part := range r.Content {
			if part.Type == "text" {
				b.WriteString(part.Text)
			}
		}
		return b.String(), false, nil
	case "openai":
		var r struct {
			Choices []struct {
				Message Message `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &r); err != nil || len(r.Choices) == 0 {
			return "", false, fmt.Errorf("unexpected response: %s", truncate(string(data), 300))
		}
		return r.Choices[0].Message.Content, false, nil
	default:
		var r struct {
			Message Message `json:"message"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", false, fmt.Errorf("unexpected response: %s", truncate(string(data), 300))
		}
		return r.Message.Content, false, nil
	}
}

func isConnRefused(err error) bool {
	return strings.Contains(err.Error(), "connection refused")
}

// ChatJSON sends messages and decodes the reply as JSON into out, asking once
// more if the first reply is not valid JSON.
func (c *Client) ChatJSON(ctx context.Context, msgs []Message, out any) error {
	text, err := c.Chat(ctx, msgs)
	if err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(ExtractJSON(text)), out); err == nil {
		return nil
	}
	msgs = append(msgs,
		Message{Role: "assistant", Content: text},
		Message{Role: "user", Content: "That was not valid JSON. Reply with only the JSON object, nothing else."})
	text, err2 := c.Chat(ctx, msgs)
	if err2 != nil {
		return err2
	}
	if err2 = json.Unmarshal([]byte(ExtractJSON(text)), out); err2 != nil {
		return errors.Join(errors.New("llm: invalid JSON reply"), err2)
	}
	return nil
}

var thinkRE = regexp.MustCompile(`(?s)<think>.*?</think>`)

// ExtractJSON pulls the JSON object out of a model reply, tolerating
// reasoning blocks and Markdown fences.
func ExtractJSON(s string) string {
	s = thinkRE.ReplaceAllString(s, "")
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return strings.TrimSpace(s)
	}
	return s[start : end+1]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
