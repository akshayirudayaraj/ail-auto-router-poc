package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/router"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// Data is everything the evaluation methods draw on. Embedder/Ctx are optional
// and only used by methods that build fresh probes (the guardrail suite).
type Data struct {
	Cfg       config.Config
	Pointwise []schema.PointwiseRow
	Pairwise  []schema.PairwiseRow
	Gold      []schema.GoldRow

	Embedder Embedder
	Ctx      context.Context
}

// ReportRow is one router's metrics under a method.
type ReportRow struct {
	Router  string             `json:"router"`
	Metrics map[string]float64 `json:"metrics"`
}

// Report is the uniform output of every EvalMethod.
type Report struct {
	Method   string      `json:"method"`
	Warnings []string    `json:"warnings,omitempty"`
	Notes    []string    `json:"notes,omitempty"`
	Rows     []ReportRow `json:"rows"`
	Detail   any         `json:"detail,omitempty"`
}

// EvalMethod is one evaluation approach producing a structured Report.
type EvalMethod interface {
	Name() string
	Run(routers []router.Router, d Data) (Report, error)
}

// TrainDataFrom builds a router.TrainData for a chosen training label source.
func TrainDataFrom(d Data, trainSource schema.LabelSource) router.TrainData {
	return router.TrainData{
		Pointwise:     d.Pointwise,
		Pairwise:      d.Pairwise,
		LocalModels:   d.Cfg.LocalModels,
		FrontierModel: d.Cfg.FrontierModel,
		TrainSource:   trainSource,
	}
}

// scoreGold returns a router's escalation score for every gold row.
func scoreGold(r router.Router, gold []schema.GoldRow) []float64 {
	out := make([]float64, len(gold))
	for i, row := range gold {
		out[i] = r.Score(router.InstanceFromGold(row))
	}
	return out
}

// Markdown renders a report as a table plus warnings/notes.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n\n", r.Method)
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "> ⚠️ %s\n\n", w)
	}
	if len(r.Rows) > 0 {
		// stable column order across rows
		cols := metricColumns(r.Rows)
		fmt.Fprintf(&b, "| router |")
		for _, c := range cols {
			fmt.Fprintf(&b, " %s |", c)
		}
		fmt.Fprintf(&b, "\n|---|")
		for range cols {
			fmt.Fprintf(&b, "--:|")
		}
		b.WriteString("\n")
		for _, row := range r.Rows {
			fmt.Fprintf(&b, "| %s |", row.Router)
			for _, c := range cols {
				fmt.Fprintf(&b, " %.3f |", row.Metrics[c])
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	return b.String()
}

func metricColumns(rows []ReportRow) []string {
	set := map[string]bool{}
	for _, r := range rows {
		for k := range r.Metrics {
			set[k] = true
		}
	}
	cols := make([]string, 0, len(set))
	for k := range set {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	return cols
}
