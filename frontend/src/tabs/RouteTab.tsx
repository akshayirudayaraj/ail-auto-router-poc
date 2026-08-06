import { useState } from "react";
import { apiPost, type RouteResult } from "../api";
import { fmt } from "../format";
import { useConsole } from "../store";

const ROUTE_EXAMPLES = [
  "Reverse a string in Go.",
  "Add a --verbose flag to this CLI using the flag package.",
  "Implement a thread-safe LRU cache in Go with O(1) Get/Put using a map and a doubly linked list.",
  "Implement lock-free MPSC queue in Go with atomic CAS, ABA-safe, correct memory ordering, race-free.",
];

export function RouteTab() {
  const { routers } = useConsole();
  const [prompt, setPrompt] = useState("");
  const [router, setRouter] = useState(""); // "" = all (majority vote)
  const [turnType, setTurnType] = useState("open");
  const [threshold, setThreshold] = useState(0.5);
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<RouteResult | null>(null);

  const selectable = routers.filter((r) => r.kind !== "baseline");

  const run = async () => {
    const p = prompt.trim();
    if (!p) {
      setStatus("enter a prompt");
      return;
    }
    setBusy(true);
    setStatus("embedding + scoring…");
    const r = await apiPost<RouteResult>("/api/route", { prompt: p, turn_type: turnType, threshold });
    setBusy(false);
    if (r.error) {
      setStatus(r.error);
      setResult(null);
      return;
    }
    setStatus(
      r.embedding_dim
        ? `embedded (${r.embedding_dim}-d)`
        : "no embedding (" + (r.embed_error || "offline") + ") — using feature priors",
    );
    setResult(r);
  };

  return (
    <section className="tab active">
      <div className="panel route-panel">
        <label className="block">
          New prompt
          <textarea
            rows={5}
            placeholder="e.g. Implement a thread-safe LRU cache in Go with O(1) Get/Put."
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
          />
        </label>
        <div className="controls">
          <label>
            Routing algorithm
            <select value={router} onChange={(e) => setRouter(e.target.value)}>
              <option value="">all (majority vote)</option>
              {selectable.map((r) => (
                <option key={r.name} value={r.name}>
                  {r.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            Turn type
            <select value={turnType} onChange={(e) => setTurnType(e.target.value)}>
              <option value="open">open</option>
              <option value="followup">followup</option>
            </select>
          </label>
          <label>
            Threshold
            <input
              type="number"
              min={0}
              max={1}
              step={0.05}
              value={threshold}
              onChange={(e) => setThreshold(parseFloat(e.target.value))}
            />
          </label>
          <button className="primary" onClick={run} disabled={busy}>
            Route
          </button>
          <span className="muted">{status}</span>
        </div>
        <div className="examples">
          <span className="muted small">try:</span>
          {ROUTE_EXAMPLES.map((ex) => (
            <span key={ex} className="ex" onClick={() => setPrompt(ex)}>
              {ex.slice(0, 42) + (ex.length > 42 ? "…" : "")}
            </span>
          ))}
        </div>
      </div>

      {result && <RouteResultView r={result} pick={router} />}
    </section>
  );
}

function RouteResultView({ r, pick }: { r: RouteResult; pick: string }) {
  let verdict: string, sub: string;
  if (pick) {
    const one = r.routers.find((x) => x.name === pick);
    verdict = one ? (one.escalate ? "↑ escalate → frontier" : "→ stay local") : "router not found";
    sub = one
      ? `${pick} scored ${one.score.toFixed(3)} at threshold ${r.threshold}. Local = ${r.local_model}, frontier = ${r.frontier_model}.`
      : "";
  } else {
    const esc = r.escalate_votes,
      tot = r.total_routers,
      maj = esc > tot / 2;
    verdict = maj ? "↑ escalate → frontier" : "→ stay local";
    sub = `${esc}/${tot} routers vote escalate at threshold ${r.threshold}. Local = ${r.local_model}, frontier = ${r.frontier_model}.`;
  }

  return (
    <div>
      <div className="verdict">
        <div className="big">{verdict}</div>
        <div className="muted">{sub}</div>
      </div>

      <h3>Per-router decision</h3>
      <div className="panel">
        <div className="router-row">
          <b>router</b>
          <b>escalation score</b>
          <b style={{ textAlign: "right" }}>score</b>
          <b style={{ textAlign: "right" }}>decision</b>
        </div>
        {r.routers.map((rt) => (
          <div key={rt.name} className={"router-row" + (pick === rt.name ? " picked" : "")}>
            <span className="rname">{rt.name}</span>
            <div className="bar">
              <span style={{ width: `${Math.round(rt.score * 100)}%` }} />
            </div>
            <span className="num mono" style={{ textAlign: "right" }}>
              {rt.score.toFixed(3)}
            </span>
            <span style={{ textAlign: "right" }}>
              <span className={"chip " + (rt.escalate ? "warn" : "ok")}>{rt.escalate ? "frontier" : "local"}</span>
            </span>
          </div>
        ))}
      </div>

      <h3>
        Prompt features <span className="muted">(model-free, computed pre-generation)</span>
      </h3>
      <div className="feat-grid">
        {r.features.map((f) => (
          <div key={f.name} className="feat">
            <span className="fname">{f.name}</span>
            <span className="fval">{String(fmt(f.value as any))}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
