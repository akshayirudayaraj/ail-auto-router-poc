package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/akshayirudayaraj/ail-routing-test/internal/eval"
	"github.com/akshayirudayaraj/ail-routing-test/internal/router"
	"github.com/akshayirudayaraj/ail-routing-test/internal/store"
)

// gitSHA is a best-effort short commit for provenance on a persisted eval run;
// empty when not in a git checkout.
func gitSHA() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// handleEvals is the persisted eval-run collection (distinct from /api/eval,
// which runs a benchmark but does not store it). GET lists the run history
// newest-first; POST runs the dual-arm gold benchmark and PERSISTS the result
// through the Store seam — the hook that lets the backing store move file →
// Postgres without changing the API.
func (s *Server) handleEvals(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		runs, err := s.store.ListEvalRuns()
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"runs": runs})
	case http.MethodPost:
		s.postEvalRun(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, 405, map[string]any{"error": "use GET (list history) or POST (run + store)"})
	}
}

// postEvalRun runs the gold benchmark (same as handleEval) then stores the run.
func (s *Server) postEvalRun(w http.ResponseWriter, r *http.Request) {
	var req evalRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.mu.RLock()
	pw, pr, gold, curSrc, curThr := s.pointwise, s.pairwise, s.gold, s.fitSource, s.fitThresh
	s.mu.RUnlock()
	if len(gold) == 0 {
		writeJSON(w, 400, map[string]any{"error": "no dual-arm gold set — run grading + `make agentic-materialize`"})
		return
	}
	src := curSrc
	if req.TrainSource != "" {
		src = normSource(req.TrainSource)
	}
	thr := curThr
	if req.Threshold > 0 {
		thr = req.Threshold
	}
	data := eval.Data{Cfg: s.cfg, Pointwise: pw, Pairwise: pr, Gold: gold}
	ge := &eval.GoldEval{Threshold: thr, TrainSource: src}
	rep, err := ge.Run(router.Registry(), data)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	var anchors []eval.Baseline
	if det, ok := rep.Detail.(eval.GoldReportDetail); ok {
		anchors = det.Anchors
	}
	payload, _ := json.Marshal(map[string]any{
		"leaderboard": rep.Rows,
		"anchors":     anchors,
		"notes":       rep.Notes,
	})
	// Dataset version: hash the gold rows + train-set sizes so the run records
	// exactly what it scored (reproducibility).
	gb, _ := json.Marshal(gold)
	dsHash := store.HashDataset(gb, []byte(fmt.Sprintf("pw=%d;pr=%d", len(pw), len(pr))))
	now := time.Now().UTC()
	run := store.EvalRun{
		ID:          now.Format("20060102T150405Z") + "-" + dsHash[:6],
		CreatedAt:   now.Format(time.RFC3339),
		GitSHA:      gitSHA(),
		DatasetHash: dsHash,
		Method:      rep.Method,
		TrainSource: srcLabel(src),
		Threshold:   thr,
		NGold:       len(gold),
		Payload:     payload,
	}
	if err := s.store.SaveEvalRun(run); err != nil {
		writeJSON(w, 500, map[string]any{"error": "eval ran but failed to persist: " + err.Error()})
		return
	}
	writeJSON(w, 201, run)
}
