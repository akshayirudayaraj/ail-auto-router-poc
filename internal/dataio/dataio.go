// Package dataio provides stdlib-only JSONL loaders for the structured
// datasets, shared by the train and eval commands.
package dataio

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

func loadJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

// LoadPointwise reads data/pointwise.jsonl.
func LoadPointwise(cfg config.Config) ([]schema.PointwiseRow, error) {
	return loadJSONL[schema.PointwiseRow](filepath.Join(cfg.DataDir, "pointwise.jsonl"))
}

// LoadPairwise reads data/pairwise.jsonl.
func LoadPairwise(cfg config.Config) ([]schema.PairwiseRow, error) {
	return loadJSONL[schema.PairwiseRow](filepath.Join(cfg.DataDir, "pairwise.jsonl"))
}

// LoadGold reads data/gold.jsonl.
func LoadGold(cfg config.Config) ([]schema.GoldRow, error) {
	return loadJSONL[schema.GoldRow](filepath.Join(cfg.DataDir, "gold.jsonl"))
}
