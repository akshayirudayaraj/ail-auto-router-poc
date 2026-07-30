package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// Embed returns an embedding for text via the local Ollama embeddings endpoint
// (POST {OLLAMA_URL}/api/embeddings, the well-known contract). Cached by
// (embed model, text). Embeddings are local and cheap but still capped.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	key := cacheKey("embed", c.cfg.EmbedModel, text)
	var cached []float32
	if c.cache.getJSON("embed", key, &cached) && len(cached) > 0 {
		atomic.AddInt64(&c.embedHits, 1)
		return cached, nil
	}

	if atomic.LoadInt64(&c.embedCalls) >= int64(c.cfg.MaxEmbedCalls) {
		return nil, fmt.Errorf("embed: %w (max %d)", ErrCapExceeded, c.cfg.MaxEmbedCalls)
	}

	var out []float32
	err := retry(ctx, 3, 300*time.Millisecond, func() error {
		c.acquire()
		defer c.release()
		v, err := c.ollamaEmbed(ctx, text)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	atomic.AddInt64(&c.embedCalls, 1)
	c.cache.putJSON("embed", key, out)
	return out, nil
}

type ollamaEmbedReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}
type ollamaEmbedResp struct {
	Embedding []float32 `json:"embedding"`
}

func (c *Client) ollamaEmbed(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(ollamaEmbedReq{Model: c.cfg.EmbedModel, Prompt: text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.OllamaURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embed: status %d", resp.StatusCode)
	}
	var r ollamaEmbedResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("ollama embed decode: %w", err)
	}
	if len(r.Embedding) == 0 {
		return nil, fmt.Errorf("ollama embed: empty embedding (is %q pulled?)", c.cfg.EmbedModel)
	}
	return r.Embedding, nil
}

type ollamaChatReq struct {
	Model    string        `json:"model"`
	Messages []Message     `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  ollamaOptions `json:"options"`
}

// ollamaOptions bounds local generation so a single hard prompt can't make the
// model ramble for minutes (keeps the overnight run snappy and responses
// log-realistic). num_predict caps output tokens.
type ollamaOptions struct {
	NumPredict  int     `json:"num_predict"`
	Temperature float64 `json:"temperature"`
}
type ollamaChatResp struct {
	Message Message `json:"message"`
}

// ollamaGenerate runs a local chat completion (non-streaming).
func (c *Client) ollamaGenerate(ctx context.Context, model string, msgs []Message) (string, error) {
	body, _ := json.Marshal(ollamaChatReq{
		Model: model, Messages: msgs, Stream: false,
		Options: ollamaOptions{NumPredict: 512, Temperature: 0.2},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.OllamaURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama chat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama chat: status %d", resp.StatusCode)
	}
	var r ollamaChatResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("ollama chat decode: %w", err)
	}
	return r.Message.Content, nil
}
