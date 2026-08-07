// Package server is a stdlib-only JSON API over the framework: it exposes
// endpoints to (1) browse raw log traces with their mined labels and planted
// truth, (2) inspect the structured datasets, (3) fit the candidate routers and
// see IRT recovery + the gold leaderboard, and (4) route a brand-new prompt
// through every fitted router live.
//
// It is API-only: the React/Vite console is a separate frontend that consumes
// these endpoints (dev: `make console-dev` on :5173, proxying /api here; prod:
// `vite build` + any static host). The Go binary no longer embeds the UI, so
// the two deploy independently.
//
// It reads the same data files the CLI stages produce and calls into the
// router/eval/extract packages directly — no extra dependencies.
package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/akshayirudayaraj/ail-routing-test/internal/backend"
	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/dataio"
	"github.com/akshayirudayaraj/ail-routing-test/internal/gold"
	"github.com/akshayirudayaraj/ail-routing-test/internal/router"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
	"github.com/akshayirudayaraj/ail-routing-test/internal/store"
)

var errNoData = errors.New("no dataset loaded — run `make extract` first")

// Server holds loaded data, the live backend, and the currently fitted routers.
type Server struct {
	cfg config.Config
	be  *backend.Client
	lg  *log.Logger

	mu        sync.RWMutex
	pointwise []schema.PointwiseRow
	pairwise  []schema.PairwiseRow
	gold      []schema.GoldRow
	goldMeta  gold.Meta

	routers   []router.Router // fitted, for /api/route
	fitSource schema.LabelSource
	fitThresh float64

	store store.Store // persistence seam for eval runs (file today, DB later)
}

// New builds a server and loads whatever datasets are present.
func New(cfg config.Config, be *backend.Client, lg *log.Logger) *Server {
	s := &Server{cfg: cfg, be: be, lg: lg, fitSource: schema.LabelImplicit, fitThresh: 0.5,
		store: store.NewFileStore(cfg.DataDir)}
	s.loadData()
	// fit on startup so /api/route works immediately (best-effort). Pick the
	// source the loaded data actually carries — synthetic corpora are implicit,
	// the agentic corpus is executed — so routers aren't left unfit on an empty
	// source filter.
	_ = s.fit(s.dominantSource(), 0.5)
	return s
}

// dominantSource returns the label source most represented in the loaded
// pointwise rows (defaulting to implicit when there is nothing to count).
func (s *Server) dominantSource() schema.LabelSource {
	counts := map[schema.LabelSource]int{}
	for _, r := range s.pointwise {
		counts[r.LabelSource]++
	}
	best, bestN := schema.LabelImplicit, 0
	for src, n := range counts {
		if n > bestN {
			best, bestN = src, n
		}
	}
	return best
}

func (s *Server) loadData() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pw, err := dataio.LoadPointwise(s.cfg); err == nil {
		s.pointwise = pw
	}
	if pr, err := dataio.LoadPairwise(s.cfg); err == nil {
		s.pairwise = pr
	}
	if g, err := dataio.LoadGold(s.cfg); err == nil {
		s.gold = g
	}
	_ = s.readJSON(filepath.Join(s.cfg.DataDir, "gold_meta.json"), &s.goldMeta)
}

// Handler returns the http.Handler serving the UI and API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/summary", s.handleSummary)
	mux.HandleFunc("/api/traces", s.handleTraces)
	mux.HandleFunc("/api/pointwise", s.handlePointwise)
	mux.HandleFunc("/api/pairwise", s.handlePairwise)
	mux.HandleFunc("/api/gold", s.handleGold)
	mux.HandleFunc("/api/reports", s.handleReports)
	mux.HandleFunc("/api/fit", s.handleFit)
	mux.HandleFunc("/api/eval", s.handleEval)
	mux.HandleFunc("/api/evals", s.handleEvals)
	mux.HandleFunc("/api/route", s.handleRoute)
	mux.HandleFunc("/api/routers", s.handleRouters)
	mux.HandleFunc("/api/agentic/session", s.handleAgenticSession)
	mux.HandleFunc("/api/agentic", s.handleAgentic)
	mux.HandleFunc("/api/labels", s.handleLabels)

	// API-only: the UI lives in the separate Vite frontend. A plain root reply
	// so a stray browser hit here isn't a bare 404.
	mux.HandleFunc("/", s.handleRoot)
	return mux
}

// handleRoot answers non-API requests with a short pointer to the frontend
// (this server no longer serves the console bundle).
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, 200, map[string]any{
		"service": "ail-routing-test API",
		"ui":      "run `make console-dev` and open http://localhost:5173 (proxies /api here)",
		"api":     []string{"/api/summary", "/api/agentic", "/api/pointwise", "/api/pairwise", "/api/gold", "/api/fit", "/api/eval", "/api/evals", "/api/route", "/api/routers", "/api/labels"},
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) readJSON(path string, v any) error {
	b, err := osReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ---- /api/summary ----

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var implicit, judge int
	for _, row := range s.pointwise {
		if row.LabelSource == schema.LabelJudge {
			judge++
		} else {
			implicit++
		}
	}
	writeJSON(w, 200, map[string]any{
		"local_models":   s.cfg.LocalModels,
		"frontier_model": s.cfg.FrontierModel,
		"embed_model":    s.cfg.EmbedModel,
		"anthropic":      s.be.AnthropicAvailable(),
		"seed":           s.cfg.Seed,
		"counts": map[string]int{
			"pointwise_implicit": implicit,
			"pointwise_judge":    judge,
			"pairwise":           len(s.pairwise),
			"gold":               len(s.gold),
		},
		"gold_meta":  s.goldMeta,
		"fit_source": s.fitSource,
		"fit_thresh": s.fitThresh,
		"has_data":   len(s.pointwise) > 0,
	})
}
