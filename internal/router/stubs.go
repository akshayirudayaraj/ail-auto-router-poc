package router

import (
	"bufio"
	"encoding/json"
	"os"

	"github.com/akshayirudayaraj/ail-routing-test/internal/numerics"
)

// This file holds the two routers whose REAL training is non-portable (needs a
// Python deep-learning stack): a fine-tuned neural encoder + MLP scorer, and a
// small-LM (SLM) router head. The Go side exposes the Router interface and a
// working baseline stub; the actual training lives in python/ and writes a
// small artifact the Go stub loads if present.
//
// See README "Go / Python portability boundary" and python/README.md.

// EncoderMLP is the Go-side handle for the encoder+MLP scorer. Without a
// trained artifact it behaves as a transparent baseline (a light linear combo
// of structural features) so the eval harness can always run it. If
// python/artifacts/encoder_mlp.json exists it loads those linear weights over
// the embedding (a stand-in for the exported MLP head).
type EncoderMLP struct {
	// optional loaded head: score = sigmoid(w·embedding + b)
	w   []float64
	b   float64
	got bool
}

func NewEncoderMLP() *EncoderMLP {
	e := &EncoderMLP{}
	e.tryLoad("python/artifacts/encoder_mlp.json")
	return e
}

func (e *EncoderMLP) Name() string {
	if e.got {
		return "encoder-mlp"
	}
	return "encoder-mlp(stub)"
}

// Fit is a no-op: the encoder/MLP is trained offline in python/. Retraining
// from Go is intentionally not supported (that is the non-portable boundary).
func (e *EncoderMLP) Fit(TrainData) error { return nil }

func (e *EncoderMLP) Score(inst Instance) float64 {
	if e.got && len(inst.Embedding) > 0 {
		z := e.b
		for i := 0; i < len(e.w) && i < len(inst.Embedding); i++ {
			z += e.w[i] * float64(inst.Embedding[i])
		}
		return numerics.Sigmoid(z)
	}
	// baseline: difficulty prior + attached-context pressure
	s := 0.6*inst.Features.HardKeywordScore + 0.4*normContext(inst.Features.AttachedCtxTokens)
	return clamp01(s)
}

func (e *EncoderMLP) Decide(inst Instance, threshold float64) bool {
	return decideAt(e.Score(inst), threshold)
}

type headArtifact struct {
	W []float64 `json:"w"`
	B float64   `json:"b"`
}

func (e *EncoderMLP) tryLoad(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	var a headArtifact
	if json.NewDecoder(bufio.NewReader(f)).Decode(&a) == nil && len(a.W) > 0 {
		e.w, e.b, e.got = a.W, a.B, true
	}
}

// SLMHead is the Go-side handle for the small-LM router head. Same pattern:
// a stub baseline unless python/artifacts/slm_head.json is present.
type SLMHead struct {
	w   []float64
	b   float64
	got bool
}

func NewSLMHead() *SLMHead {
	s := &SLMHead{}
	s.tryLoad("python/artifacts/slm_head.json")
	return s
}

func (s *SLMHead) Name() string {
	if s.got {
		return "slm-head"
	}
	return "slm-head(stub)"
}

func (s *SLMHead) Fit(TrainData) error { return nil }

func (s *SLMHead) Score(inst Instance) float64 {
	if s.got && len(inst.Embedding) > 0 {
		z := s.b
		for i := 0; i < len(s.w) && i < len(inst.Embedding); i++ {
			z += s.w[i] * float64(inst.Embedding[i])
		}
		return numerics.Sigmoid(z)
	}
	// baseline: a slightly different prior emphasising prompt length + keywords
	s2 := 0.5*inst.Features.HardKeywordScore +
		0.3*normLen(inst.Features.PromptTokensApprox) +
		0.2*normContext(inst.Features.AttachedCtxTokens)
	return clamp01(s2)
}

func (s *SLMHead) Decide(inst Instance, threshold float64) bool {
	return decideAt(s.Score(inst), threshold)
}

func (s *SLMHead) tryLoad(path string) {
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	var a headArtifact
	if json.NewDecoder(bufio.NewReader(f)).Decode(&a) == nil && len(a.W) > 0 {
		s.w, s.b, s.got = a.W, a.B, true
	}
}

func normContext(tok int) float64 { return clamp01(float64(tok) / 500.0) }
func normLen(tok int) float64     { return clamp01(float64(tok) / 300.0) }
func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

var _ Router = (*EncoderMLP)(nil)
var _ Router = (*SLMHead)(nil)
