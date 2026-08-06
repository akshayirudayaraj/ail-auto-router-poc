// Command serve starts the web console: browse log traces + labeled data, fit
// the routers (IRT recovery + gold leaderboard), and route a new prompt live.
//
//	make serve            # then open http://localhost:8080
//	AIL_ADDR=:9000 make serve
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/akshayirudayaraj/ail-routing-test/internal/backend"
	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/dataio"
	"github.com/akshayirudayaraj/ail-routing-test/internal/server"
)

func main() {
	lg := log.New(os.Stderr, "[serve] ", 0)
	cfg, err := config.Load()
	if err != nil {
		lg.Fatalf("config: %v", err)
	}
	// Adapt the roster to the served models in this data dir (no-op on synthetic;
	// picks up gpt-oss:20b / opus on the agentic set) so the console classifies
	// local vs frontier correctly and fits the right models.
	cfg = dataio.ResolveRoster(cfg)
	be := backend.New(cfg, lg)
	srv := server.New(cfg, be, lg)

	addr := os.Getenv("AIL_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	lg.Printf("console on http://localhost%s  (data dir: %s)", addr, cfg.DataDir)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		lg.Fatalf("serve: %v", err)
	}
}
