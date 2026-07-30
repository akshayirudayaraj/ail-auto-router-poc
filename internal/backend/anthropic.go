package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// Generate dispatches to the local (Ollama) or frontier (Anthropic) backend
// based on the model name. Cached by (model, messages). Local and frontier
// have independent counters and caps.
func (c *Client) Generate(ctx context.Context, model string, msgs []Message) (string, error) {
	msgJSON, _ := json.Marshal(msgs)
	key := cacheKey("gen", model, string(msgJSON))
	frontier := c.isAnthropicModel(model)

	var cached string
	if c.cache.getJSON("gen", key, &cached) && cached != "" {
		if frontier {
			atomic.AddInt64(&c.genFrontierHits, 1)
		} else {
			atomic.AddInt64(&c.genLocalHits, 1)
		}
		return cached, nil
	}

	if frontier {
		if !c.AnthropicAvailable() {
			return "", fmt.Errorf("frontier generate: anthropic backend unavailable")
		}
		if atomic.LoadInt64(&c.genFrontierCalls) >= int64(c.cfg.MaxFrontierCalls) {
			return "", fmt.Errorf("frontier generate: %w (max %d)", ErrCapExceeded, c.cfg.MaxFrontierCalls)
		}
	}

	var out string
	err := retry(ctx, 3, 500*time.Millisecond, func() error {
		c.acquire()
		defer c.release()
		var v string
		var e error
		if frontier {
			v, e = c.anthropicGenerate(ctx, model, msgs)
		} else {
			v, e = c.ollamaGenerate(ctx, model, msgs)
		}
		if e != nil {
			return e
		}
		out = v
		return nil
	})
	if err != nil {
		return "", err
	}
	if frontier {
		atomic.AddInt64(&c.genFrontierCalls, 1)
	} else {
		atomic.AddInt64(&c.genLocalCalls, 1)
	}
	c.cache.putJSON("gen", key, out)
	return out, nil
}

// Judge asks the frontier model whether response is ADEQUATE for prompt,
// returning a structured verdict. Cached by (judge model, prompt, response).
func (c *Client) Judge(ctx context.Context, prompt, response string) (JudgeResult, error) {
	key := cacheKey("judge", c.cfg.JudgeModel, prompt, response)
	var cached JudgeResult
	if c.cache.getJSON("judge", key, &cached) {
		atomic.AddInt64(&c.judgeHits, 1)
		return cached, nil
	}
	if !c.AnthropicAvailable() {
		return JudgeResult{}, fmt.Errorf("judge: anthropic backend unavailable")
	}
	if atomic.LoadInt64(&c.judgeCalls) >= int64(c.cfg.MaxJudgeCalls) {
		return JudgeResult{}, fmt.Errorf("judge: %w (max %d)", ErrCapExceeded, c.cfg.MaxJudgeCalls)
	}

	judgePrompt := buildJudgePrompt(prompt, response)
	var out JudgeResult
	err := retry(ctx, 3, 500*time.Millisecond, func() error {
		c.acquire()
		defer c.release()
		raw, e := c.anthropicGenerate(ctx, c.cfg.JudgeModel, []Message{{Role: "user", Content: judgePrompt}})
		if e != nil {
			return e
		}
		jr, e := parseJudge(raw)
		if e != nil {
			return e
		}
		out = jr
		return nil
	})
	if err != nil {
		return JudgeResult{}, err
	}
	atomic.AddInt64(&c.judgeCalls, 1)
	c.cache.putJSON("judge", key, out)
	return out, nil
}

func buildJudgePrompt(prompt, response string) string {
	return "You are grading whether an AI assistant's response is ADEQUATE for a " +
		"software-engineering request: correct, complete enough to act on, and not " +
		"misleading. Be strict but fair.\n\n" +
		"REQUEST:\n" + truncate(prompt, 4000) + "\n\n" +
		"RESPONSE:\n" + truncate(response, 4000) + "\n\n" +
		"Reply with ONLY a JSON object, no prose, of the form:\n" +
		`{"adequate": true|false, "score": 0.0-1.0, "rationale": "one sentence"}`
}

// ---- Anthropic transport: CLI (preferred) or HTTP ----

func (c *Client) anthropicGenerate(ctx context.Context, model string, msgs []Message) (string, error) {
	switch c.anthropic {
	case anthropicCLI:
		return c.claudeCLI(ctx, model, msgs)
	case anthropicHTTP:
		return c.claudeHTTP(ctx, model, msgs)
	default:
		return "", fmt.Errorf("anthropic backend unavailable")
	}
}

// claudeCLI invokes the logged-in `claude` CLI as a subprocess (uses the
// subscription; no API key). The full environment is inherited so the CLI can
// read its macOS Keychain credential — without USER/LOGNAME/HOME it misreports
// as "Credit balance is too low".
func (c *Client) claudeCLI(ctx context.Context, model string, msgs []Message) (string, error) {
	prompt := flattenMessages(msgs)
	args := []string{"-p", prompt}
	if alias := cliModelAlias(model); alias != "" {
		args = append(args, "--model", alias)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Env = os.Environ() // inherit HOME/PATH/USER/LOGNAME (see DECISIONS D3)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude CLI: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return "", fmt.Errorf("claude CLI: empty output: %s", strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// cliModelAlias maps a full model id to a stable CLI alias (sonnet/opus/haiku).
// The CLI resolves aliases to the latest of that tier.
func cliModelAlias(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "opus"):
		return "opus"
	case strings.Contains(m, "haiku"):
		return "haiku"
	case strings.Contains(m, "sonnet"):
		return "sonnet"
	default:
		return "" // let the CLI use its default
	}
}

func flattenMessages(msgs []Message) string {
	if len(msgs) == 1 && msgs[0].Role == "user" {
		return msgs[0].Content
	}
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(strings.ToUpper(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

type anthropicHTTPReq struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []Message `json:"messages"`
}
type anthropicHTTPResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) claudeHTTP(ctx context.Context, model string, msgs []Message) (string, error) {
	// Only user/assistant messages go in the array for the HTTP API.
	body, _ := json.Marshal(anthropicHTTPReq{Model: model, MaxTokens: 1024, Messages: msgs})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.cfg.AnthropicKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic http: %w", err)
	}
	defer resp.Body.Close()
	var r anthropicHTTPResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("anthropic http decode: %w", err)
	}
	if r.Error != nil {
		return "", fmt.Errorf("anthropic http: %s", r.Error.Message)
	}
	var b strings.Builder
	for _, part := range r.Content {
		if part.Type == "text" {
			b.WriteString(part.Text)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("anthropic http: empty response (status %d)", resp.StatusCode)
	}
	return out, nil
}

// parseJudge extracts the JSON verdict from a model reply, tolerating
// surrounding prose or code fences.
func parseJudge(raw string) (JudgeResult, error) {
	s := extractJSONObject(raw)
	if s == "" {
		return JudgeResult{}, fmt.Errorf("judge: no JSON object in reply: %q", truncate(raw, 200))
	}
	var jr JudgeResult
	if err := json.Unmarshal([]byte(s), &jr); err != nil {
		return JudgeResult{}, fmt.Errorf("judge: bad JSON: %w", err)
	}
	// clamp score
	if jr.Score < 0 {
		jr.Score = 0
	}
	if jr.Score > 1 {
		jr.Score = 1
	}
	return jr, nil
}

// extractJSONObject returns the first balanced {...} substring, or "".
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
