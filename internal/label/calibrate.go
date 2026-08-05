package label

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// Calibration measures how trustworthy the weak labelers (judge, heuristics) are,
// by scoring them against EXECUTED truth on the sessions where we have both. This
// is the answer-key comparison the mixed corpus exists to enable
// (OFFLINE_ENGINE_PLAN §6.1): its output sets weak-label confidence and confirms
// the fusion rule on oracle-LESS sessions.
//
// Positive class = INADEQUATE (outcome 0) — the escalation-relevant class a router
// must catch. Precision/recall are reported for detecting inadequacy.

// SourceCalib is one weak source's agreement with executed truth.
type SourceCalib struct {
	Source        schema.LabelSource `json:"source"`
	N             int                `json:"n"` // sessions with BOTH executed and this source
	Accuracy      float64            `json:"accuracy"`
	Precision     float64            `json:"precision"` // of predicting inadequate
	Recall        float64            `json:"recall"`
	F1            float64            `json:"f1"`
	TP            int                `json:"tp"` // said inadequate, truly inadequate
	FP            int                `json:"fp"` // said inadequate, truly adequate
	FN            int                `json:"fn"` // said adequate, truly inadequate
	TN            int                `json:"tn"`
}

// CalibrationReport is the whole picture; consumed by fusion and the UI.
type CalibrationReport struct {
	BySource            map[string]SourceCalib `json:"by_source"`
	JudgeHeurAgreement  float64                `json:"judge_heuristic_agreement"`
	NJudgeHeurPairs     int                    `json:"n_judge_heuristic_pairs"`
	NExecuted           int                    `json:"n_executed"`
	Note                string                 `json:"note,omitempty"`
}

// bySessionOutcomes groups outcomes per (task,model) by source.
type sessionOutcomes struct {
	executed, judge, implicit *int
}

func groupOutcomes(recs []LabelRecord) map[string]*sessionOutcomes {
	m := map[string]*sessionOutcomes{}
	for i := range recs {
		r := recs[i]
		so := m[r.key()]
		if so == nil {
			so = &sessionOutcomes{}
			m[r.key()] = so
		}
		v := r.Outcome
		switch r.LabelSource {
		case schema.LabelExecuted:
			so.executed = &v
		case schema.LabelJudge:
			so.judge = &v
		case schema.LabelImplicit:
			so.implicit = &v
		}
	}
	return m
}

// Calibrate compares each weak source to executed truth. Robust to zero overlap
// (reports N=0 and a note rather than dividing by zero).
func Calibrate(recs []LabelRecord) CalibrationReport {
	groups := groupOutcomes(recs)
	rep := CalibrationReport{BySource: map[string]SourceCalib{}}

	judge := SourceCalib{Source: schema.LabelJudge}
	impl := SourceCalib{Source: schema.LabelImplicit}
	var jhAgree, jhPairs, nExec int

	for _, so := range groups {
		if so.executed != nil {
			nExec++
			if so.judge != nil {
				accumulate(&judge, *so.judge, *so.executed)
			}
			if so.implicit != nil {
				accumulate(&impl, *so.implicit, *so.executed)
			}
		}
		if so.judge != nil && so.implicit != nil {
			jhPairs++
			if *so.judge == *so.implicit {
				jhAgree++
			}
		}
	}

	finalize(&judge)
	finalize(&impl)
	rep.BySource["judge"] = judge
	rep.BySource["implicit"] = impl
	rep.NExecuted = nExec
	rep.NJudgeHeurPairs = jhPairs
	if jhPairs > 0 {
		rep.JudgeHeurAgreement = float64(jhAgree) / float64(jhPairs)
	}
	if judge.N == 0 && impl.N == 0 {
		rep.Note = "no weak labels overlap executed truth yet — run the judge/heuristics on oracle-bearing sessions to calibrate"
	}
	return rep
}

// accumulate updates confusion counts. positive class = inadequate (outcome 0).
func accumulate(c *SourceCalib, pred, truth int) {
	c.N++
	predInadequate := pred == 0
	truthInadequate := truth == 0
	switch {
	case predInadequate && truthInadequate:
		c.TP++
	case predInadequate && !truthInadequate:
		c.FP++
	case !predInadequate && truthInadequate:
		c.FN++
	default:
		c.TN++
	}
}

func finalize(c *SourceCalib) {
	if c.N == 0 {
		return
	}
	c.Accuracy = float64(c.TP+c.TN) / float64(c.N)
	if c.TP+c.FP > 0 {
		c.Precision = float64(c.TP) / float64(c.TP+c.FP)
	}
	if c.TP+c.FN > 0 {
		c.Recall = float64(c.TP) / float64(c.TP+c.FN)
	}
	if c.Precision+c.Recall > 0 {
		c.F1 = 2 * c.Precision * c.Recall / (c.Precision + c.Recall)
	}
}

// JudgeAccuracy returns the calibrated judge accuracy, or 0 if uncalibrated.
func (r CalibrationReport) JudgeAccuracy() float64 {
	if c, ok := r.BySource["judge"]; ok && c.N > 0 {
		return c.Accuracy
	}
	return 0
}

// SaveCalibration writes the report to <dir>/calibration/report.json.
func SaveCalibration(dir string, rep CalibrationReport) error {
	cdir := filepath.Join(dir, "calibration")
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	return os.WriteFile(filepath.Join(cdir, "report.json"), b, 0o644)
}
