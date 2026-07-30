package extract

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// ClassMetrics scores a binary labeler where the POSITIVE class is "inadequate"
// (outcome 0) — i.e. how well the labeler catches answers that needed
// escalation. Accuracy is overall agreement with truth.
type ClassMetrics struct {
	N         int     `json:"n"`
	Accuracy  float64 `json:"accuracy"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
	TP        int     `json:"tp"`
	FP        int     `json:"fp"`
	FN        int     `json:"fn"`
	TN        int     `json:"tn"`
}

// SignalStat is per-signal-type precision for the implicit labeler.
type SignalStat struct {
	Signal    string  `json:"signal"`
	N         int     `json:"n"`
	Correct   int     `json:"correct"`
	Precision float64 `json:"precision"`
}

// Report is the Pillar 1c extractor-quality report.
type Report struct {
	NumAssistantTurns int          `json:"num_assistant_turns"`
	Implicit          ClassMetrics `json:"implicit"`
	Judge             ClassMetrics `json:"judge"`
	PerSignal         []SignalStat `json:"per_signal"`
	Note              string       `json:"note"`
}

// BuildReport grades extraction against the hidden ground truth. It reads the
// ORIGINAL (un-stripped) logs for truth, and re-derives implicit predictions
// from a stripped load — so grading never contaminates extraction. Judge
// predictions are read from the produced pointwise dataset.
func BuildReport(cfg config.Config) (Report, error) {
	var rep Report
	logPath := filepath.Join(cfg.DataDir, "raw_logs.jsonl")

	// truth: promptID -> adequate (from unstripped assistant turns)
	rawTruth, err := LoadRaw(logPath, false)
	if err != nil {
		return rep, err
	}
	truth := map[string]bool{}
	for _, s := range Reconstruct(rawTruth) {
		for _, t := range s.Turns {
			if t.Role == schema.RoleAssistant && t.TrueAdequate != nil {
				truth[fmt.Sprintf("%s-t%02d", s.ID, t.TurnIndex)] = *t.TrueAdequate
			}
		}
	}
	rep.NumAssistantTurns = len(truth)

	// implicit predictions: re-derive from STRIPPED logs
	stripped, err := LoadRaw(logPath, true)
	if err != nil {
		return rep, err
	}
	obs := Observations(Reconstruct(stripped))
	isFrontier := frontierPredicate(cfg)

	perSig := map[string]*SignalStat{}
	var impTP, impFP, impFN, impTN int
	for _, o := range obs {
		tr, ok := truth[o.PromptID]
		if !ok {
			continue
		}
		lab := InferSignal(o, isFrontier)
		predInadequate := lab.Outcome == 0
		truthInadequate := !tr
		switch {
		case truthInadequate && predInadequate:
			impTP++
		case !truthInadequate && predInadequate:
			impFP++
		case truthInadequate && !predInadequate:
			impFN++
		default:
			impTN++
		}
		// per-signal precision: was this signal's implied outcome correct?
		ss := perSig[string(lab.Signal)]
		if ss == nil {
			ss = &SignalStat{Signal: string(lab.Signal)}
			perSig[string(lab.Signal)] = ss
		}
		ss.N++
		if (lab.Outcome == 1) == tr {
			ss.Correct++
		}
	}
	rep.Implicit = metrics(impTP, impFP, impFN, impTN)

	for _, ss := range perSig {
		if ss.N > 0 {
			ss.Precision = float64(ss.Correct) / float64(ss.N)
		}
		rep.PerSignal = append(rep.PerSignal, *ss)
	}
	sort.Slice(rep.PerSignal, func(i, j int) bool { return rep.PerSignal[i].Signal < rep.PerSignal[j].Signal })

	// judge predictions: read judge-sourced rows from the pointwise dataset
	judgeRows, err := loadJudgeOutcomes(filepath.Join(cfg.DataDir, "pointwise.jsonl"))
	if err == nil {
		var jTP, jFP, jFN, jTN int
		for id, out := range judgeRows {
			tr, ok := truth[id]
			if !ok {
				continue
			}
			predInadequate := out == 0
			truthInadequate := !tr
			switch {
			case truthInadequate && predInadequate:
				jTP++
			case !truthInadequate && predInadequate:
				jFP++
			case truthInadequate && !predInadequate:
				jFN++
			default:
				jTN++
			}
		}
		rep.Judge = metrics(jTP, jFP, jFN, jTN)
	}

	rep.Note = "Implicit signals are NOISY FEATURES anchored by the judge, not clean labels. " +
		"Positive class = 'inadequate' (needed escalation). CAVEAT: on SYNTHETIC data the judge " +
		"row grades templated stub responses; a strict judge correctly flags a planted-'adequate' " +
		"stub as inadequate, so judge-vs-planted-truth here mostly measures template realism, not " +
		"judge quality. The judge path is validated for wiring; on REAL responses judge-vs-truth is " +
		"the meaningful anchor. The implicit metric is meaningful either way (it grades signal mining)."
	return rep, nil
}

func metrics(tp, fp, fn, tn int) ClassMetrics {
	m := ClassMetrics{TP: tp, FP: fp, FN: fn, TN: tn, N: tp + fp + fn + tn}
	if m.N > 0 {
		m.Accuracy = float64(tp+tn) / float64(m.N)
	}
	if tp+fp > 0 {
		m.Precision = float64(tp) / float64(tp+fp)
	}
	if tp+fn > 0 {
		m.Recall = float64(tp) / float64(tp+fn)
	}
	if m.Precision+m.Recall > 0 {
		m.F1 = 2 * m.Precision * m.Recall / (m.Precision + m.Recall)
	}
	return m
}

// loadJudgeOutcomes returns promptID -> outcome for judge-sourced rows.
func loadJudgeOutcomes(path string) (map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		var r schema.PointwiseRow
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.LabelSource == schema.LabelJudge {
			out[r.PromptID] = r.Outcome
		}
	}
	return out, sc.Err()
}

// Markdown renders the report as a human-readable table.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Extractor quality (Pillar 1c)\n\n")
	fmt.Fprintf(&b, "Graded against planted ground truth over %d assistant turns. ", r.NumAssistantTurns)
	fmt.Fprintf(&b, "Positive class = *inadequate* (the answer that needed escalation).\n\n")
	fmt.Fprintf(&b, "| Labeler | N | Accuracy | Precision | Recall | F1 |\n")
	fmt.Fprintf(&b, "|---|--:|--:|--:|--:|--:|\n")
	fmt.Fprintf(&b, "| implicit heuristics | %d | %.3f | %.3f | %.3f | %.3f |\n",
		r.Implicit.N, r.Implicit.Accuracy, r.Implicit.Precision, r.Implicit.Recall, r.Implicit.F1)
	fmt.Fprintf(&b, "| frontier judge (sample) | %d | %.3f | %.3f | %.3f | %.3f |\n",
		r.Judge.N, r.Judge.Accuracy, r.Judge.Precision, r.Judge.Recall, r.Judge.F1)
	fmt.Fprintf(&b, "\n### Per-signal precision (implicit)\n\n")
	fmt.Fprintf(&b, "| signal | n | correct | precision |\n|---|--:|--:|--:|\n")
	for _, s := range r.PerSignal {
		fmt.Fprintf(&b, "| %s | %d | %d | %.3f |\n", s.Signal, s.N, s.Correct, s.Precision)
	}
	fmt.Fprintf(&b, "\n> %s\n", r.Note)
	return b.String()
}

// Save writes the report as JSON and Markdown into DataDir.
func (r Report) Save(cfg config.Config) error {
	jb, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "extractor_report.json"), jb, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg.DataDir, "extractor_report.md"), []byte(r.Markdown()), 0o644)
}
