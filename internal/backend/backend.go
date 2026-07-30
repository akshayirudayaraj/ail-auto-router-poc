// Package backend is the pluggable model-access layer. It exposes three
// capabilities — Embed, Generate, Judge — each wrapped with content-hash disk
// caching, bounded global concurrency, retry-with-backoff, and hard spend
// caps. Anthropic access auto-detects the `claude` CLI (subscription,
// preferred) vs direct HTTP with an API key.
//
// The interface is what the rest of the framework depends on; the concrete
// *Client wires the real backends. Tests use a fake implementing Backend.
package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"` // "user" | "assistant" | "system"
	Content string `json:"content"`
}

// JudgeResult is the frontier-as-judge verdict on a (prompt, response) pair.
type JudgeResult struct {
	Adequate  bool    `json:"adequate"`
	Score     float64 `json:"score"` // [0,1] confidence/quality
	Rationale string  `json:"rationale"`
}

// Backend is the capability surface the framework depends on.
type Backend interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Generate(ctx context.Context, model string, msgs []Message) (string, error)
	Judge(ctx context.Context, prompt, response string) (JudgeResult, error)
	Stats() Stats
}

// Stats is a snapshot of call counts (for spend accounting and logging).
type Stats struct {
	EmbedCalls, EmbedHits             int64
	GenLocalCalls, GenLocalHits       int64
	GenFrontierCalls, GenFrontierHits int64
	JudgeCalls, JudgeHits             int64
}

// ErrCapExceeded is returned when a hard spend cap would be crossed.
var ErrCapExceeded = errors.New("backend: spend cap exceeded")

// anthropicMode records which auth path is active.
type anthropicMode int

const (
	anthropicNone anthropicMode = iota
	anthropicCLI                // `claude` subprocess (subscription; preferred)
	anthropicHTTP               // direct API with ANTHROPIC_API_KEY
)

func (m anthropicMode) String() string {
	switch m {
	case anthropicCLI:
		return "claude-CLI (subscription)"
	case anthropicHTTP:
		return "HTTP (ANTHROPIC_API_KEY)"
	default:
		return "unavailable"
	}
}

// Client is the concrete Backend.
type Client struct {
	cfg   config.Config
	cache *diskCache
	sem   chan struct{} // bounded concurrency for real (non-cached) calls
	log   *log.Logger

	anthropic anthropicMode

	// counters (atomic)
	embedCalls, embedHits             int64
	genLocalCalls, genLocalHits       int64
	genFrontierCalls, genFrontierHits int64
	judgeCalls, judgeHits             int64

	once sync.Once
}

// New constructs a Client, auto-detecting the Anthropic auth path and logging
// which capabilities are live.
func New(cfg config.Config, logger *log.Logger) *Client {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	c := &Client{
		cfg:   cfg,
		cache: newDiskCache(cfg.CacheDir),
		sem:   make(chan struct{}, cfg.Concurrency),
		log:   logger,
	}
	c.anthropic = detectAnthropic(cfg)
	logger.Printf("backend: anthropic=%s embed=%s local=%v frontier=%s concurrency=%d",
		c.anthropic, cfg.EmbedModel, cfg.LocalModels, cfg.FrontierModel, cfg.Concurrency)
	return c
}

func detectAnthropic(cfg config.Config) anthropicMode {
	if _, err := exec.LookPath("claude"); err == nil {
		return anthropicCLI
	}
	if cfg.AnthropicKey != "" {
		return anthropicHTTP
	}
	return anthropicNone
}

// AnthropicAvailable reports whether frontier generation / judging can run.
func (c *Client) AnthropicAvailable() bool { return c.anthropic != anthropicNone }

// isAnthropicModel decides which backend serves a given model name.
func (c *Client) isAnthropicModel(model string) bool {
	if model == c.cfg.FrontierModel || model == c.cfg.JudgeModel {
		return true
	}
	return len(model) >= 6 && model[:6] == "claude"
}

// acquire/release bound global concurrency for real network calls.
func (c *Client) acquire() { c.sem <- struct{}{} }
func (c *Client) release() { <-c.sem }

// Stats returns a counter snapshot.
func (c *Client) Stats() Stats {
	return Stats{
		EmbedCalls:       atomic.LoadInt64(&c.embedCalls),
		EmbedHits:        atomic.LoadInt64(&c.embedHits),
		GenLocalCalls:    atomic.LoadInt64(&c.genLocalCalls),
		GenLocalHits:     atomic.LoadInt64(&c.genLocalHits),
		GenFrontierCalls: atomic.LoadInt64(&c.genFrontierCalls),
		GenFrontierHits:  atomic.LoadInt64(&c.genFrontierHits),
		JudgeCalls:       atomic.LoadInt64(&c.judgeCalls),
		JudgeHits:        atomic.LoadInt64(&c.judgeHits),
	}
}

// LogStats prints a spend summary.
func (c *Client) LogStats() {
	s := c.Stats()
	c.log.Printf("backend spend: embed=%d(+%d cached) local_gen=%d(+%d) frontier_gen=%d(+%d) judge=%d(+%d)",
		s.EmbedCalls, s.EmbedHits, s.GenLocalCalls, s.GenLocalHits,
		s.GenFrontierCalls, s.GenFrontierHits, s.JudgeCalls, s.JudgeHits)
}

// retry runs fn up to attempts times with exponential backoff. ctx cancellation
// aborts early. Cap-exceeded errors are not retried.
func retry(ctx context.Context, attempts int, base time.Duration, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if errors.Is(err, ErrCapExceeded) {
			return err
		}
		if i == attempts-1 {
			break
		}
		d := base * (1 << i)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	return fmt.Errorf("after %d attempts: %w", attempts, err)
}
