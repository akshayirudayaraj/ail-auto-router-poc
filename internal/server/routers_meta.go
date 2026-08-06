package server

import (
	"net/http"

	"github.com/akshayirudayaraj/ail-routing-test/internal/router"
)

// routers_meta.go exposes the router registry as metadata: name, kind
// (baseline / learned / stub), and a one-line description. Powers the Training
// tab's method list and the Route tab's algorithm selector.

type routerMeta struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

var routerDescriptions = map[string]struct {
	kind, desc string
}{
	"always-local":      {"baseline", "Never escalate. Lower bound on cost; upper bound on under-escalation. Eval anchor."},
	"always-frontier":   {"baseline", "Always escalate. Upper bound on quality and cost. Eval anchor."},
	"routellm-logistic": {"learned", "RouteLLM-style logistic classifier over prompt features + pairwise preferences."},
	"irt-1pl":           {"learned", "1-parameter IRT: P(adequate)=σ(θ_model − b_prompt). Escalates when the local rung's success probability is low."},
	"knn":               {"learned", "k-NN over prompt embeddings: escalation score = neighbors' local-inadequate rate."},
	"encoder-mlp(stub)": {"stub", "Encoder + MLP head over embeddings (non-portable stub; placeholder scores)."},
	"slm-head(stub)":    {"stub", "Small-LM classification head (non-portable stub; placeholder scores)."},
}

func (s *Server) handleRouters(w http.ResponseWriter, r *http.Request) {
	var out []routerMeta
	for _, rt := range router.Registry() {
		m := routerMeta{Name: rt.Name(), Kind: "learned"}
		if d, ok := routerDescriptions[rt.Name()]; ok {
			m.Kind, m.Description = d.kind, d.desc
		}
		out = append(out, m)
	}
	writeJSON(w, 200, map[string]any{"routers": out})
}
