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
	// Shape is the training data a router consumes ("pointwise" | "pairwise" |
	// "none"). Trainable is false for baselines/stubs (no fit button in the UI).
	Shape     string `json:"shape"`
	Trainable bool   `json:"trainable"`
}

var routerDescriptions = map[string]struct {
	kind, desc, shape string
	trainable         bool
}{
	"always-local":      {"baseline", "Never escalate. Lower bound on cost; upper bound on under-escalation. Eval anchor.", "none", false},
	"always-frontier":   {"baseline", "Always escalate. Upper bound on quality and cost. Eval anchor.", "none", false},
	"routellm-logistic": {"learned", "RouteLLM-style logistic classifier over prompt features + pairwise preferences.", "pairwise", true},
	"irt-1pl":           {"learned", "1-parameter IRT: P(adequate)=σ(θ_model − b_prompt). Escalates when the local rung's success probability is low.", "pointwise", true},
	"knn":               {"learned", "k-NN over prompt embeddings: escalation score = neighbors' local-inadequate rate.", "pointwise", true},
	"encoder-mlp(stub)": {"stub", "Encoder + MLP head over embeddings (non-portable stub; placeholder scores).", "none", false},
	"slm-head(stub)":    {"stub", "Small-LM classification head (non-portable stub; placeholder scores).", "none", false},
}

func (s *Server) handleRouters(w http.ResponseWriter, r *http.Request) {
	var out []routerMeta
	for _, rt := range router.Registry() {
		m := routerMeta{Name: rt.Name(), Kind: "learned", Shape: "pointwise", Trainable: true}
		if d, ok := routerDescriptions[rt.Name()]; ok {
			m.Kind, m.Description, m.Shape, m.Trainable = d.kind, d.desc, d.shape, d.trainable
		}
		out = append(out, m)
	}
	writeJSON(w, 200, map[string]any{"routers": out})
}
