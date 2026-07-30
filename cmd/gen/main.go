// Command gen produces synthetic Claude-Code session logs (Pillar 1a).
package main

import (
	"log"
	"os"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/generate"
)

func main() {
	lg := log.New(os.Stderr, "[gen] ", 0)
	cfg, err := config.Load()
	if err != nil {
		lg.Fatalf("config: %v", err)
	}
	g := generate.New(cfg)
	path, turns, err := g.Run()
	if err != nil {
		lg.Fatalf("generate: %v", err)
	}
	lg.Printf("wrote %d turns across %d sessions -> %s (seed=%d, epsilon=%.2f)",
		turns, cfg.NumSessions, path, cfg.Seed, cfg.EpsilonGreedy)
}
