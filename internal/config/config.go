// Package config centralizes every tunable knob: dataset sizes, model names,
// backend endpoints, concurrency, spend caps and the RNG seed. Everything is
// seedable so runs are reproducible.
//
// Configuration is loaded from (in increasing precedence): built-in defaults,
// an optional key=value file, then environment variables. This keeps the
// portable core dependency-free (no YAML/TOML library).
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the whole framework's configuration.
type Config struct {
	// Reproducibility.
	Seed int64

	// Dataset sizes (small by default so an overnight run completes cheaply).
	NumSessions   int     // raw sessions to generate
	NumGoldRows   int     // dual-arm gold benchmark size
	JudgeSample   int     // max (prompt,response) pairs sent to the frontier judge
	EpsilonGreedy float64 // logging-policy exploration prob (enables off-policy eval)

	// Models. LocalModels is the cheap open-weight rung; FrontierModel is the
	// escalation rung. EmbedModel serves embeddings.
	LocalModels   []string
	FrontierModel string
	JudgeModel    string
	EmbedModel    string

	// Backends.
	OllamaURL    string
	AnthropicKey string // optional; empty => prefer claude CLI subprocess
	Concurrency  int

	// Spend/rate caps (hard stops; the backend refuses calls past these).
	MaxFrontierCalls int
	MaxJudgeCalls    int
	MaxEmbedCalls    int

	// Paths.
	DataDir  string
	CacheDir string
}

// Default returns the small, cheap, overnight-friendly configuration.
func Default() Config {
	return Config{
		Seed:          42,
		NumSessions:   60, // ~150+ prompts once multi-turn is expanded
		NumGoldRows:   40,
		JudgeSample:   40,
		EpsilonGreedy: 0.2,

		LocalModels:   []string{"llama3.1:8b", "qwen2.5-coder:14b"},
		FrontierModel: "claude-sonnet-5",
		JudgeModel:    "claude-sonnet-5",
		EmbedModel:    "nomic-embed-text",

		OllamaURL:    "http://localhost:11434",
		AnthropicKey: "",
		Concurrency:  4,

		MaxFrontierCalls: 200,
		MaxJudgeCalls:    200,
		MaxEmbedCalls:    5000,

		DataDir:  "data",
		CacheDir: "cache",
	}
}

// Load builds a Config from Default(), overlays an optional key=value file
// (path from env AIL_CONFIG, ignored if unset/missing), then overlays
// environment variables. Env keys mirror struct fields, upper-snake-cased and
// prefixed AIL_ (e.g. AIL_NUM_SESSIONS). OLLAMA_URL and ANTHROPIC_API_KEY are
// also honored under their conventional names.
func Load() (Config, error) {
	c := Default()

	if path := os.Getenv("AIL_CONFIG"); path != "" {
		if err := c.applyFile(path); err != nil {
			return c, err
		}
	}
	c.applyEnv()
	return c, c.Validate()
}

func (c *Config) applyFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("config file: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		c.set(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	return sc.Err()
}

func (c *Config) applyEnv() {
	// Conventional names first.
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		c.OllamaURL = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		c.AnthropicKey = v
	}
	// AIL_ prefixed knobs.
	for _, key := range []string{
		"AIL_SEED", "AIL_NUM_SESSIONS", "AIL_NUM_GOLD_ROWS", "AIL_JUDGE_SAMPLE",
		"AIL_EPSILON_GREEDY", "AIL_LOCAL_MODELS", "AIL_FRONTIER_MODEL",
		"AIL_JUDGE_MODEL", "AIL_EMBED_MODEL", "AIL_OLLAMA_URL", "AIL_CONCURRENCY",
		"AIL_MAX_FRONTIER_CALLS", "AIL_MAX_JUDGE_CALLS", "AIL_MAX_EMBED_CALLS",
		"AIL_DATA_DIR", "AIL_CACHE_DIR",
	} {
		if v, ok := os.LookupEnv(key); ok {
			c.set(strings.TrimPrefix(key, "AIL_"), v)
		}
	}
}

func (c *Config) set(key, val string) {
	switch strings.ToUpper(key) {
	case "SEED":
		c.Seed = atoi64(val, c.Seed)
	case "NUM_SESSIONS":
		c.NumSessions = atoi(val, c.NumSessions)
	case "NUM_GOLD_ROWS":
		c.NumGoldRows = atoi(val, c.NumGoldRows)
	case "JUDGE_SAMPLE":
		c.JudgeSample = atoi(val, c.JudgeSample)
	case "EPSILON_GREEDY":
		c.EpsilonGreedy = atof(val, c.EpsilonGreedy)
	case "LOCAL_MODELS":
		c.LocalModels = splitList(val)
	case "FRONTIER_MODEL":
		c.FrontierModel = val
	case "JUDGE_MODEL":
		c.JudgeModel = val
	case "EMBED_MODEL":
		c.EmbedModel = val
	case "OLLAMA_URL":
		c.OllamaURL = val
	case "CONCURRENCY":
		c.Concurrency = atoi(val, c.Concurrency)
	case "MAX_FRONTIER_CALLS":
		c.MaxFrontierCalls = atoi(val, c.MaxFrontierCalls)
	case "MAX_JUDGE_CALLS":
		c.MaxJudgeCalls = atoi(val, c.MaxJudgeCalls)
	case "MAX_EMBED_CALLS":
		c.MaxEmbedCalls = atoi(val, c.MaxEmbedCalls)
	case "DATA_DIR":
		c.DataDir = val
	case "CACHE_DIR":
		c.CacheDir = val
	}
}

// Validate checks invariants that would otherwise fail deep in a run.
func (c *Config) Validate() error {
	if len(c.LocalModels) == 0 {
		return fmt.Errorf("config: at least one local model required")
	}
	if c.FrontierModel == "" {
		return fmt.Errorf("config: frontier model required")
	}
	if c.Concurrency < 1 {
		return fmt.Errorf("config: concurrency must be >= 1")
	}
	if c.EpsilonGreedy < 0 || c.EpsilonGreedy > 1 {
		return fmt.Errorf("config: epsilon_greedy must be in [0,1]")
	}
	return nil
}

// AllModels returns local models followed by the frontier model.
func (c Config) AllModels() []string {
	return append(append([]string{}, c.LocalModels...), c.FrontierModel)
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}
func atoi64(s string, def int64) int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
		return n
	}
	return def
}
func atof(s string, def float64) float64 {
	if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return f
	}
	return def
}
func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
