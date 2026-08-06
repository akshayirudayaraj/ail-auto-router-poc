package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akshayirudayaraj/ail-routing-test/internal/eval"
	"github.com/akshayirudayaraj/ail-routing-test/internal/extract"
	"github.com/akshayirudayaraj/ail-routing-test/internal/feature"
	"github.com/akshayirudayaraj/ail-routing-test/internal/router"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

func osReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// ---- /api/traces ----

// traceTurn is the UI view of one turn.
type traceTurn struct {
	TurnIndex   int      `json:"turn_index"`
	Role        string   `json:"role"`
	Content     string   `json:"content"`
	ServedModel string   `json:"served_model,omitempty"`
	Propensity  *float64 `json:"propensity,omitempty"`
	// mined (assistant turns): what the extractor inferred
	MinedOutcome    *int    `json:"mined_outcome,omitempty"`
	MinedConfidence float64 `json:"mined_confidence,omitempty"`
	MinedSignal     string  `json:"mined_signal,omitempty"`
	// planted truth (grader-only; shown for inspection, clearly labeled)
	TruthAdequate   *bool    `json:"truth_adequate,omitempty"`
	TruthDifficulty *float64 `json:"truth_difficulty,omitempty"`
	TruthSignal     string   `json:"truth_signal,omitempty"`
}

type traceSession struct {
	SessionID string      `json:"session_id"`
	NumTurns  int         `json:"num_turns"`
	Task      string      `json:"task"` // first prompt, truncated
	Turns     []traceTurn `json:"turns"`
}

func (s *Server) handleTraces(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.cfg.DataDir, "raw_logs.jsonl")
	// load WITH hidden truth for display; mining reads only non-hidden fields.
	turns, err := extract.LoadRaw(path, false)
	if err != nil {
		writeJSON(w, 200, map[string]any{"sessions": []any{}, "error": "no raw logs — run `make gen`"})
		return
	}
	sessions := extract.Reconstruct(turns)

	// mine implicit labels per assistant observation
	isFrontier := func(m string) bool {
		return m == s.cfg.FrontierModel || (len(m) >= 6 && m[:6] == "claude")
	}
	obs := extract.Observations(sessions)
	mined := map[string]extract.ImplicitLabel{}
	for _, o := range obs {
		mined[o.PromptID] = extract.InferSignal(o, isFrontier)
	}

	var out []traceSession
	for _, sess := range sessions {
		ts := traceSession{SessionID: sess.ID, NumTurns: len(sess.Turns)}
		for _, t := range sess.Turns {
			tt := traceTurn{
				TurnIndex: t.TurnIndex, Role: string(t.Role), Content: t.Content,
				ServedModel: t.ServedModel, Propensity: t.Propensity,
			}
			if t.Role == schema.RoleAssistant {
				pid := sess.ID + "-t" + pad2(t.TurnIndex)
				if lab, ok := mined[pid]; ok {
					o := lab.Outcome
					tt.MinedOutcome = &o
					tt.MinedConfidence = lab.Confidence
					tt.MinedSignal = string(lab.Signal)
				}
				tt.TruthAdequate = t.TrueAdequate
				tt.TruthDifficulty = t.TrueDifficulty
			} else {
				if ts.Task == "" {
					ts.Task = truncate(t.Content, 90)
				}
				tt.TruthSignal = t.TrueSignal
			}
			ts.Turns = append(ts.Turns, tt)
		}
		out = append(out, ts)
	}
	writeJSON(w, 200, map[string]any{"sessions": out})
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ---- /api/pointwise, /api/pairwise, /api/gold ----

func (s *Server) handlePointwise(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	rows := s.pointwise
	s.mu.RUnlock()
	src := r.URL.Query().Get("source")
	var out []map[string]any
	for _, row := range rows {
		if src != "" && string(row.LabelSource) != src {
			continue
		}
		out = append(out, map[string]any{
			"prompt_id":  row.PromptID,
			"prompt":     truncate(row.PromptText, 120),
			"model":      row.Model,
			"outcome":    row.Outcome,
			"source":     row.LabelSource,
			"confidence": round3(row.LabelConfidence),
			"turn_type":  row.Features.TurnType,
			"hard_kw":    round3(row.Features.HardKeywordScore),
			"tokens":     row.Features.PromptTokensApprox,
			"session_id": row.SessionID,
			"has_embed":  len(row.Embedding) > 0,
			"propensity": row.Propensity,
		})
	}
	writeJSON(w, 200, map[string]any{"rows": out, "total": len(out)})
}

func (s *Server) handlePairwise(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	rows := s.pairwise
	s.mu.RUnlock()
	var out []map[string]any
	for _, row := range rows {
		out = append(out, map[string]any{
			"prompt_id": row.PromptID,
			"prompt":    truncate(row.PromptText, 100),
			"model_a":   row.ModelA,
			"model_b":   row.ModelB,
			"preferred": row.Preferred,
			"source":    row.Source,
		})
	}
	writeJSON(w, 200, map[string]any{"rows": out, "total": len(out)})
}

func (s *Server) handleGold(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	rows := s.gold
	s.mu.RUnlock()
	var out []map[string]any
	for _, row := range rows {
		out = append(out, map[string]any{
			"prompt_id":        row.PromptID,
			"prompt":           truncate(row.PromptText, 110),
			"outcome_local":    row.OutcomeLocal,
			"outcome_frontier": row.OutcomeFrontier,
			"cost_local":       round3(row.CostLocal),
			"cost_frontier":    round3(row.CostFrontier),
			"cell":             goldCell(row),
			"executable":       row.Executable,
		})
	}
	writeJSON(w, 200, map[string]any{"rows": out, "total": len(out)})
}

// goldCell labels the escalation-relevant cell for a gold row.
func goldCell(row schema.GoldRow) string {
	switch {
	case row.OutcomeLocal == 0 && row.OutcomeFrontier == 1:
		return "frontier-rescues" // escalation pays off
	case row.OutcomeLocal == 1 && row.OutcomeFrontier == 1:
		return "both-pass"
	case row.OutcomeLocal == 1 && row.OutcomeFrontier == 0:
		return "local-only"
	default:
		return "both-fail" // no headroom
	}
}

// ---- /api/reports ----

// handleReports serves the saved JSON reports (extractor, train, eval methods).
func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	load := func(key, name string) {
		var v any
		if s.readJSON(filepath.Join(s.cfg.DataDir, name), &v) == nil {
			out[key] = v
		}
	}
	load("extractor", "extractor_report.json")
	load("train", "train_summary.json")
	load("gold", "eval_dual-arm-gold.json")
	load("backtest", "eval_temporal-backtest.json")
	load("offpolicy", "eval_off-policy-ips-dr.json")
	load("guardrail", "eval_guardrail-suite.json")
	writeJSON(w, 200, out)
}

// ---- /api/fit ----

type fitRequest struct {
	Router      string  `json:"router"`       // "" => fit ALL routers; else just this one
	TrainSource string  `json:"train_source"` // "" / "all" => no source filter
	Threshold   float64 `json:"threshold"`
}

// normSource maps the UI's "all" (or empty) to the no-filter sentinel "".
func normSource(s string) schema.LabelSource {
	if s == "" || s == "all" {
		return ""
	}
	return schema.LabelSource(s)
}

// srcLabel renders a source for display ("" => "all").
func srcLabel(src schema.LabelSource) string {
	if src == "" {
		return "all"
	}
	return string(src)
}

func (s *Server) handleFit(w http.ResponseWriter, r *http.Request) {
	var req fitRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	src := normSource(req.TrainSource)
	thr := req.Threshold
	if thr <= 0 {
		thr = 0.5
	}
	// Per-router fit: train just the named router on its own source.
	if req.Router != "" {
		res, err := s.fitOne(req.Router, src)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, res)
		return
	}
	// Fit ALL routers on one source (the "Fit all routers" button).
	if err := s.fit(src, thr); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, s.fitReport(thr))
}

// fit refits router.Registry() on the chosen source and stores them for routing.
func (s *Server) fit(src schema.LabelSource, thr float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pointwise) == 0 {
		return errNoData
	}
	routers := router.Registry()
	td := router.TrainData{
		Pointwise: s.pointwise, Pairwise: s.pairwise,
		LocalModels: s.cfg.LocalModels, FrontierModel: s.cfg.FrontierModel,
		TrainSource: src,
	}
	for _, rt := range routers {
		if err := rt.Fit(td); err != nil {
			return err
		}
	}
	s.routers = routers
	s.fitSource = src
	s.fitThresh = thr
	return nil
}

// fitOne trains a single router in place on its own label source, leaving the
// others as they are. Returns that router's training breakdown (+ IRT abilities
// when relevant) so the Training tab can show exactly what it consumed.
func (s *Server) fitOne(name string, src schema.LabelSource) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pointwise) == 0 {
		return nil, errNoData
	}
	var target router.Router
	for _, rt := range s.routers {
		if rt.Name() == name {
			target = rt
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("unknown router %q", name)
	}
	td := router.TrainData{
		Pointwise: s.pointwise, Pairwise: s.pairwise,
		LocalModels: s.cfg.LocalModels, FrontierModel: s.cfg.FrontierModel,
		TrainSource: src,
	}
	if err := target.Fit(td); err != nil {
		return nil, err
	}
	out := map[string]any{
		"router":       name,
		"train_source": srcLabel(src),
		"trained_on":   s.trainedOn(name, src),
	}
	if irt, ok := target.(*router.IRT); ok {
		out["abilities"] = s.abilityRows(irt.Abilities())
	}
	return out, nil
}

func (s *Server) isFrontierModel(m string) bool {
	return m == s.cfg.FrontierModel || strings.HasPrefix(m, "claude")
}

// filterPointwise returns pointwise rows for one source ("" => all).
func (s *Server) filterPointwise(src schema.LabelSource) []schema.PointwiseRow {
	if src == "" {
		return s.pointwise
	}
	out := s.pointwise[:0:0]
	for _, r := range s.pointwise {
		if r.LabelSource == src {
			out = append(out, r)
		}
	}
	return out
}

// trainedOn reports the shape + row count a router actually consumes at `src`.
// This mirrors each router's Fit-time data selection (router pkg) for display.
func (s *Server) trainedOn(name string, src schema.LabelSource) map[string]any {
	pw := s.filterPointwise(src)
	switch name {
	case "irt-1pl":
		return map[string]any{"shape": "pointwise", "count": len(pw)}
	case "knn":
		n := 0
		for _, r := range pw {
			if !s.isFrontierModel(r.Model) && len(r.Embedding) > 0 {
				n++
			}
		}
		return map[string]any{"shape": "embedded pointwise", "count": n}
	case "routellm-logistic":
		pairs := 0
		for _, p := range s.pairwise {
			if src != "" && p.Source != src {
				continue
			}
			if aF, bF := s.isFrontierModel(p.ModelA), s.isFrontierModel(p.ModelB); aF != bF && p.Preferred != "tie" {
				pairs++
			}
		}
		pseudo := 0
		for _, r := range pw {
			if !s.isFrontierModel(r.Model) {
				pseudo++
			}
		}
		return map[string]any{"shape": "pairwise + pointwise pseudo-pairs", "pairwise": pairs, "pseudo": pseudo, "count": pairs + pseudo}
	default:
		return map[string]any{"shape": "none", "count": 0}
	}
}

// dataSummary is the corpus-level training-data breakdown the tab shows above
// the router cards, and the per-shape source lists that scope each selector.
// Caller must hold at least the read lock.
func (s *Server) dataSummary() map[string]any {
	pwBySrc := map[string]int{}
	emb := 0
	for _, r := range s.pointwise {
		pwBySrc[string(r.LabelSource)]++
		if len(r.Embedding) > 0 {
			emb++
		}
	}
	prBySrc := map[string]int{}
	for _, p := range s.pairwise {
		prBySrc[string(p.Source)]++
	}
	return map[string]any{
		"pointwise": map[string]any{"total": len(s.pointwise), "by_source": pwBySrc},
		"pairwise":  map[string]any{"total": len(s.pairwise), "by_source": prBySrc},
		"embedded":  emb,
	}
}

// abilityRows renders the IRT ability map as reference-centered rows.
func (s *Server) abilityRows(abilities map[string]float64) []map[string]any {
	var out []map[string]any
	for _, m := range s.cfg.AllModels() {
		out = append(out, map[string]any{"model": m, "recovered": round3(abilities[m])})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i]["recovered"].(float64) < out[j]["recovered"].(float64)
	})
	return out
}

// fitReport builds the IRT-recovery + gold-leaderboard payload.
func (s *Server) fitReport(thr float64) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// IRT abilities from the fitted registry
	abilities := map[string]float64{}
	for _, rt := range s.routers {
		if irt, ok := rt.(*router.IRT); ok {
			abilities = irt.Abilities()
		}
	}
	var planted map[string]float64
	_ = s.readJSON(filepath.Join(s.cfg.DataDir, "_truth.json"), &struct {
		Abilities *map[string]float64 `json:"abilities"`
	}{Abilities: &planted})

	// gold leaderboard (only if gold present)
	var leaderboard []eval.ReportRow
	var anchors []eval.Baseline
	if len(s.gold) > 0 {
		data := eval.Data{Cfg: s.cfg, Pointwise: s.pointwise, Pairwise: s.pairwise, Gold: s.gold}
		ge := &eval.GoldEval{Threshold: thr, TrainSource: s.fitSource}
		if rep, err := ge.Run(router.Registry(), data); err == nil {
			leaderboard = rep.Rows
			if det, ok := rep.Detail.(eval.GoldReportDetail); ok {
				anchors = det.Anchors
			}
		}
	}

	// build ability rows (reference-centered planted vs recovered)
	type abRow struct {
		Model     string   `json:"model"`
		Planted   *float64 `json:"planted,omitempty"`
		Recovered float64  `json:"recovered"`
	}
	var abRows []abRow
	ref := s.cfg.LocalModels[0]
	shift := 0.0
	if planted != nil {
		shift = planted[ref]
	}
	for _, m := range s.cfg.AllModels() {
		row := abRow{Model: m, Recovered: round3(abilities[m])}
		if planted != nil {
			p := round3(planted[m] - shift)
			row.Planted = &p
		}
		abRows = append(abRows, row)
	}
	sort.Slice(abRows, func(i, j int) bool { return abRows[i].Recovered < abRows[j].Recovered })

	training := map[string]any{}
	for _, rt := range s.routers {
		training[rt.Name()] = s.trainedOn(rt.Name(), s.fitSource)
	}

	return map[string]any{
		"train_source": srcLabel(s.fitSource),
		"threshold":    thr,
		"n_pointwise":  len(s.pointwise),
		"n_pairwise":   len(s.pairwise),
		"abilities":    abRows,
		"leaderboard":  leaderboard,
		"anchors":      anchors,
		"has_gold":     len(s.gold) > 0,
		"n_gold":       len(s.gold),
		"data_summary": s.dataSummary(),
		"training":     training,
	}
}

// ---- /api/eval ----

type evalRequest struct {
	TrainSource string  `json:"train_source"` // "" => current fit source
	Threshold   float64 `json:"threshold"`    // <=0 => current threshold
}

// handleEval runs the dual-arm gold benchmark on demand and returns the
// leaderboard + method notes. Explicit control (per-router fits don't refresh
// the leaderboard) and a hook for the fuller harness later.
func (s *Server) handleEval(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, 200, map[string]any{
		"method":       rep.Method,
		"train_source": srcLabel(src),
		"threshold":    thr,
		"n_gold":       len(gold),
		"leaderboard":  rep.Rows,
		"anchors":      anchors,
		"notes":        rep.Notes,
	})
}

// ---- /api/route ----

type routeRequest struct {
	Prompt    string  `json:"prompt"`
	TurnType  string  `json:"turn_type"`
	Threshold float64 `json:"threshold"`
}

func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	var req routeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prompt == "" {
		writeJSON(w, 400, map[string]any{"error": "provide a non-empty prompt"})
		return
	}
	turnType := req.TurnType
	if turnType == "" {
		turnType = "open"
	}
	thr := req.Threshold
	if thr <= 0 {
		thr = s.fitThresh
	}

	s.mu.RLock()
	routers := s.routers
	s.mu.RUnlock()
	if len(routers) == 0 {
		if err := s.fit(schema.LabelImplicit, 0.5); err != nil {
			writeJSON(w, 400, map[string]any{"error": "no fitted routers and no data to fit: " + err.Error()})
			return
		}
		s.mu.RLock()
		routers = s.routers
		s.mu.RUnlock()
	}

	feats := feature.Extract(req.Prompt, turnType)
	inst := router.Instance{Features: feats}
	embedErr := ""
	if emb, err := s.be.Embed(r.Context(), req.Prompt); err == nil {
		inst.Embedding = emb
	} else {
		embedErr = err.Error()
	}

	type rr struct {
		Name     string  `json:"name"`
		Score    float64 `json:"score"`
		Escalate bool    `json:"escalate"`
	}
	var results []rr
	escVotes := 0
	for _, rt := range routers {
		sc := rt.Score(inst)
		dec := rt.Decide(inst, thr)
		if dec {
			escVotes++
		}
		results = append(results, rr{Name: rt.Name(), Score: round3(sc), Escalate: dec})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })

	// feature vector (named) for transparency
	names := feature.VectorNames()
	vec := feature.Vector(feats)
	featOut := make([]map[string]any, len(names))
	for i := range names {
		featOut[i] = map[string]any{"name": names[i], "value": round3(vec[i])}
	}

	writeJSON(w, 200, map[string]any{
		"threshold":      thr,
		"turn_type":      turnType,
		"embedding_dim":  len(inst.Embedding),
		"embed_error":    embedErr,
		"features":       featOut,
		"routers":        results,
		"escalate_votes": escVotes,
		"total_routers":  len(results),
		"local_model":    s.cfg.LocalModels[0],
		"frontier_model": s.cfg.FrontierModel,
	})
}

func round3(f float64) float64 {
	return float64(int(f*1000+sign(f)*0.5)) / 1000
}
func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}
