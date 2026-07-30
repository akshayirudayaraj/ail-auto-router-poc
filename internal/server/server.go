// Package server is a stdlib-only web UI over the framework: it serves an
// embedded single-page app plus a JSON API to (1) browse raw log traces with
// their mined labels and planted truth, (2) inspect the structured datasets,
// (3) fit the candidate routers and see IRT recovery + the gold leaderboard,
// and (4) route a brand-new prompt through every fitted router live.
//
// It reads the same data files the CLI stages produce and calls into the
// router/eval/extract packages directly — no extra dependencies.
package server

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
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
)

var errNoData = errors.New("no dataset loaded — run `make extract` first")

//go:embed static/*
var staticFS embed.FS

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
}

// New builds a server and loads whatever datasets are present.
func New(cfg config.Config, be *backend.Client, lg *log.Logger) *Server {
	s := &Server{cfg: cfg, be: be, lg: lg, fitSource: schema.LabelImplicit, fitThresh: 0.5}
	s.loadData()
	// fit on startup so /api/route works immediately (best-effort)
	_ = s.fit(schema.LabelImplicit, 0.5)
	return s
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
	mux.HandleFunc("/api/route", s.handleRoute)

	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
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
