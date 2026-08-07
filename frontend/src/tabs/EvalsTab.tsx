import { useEffect, useState } from "react";
import { type Ability, type Anchor, type LeaderRow } from "../api";
import { useConsole } from "../store";
import { signed } from "../format";
import { CostQualityPlot } from "../components/CostQualityPlot";
import { GoldTable } from "../components/DatasetTables";
import { ModelChip } from "../components/chips";
import { HelpTip } from "../components/HelpTip";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// Headline scorecard = local share + quality retention. Everything else is a
// supporting or secondary/diagnostic column.
const PRIMARY_COLS = ["local_share@thr", "qual_retention", "safety", "thrift", "savings_capture", "under_escal_cellB"];
const SECONDARY_COLS = ["offload_isoq", "escalation@thr", "quality@thr", "over_escalation", "aiq", "auc", "ece", "cost_vs_local"];

const COL_LABEL: Record<string, string> = {
  "local_share@thr": "local share",
  qual_retention: "quality vs Opus",
  safety: "safety",
  thrift: "thrift",
  savings_capture: "vs oracle",
  under_escal_cellB: "quality leaks",
  offload_isoq: "offload_isoq",
  "escalation@thr": "escalation@thr",
  "quality@thr": "quality@thr",
  over_escalation: "over_escal",
  aiq: "aiq",
  auc: "auc",
  ece: "ece",
  cost_vs_local: "cost_vs_local",
};

// One-liner explanations, surfaced as native hover tooltips on the headers.
const COL_HELP: Record<string, string> = {
  router: "The routing policy being scored on the dual-arm gold set.",
  "local_share@thr": "Share of requests kept on the cheap local model at the router's tuned isoquality operating point — equals offload_isoq (higher = cheaper).",
  qual_retention: "Adequacy retained vs always-Opus at the operating point — 100% means it matches Opus's quality (can exceed 100% where local beats Opus).",
  safety: "Of prompts where local would fail, the share the router correctly escalated to Opus (protects quality; want 100%).",
  thrift: "Of prompts where local would pass, the share the router correctly kept local at the operating point (captures the savings; want 100%).",
  savings_capture: "Local share as a fraction of a perfect oracle's — how much of the safely-offloadable traffic it captured.",
  under_escal_cellB: "The fraction of prompts where the router kept the request on local, local failed, and Opus would have passed (want 0%).",
  offload_isoq: "Max local share reachable while matching always-Opus quality. This IS the tuned operating point every headline metric is read at (so local_share@thr equals it).",
  "escalation@thr": "Share of requests sent to Opus at the threshold (= 1 − local share).",
  "quality@thr": "Mean achieved adequacy across all requests at the operating threshold.",
  over_escalation: "Escalated to Opus though local would have passed — wasted spend.",
  aiq: "Area under the $-cost/quality hull: quality per unit cost, threshold-independent (higher = better).",
  auc: "Ranking power of the escalation score against 'local inadequate' (0.5 = random, 1.0 = perfect).",
  ece: "Expected calibration error of the escalation score — how well its probabilities match reality (lower = better).",
  cost_vs_local: "Achieved $ cost relative to always-local (1.0 = as cheap as local; higher = pricier).",
};

// metrics shown as percentages; the rest as 3-decimal floats.
const PCT_METRICS = new Set([
  "local_share@thr",
  "qual_retention",
  "safety",
  "thrift",
  "savings_capture",
  "under_escal_cellB",
  "offload_isoq",
  "escalation@thr",
  "quality@thr",
  "over_escalation",
]);

function fmtMetric(key: string, v: number | null | undefined): string {
  if (v == null) return "";
  return PCT_METRICS.has(key) ? `${(v * 100).toFixed(0)}%` : v.toFixed(3);
}

// A column header with an inline "?" that reveals the metric's one-liner on hover.
function MetricTh({ col, num }: { col: string; num?: boolean }) {
  return (
    <th className={num ? "num" : undefined}>
      <span className="th-help">
        {COL_LABEL[col] ?? col}
        {COL_HELP[col] && <HelpTip text={COL_HELP[col]} />}
      </span>
    </th>
  );
}

const EVAL_METHODS: [string, string][] = [
  ["dual-arm-gold", "Both arms' outcomes known → RouterBench-style cost/quality curve, AIQ, and escalation cells. The only trustworthy ABSOLUTE anchor."],
  ["temporal-backtest", "Splits logs by session+time and RANKS routers on held-out future. Enforces eval labels be a strictly-stronger source than train (no circularity)."],
  ["off-policy-ips-dr", "Estimates the reward of DEPLOYING each router from logs via IPS + doubly-robust. Refuses on deterministic logs (no propensities) — expected here."],
  ["guardrail-suite", "Matched perturbation probes: escalation must rise with difficulty and not flip on off-topic keyword injection."],
];

function EvalMethods() {
  return (
    <div className="methods">
      {EVAL_METHODS.map(([n, d]) => (
        <div key={n} className="method">
          <div className="mhead">
            <span className="rname">{n}</span>
          </div>
          <div className="muted small">{d}</div>
        </div>
      ))}
    </div>
  );
}

// champion = the router that keeps the MOST traffic local while still matching
// frontier quality (retention ≈ 1). Falls back to the highest-quality router
// when none reaches full quality. Used to highlight the winning row.
function champion(rows: LeaderRow[]): string {
  const qr = (r: LeaderRow) => (r.metrics["qual_retention"] as number) ?? 0;
  const ls = (r: LeaderRow) => (r.metrics["local_share@thr"] as number) ?? 0;
  const full = rows.filter((r) => qr(r) >= 0.995);
  const pool = full.length ? full : rows;
  let best: LeaderRow | null = null;
  for (const r of pool) {
    if (!best) {
      best = r;
      continue;
    }
    if (full.length ? ls(r) > ls(best) : qr(r) > qr(best)) best = r;
  }
  return best?.router ?? "";
}

// The POC headline: for each learned router, the QUALITY it retains vs always-
// Opus is the main bar (the promise: don't regress), and the share it keeps on
// the cheap LOCAL model is the secondary readout (the payoff). Ideal = a full
// quality bar with a high local share.
function ScoreCard({ rows: rows0, anchors, nGold, champ }: { rows: LeaderRow[]; anchors: Anchor[]; nGold: number; champ: string }) {
  const oracle = anchors.find((a) => a.name === "oracle");
  const rows = [...rows0].sort((a, b) => {
    const qd = ((b.metrics["qual_retention"] ?? 0) as number) - ((a.metrics["qual_retention"] ?? 0) as number);
    if (Math.abs(qd) > 1e-9) return qd;
    return ((b.metrics["local_share@thr"] ?? 0) as number) - ((a.metrics["local_share@thr"] ?? 0) as number);
  });

  return (
    <>
      <div id="evals-dist">
        {rows.map((row) => {
          const ls = (row.metrics["local_share@thr"] ?? 0) as number;
          const qr = (row.metrics["qual_retention"] ?? 0) as number;
          const qPct = Math.round(qr * 100);
          const locPct = Math.round(ls * 100);
          const locN = Math.round(ls * nGold);
          const barColor = qr >= 0.98 ? "var(--good)" : qr >= 0.9 ? "var(--warn)" : "var(--bad)";

          return (
            <div key={row.router} className="dist-row">
              <span className="rname" style={row.router === champ ? { fontWeight: 700, color: "var(--good)" } : undefined}>
                {row.router}
              </span>
              <div className="stack">
                {qPct > 0 && (
                  <span className="seg" style={{ width: `${Math.min(qPct, 100)}%`, background: barColor }}>
                    {qPct >= 12 ? `${qPct}%` : ""}
                  </span>
                )}
              </div>
              <span className="dist-counts muted small">{qPct}% of Opus</span>
              <span
                className="chip"
                style={{ background: "color-mix(in srgb, var(--local) 16%, transparent)", color: "var(--local)" }}
              >
                {nGold ? `${locPct}% local · ${locN}/${nGold}` : `${locPct}% local`}
              </span>
            </div>
          );
        })}
      </div>
      <div className="dist-legend muted small">
        Bar = <b>quality retained vs always-Opus</b> (full = matches Opus). The green chip is the payoff — the share of
        requests kept on <span className="swatch loc" /> local (gpt-oss:20b, cheap).
        {oracle != null && (
          <>
            {" "}
            A perfect <b>oracle</b> holds 100% quality while keeping <b>{Math.round(oracle.local_share * 100)}%</b> local.
          </>
        )}
      </div>
    </>
  );
}

// The primary metrics table: the scorecard numbers plus WHY it works (safety,
// thrift, vs-oracle) and the one cell to minimize (quality leaks). Reference
// anchors are appended, muted, so the learned routers read against the bounds.
function PrimaryTable({ leaderboard, anchors, champ }: { leaderboard: LeaderRow[]; anchors: Anchor[]; champ: string }) {
  return (
    <div className="tablewrap">
      <table>
        <thead>
          <tr>
            <MetricTh col="router" />
            {PRIMARY_COLS.map((c) => (
              <MetricTh key={c} col={c} num />
            ))}
          </tr>
        </thead>
        <tbody>
          {leaderboard.map((row) => (
            <tr key={row.router}>
              <td className="rname" style={row.router === champ ? { fontWeight: 700, color: "var(--good)" } : undefined}>
                {row.router}
              </td>
              {PRIMARY_COLS.map((m) => (
                <td key={m} className="num">
                  {fmtMetric(m, row.metrics[m] as number | null)}
                </td>
              ))}
            </tr>
          ))}
          {anchors.map((a) => (
            <tr key={a.name} style={{ color: "var(--muted)", fontStyle: "italic" }}>
              <td className="rname">{a.name}</td>
              <td className="num">{fmtMetric("local_share@thr", a.local_share)}</td>
              <td className="num">{fmtMetric("qual_retention", a.qual_retention)}</td>
              <td className="num" colSpan={4}>
                {a.name === "oracle" ? "perfect router (upper bound)" : "reference"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SecondaryTable({ leaderboard, abilities }: { leaderboard: LeaderRow[]; abilities: Ability[] }) {
  const hasPlanted = abilities.some((a) => a.planted != null);
  return (
    <details className="panel" style={{ marginTop: 12 }}>
      <summary style={{ cursor: "pointer", color: "var(--muted)" }}>
        Secondary / diagnostic metrics (AIQ, AUC, ECE, offload_isoq, raw cost · IRT ability recovery)
      </summary>
      <div className="tablewrap" style={{ marginTop: 10 }}>
        <table>
          <thead>
            <tr>
              <MetricTh col="router" />
              {SECONDARY_COLS.map((c) => (
                <MetricTh key={c} col={c} num />
              ))}
            </tr>
          </thead>
          <tbody>
            {leaderboard.map((row) => (
              <tr key={row.router}>
                <td className="rname">{row.router}</td>
                {SECONDARY_COLS.map((m) => (
                  <td key={m} className="num">
                    {fmtMetric(m, row.metrics[m] as number | null)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* IRT ability recovery — a diagnostic of the IRT fit, not a routing score:
          are the fitted per-model abilities θ sane (and, on synthetic data with a
          planted truth, are they recovered)? Lives here under the secondary/
          diagnostic dropdown rather than in Training. */}
      <h4 style={{ margin: "18px 0 6px" }}>
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
          IRT ability recovery <span className="muted">(θ, reference-centered — higher = more capable)</span>
          <HelpTip text="Diagnostic of the IRT fit, not a routing metric. The 1-parameter IRT (Rasch) model fits P(model adequate on a prompt) = σ(model ability θ − prompt difficulty) on the TRAINING labels. This table shows each model's fitted θ; the router escalates a prompt when its difficulty exceeds local's ability. 'Recovery' = does the fit recover the PLANTED θ — only defined on synthetic data with a ground truth (the 'planted θ' column). On real data there is no planted truth, so only the ordering and sign of the θ gaps are meaningful." />
        </span>
      </h4>
      {abilities.length ? (
        <>
          <div className="tablewrap" style={{ maxWidth: 400 }}>
            <table>
              <thead>
                <tr>
                  <th>model</th>
                  {hasPlanted && <th className="num">planted θ</th>}
                  <th className="num">recovered θ</th>
                </tr>
              </thead>
              <tbody>
                {abilities.map((a) => (
                  <tr key={a.model}>
                    <td>
                      <ModelChip model={a.model} />
                    </td>
                    {hasPlanted && <td className="num">{a.planted == null ? "—" : signed(a.planted)}</td>}
                    <td className="num">{signed(a.recovered)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p
            className="muted small"
            dangerouslySetInnerHTML={{
              __html:
                "θ is reference-centered (local rung = 0). Only the <b>ordering and sign</b> of the gaps matter for routing; magnitudes compress under noisy labels.",
            }}
          />
        </>
      ) : (
        <p className="muted small">Fit the routers to see IRT ability recovery.</p>
      )}
    </details>
  );
}

// Headline STAT (not a row): the best achievable local offload on this eval —
// the oracle's local share (max routable to local at 100% Opus quality) — next
// to the best a learned router reaches today (offload_isoq).
function BestLocalStat({ anchors, rows }: { anchors: Anchor[]; rows: LeaderRow[] }) {
  const oracle = anchors.find((a) => a.name === "oracle");
  if (!oracle) return null;
  const ceiling = Math.round(oracle.local_share * 100);
  const best = Math.round(Math.max(0, ...rows.map((r) => (r.metrics["offload_isoq"] as number) || 0)) * 100);
  return (
    <div className="cards" style={{ margin: "0 0 20px", gridTemplateColumns: "repeat(2, minmax(190px, 260px))" }}>
      <div
        className="card"
        style={{ borderColor: "var(--local)", background: "color-mix(in srgb, var(--local) 6%, var(--panel))" }}
      >
        <div className="big" style={{ color: "var(--local)" }}>{ceiling}%</div>
        <div className="lbl">best local offload · ceiling</div>
        <div className="muted small" style={{ marginTop: 4 }}>
          max routable to local at 100% Opus quality (perfect oracle)
        </div>
      </div>
      <div className="card">
        <div className="big">{best}%</div>
        <div className="lbl">best learned router today</div>
        <div className="muted small" style={{ marginTop: 4 }}>
          local kept at full quality (offload_isoq)
        </div>
      </div>
    </div>
  );
}

export function EvalsTab() {
  const { fit, routers, ensureFit, runEval } = useConsole();
  const [busy, setBusy] = useState(false);
  const [evalAt, setEvalAt] = useState("");
  const [evalErr, setEvalErr] = useState("");

  useEffect(() => {
    ensureFit();
  }, [ensureFit]);

  const onRunEvals = async () => {
    setBusy(true);
    setEvalAt("");
    setEvalErr("");
    try {
      const [res] = await Promise.all([runEval(), sleep(450)]);
      if (res.error) setEvalErr(res.error);
      else setEvalAt(new Date().toLocaleTimeString());
    } catch (e) {
      setEvalErr(String(e));
    } finally {
      setBusy(false);
    }
  };

  const leaderboard = fit?.leaderboard || [];
  const anchors = fit?.anchors || [];
  const hasGold = !!(fit && !fit.error && fit.has_gold && leaderboard.length);

  // Headline shows only genuinely-LEARNED routers. always-local/always-frontier
  // are baselines (represented by the anchors) and the stubs are placeholders —
  // both would muddy the scorecard and skew the vs-oracle numbers. Fall back to
  // dropping just the two baselines if router metadata hasn't loaded yet.
  const learnedNames = new Set(routers.filter((r) => r.kind === "learned").map((r) => r.name));
  const learnedRows = learnedNames.size
    ? leaderboard.filter((r) => learnedNames.has(r.router))
    : leaderboard.filter((r) => r.router !== "always-local" && r.router !== "always-frontier");
  const champ = hasGold ? champion(learnedRows) : "";

  return (
    <section className="tab active">
      <div className="panel">
        <div className="controls" style={{ alignItems: "center" }}>
          <button className="primary" onClick={onRunEvals} disabled={busy}>
            {busy ? "running evals…" : "Run evals"}
          </button>
          {busy ? (
            <span className="fit-status running">
              <span className="spinner" /> running dual-arm gold benchmark…
            </span>
          ) : evalErr ? (
            <span className="fit-status err">✗ {evalErr}</span>
          ) : evalAt ? (
            <span className="fit-status done">
              ✓ evaluated {fit?.n_gold ?? 0} gold rows · {evalAt}
            </span>
          ) : (
            <span className="muted small">
              Re-runs the dual-arm gold benchmark on the current fit source and refreshes the leaderboard.
            </span>
          )}
        </div>
      </div>

      {!hasGold && (
        <div
          className="note"
          dangerouslySetInnerHTML={{
            __html:
              "No dual-arm <b>gold</b> set yet (executed holdout not populated). Absolute cost/quality numbers appear once grading runs and <code>make agentic-materialize</code> writes gold rows. The harness methods below still describe what will be measured.",
          }}
        />
      )}

      {hasGold && (
        <>
          <BestLocalStat anchors={anchors} rows={learnedRows} />
          <h3>
            Router scorecard <span className="muted">(the POC goal: keep requests on local without losing quality)</span>
          </h3>
          <p className="muted small">
            The promise first: each router should retain <b>~100% of Opus quality</b> (the bar). The payoff: it does so
            while keeping a <b>high share of requests on the cheap local</b> model (the green chip). No cost model needed —
            just achieved quality and fraction-of-requests on the dual-arm gold set.
          </p>
          <p className="muted small" style={{ marginTop: -4 }}>
            <b>Threshold tuning:</b> each router runs at its <i>isoquality</i> operating point — the escalation threshold
            that keeps the most requests local while still matching Opus quality. So the local share below equals the{" "}
            <code>offload_isoq</code> headline, and <code>thrift</code> is read at that same point (not a fixed 0.5).
          </p>
          <ScoreCard rows={learnedRows} anchors={anchors} nGold={fit!.n_gold || 0} champ={champ} />

          <h3 style={{ marginTop: 22 }}>
            How it earns that <span className="muted">(safety · thrift · vs the perfect oracle)</span>
          </h3>
          <PrimaryTable leaderboard={learnedRows} anchors={anchors} champ={champ} />

          <h3 style={{ marginTop: 22 }}>
            Cost / quality map <span className="muted">(each router vs the always-local / oracle / always-Opus anchors)</span>
          </h3>
          <CostQualityPlot leaderboard={learnedRows} anchors={anchors} />

          <SecondaryTable leaderboard={leaderboard} abilities={fit?.abilities || []} />
        </>
      )}

      <h3>
        Gold rows <span className="muted">(dual-arm executed benchmark — the rows behind the scorecard)</span>
      </h3>
      <GoldTable />

      <h3 style={{ marginTop: 22 }}>
        <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
          Temporal backtest <span className="muted">(ranks routers on held-out future logs)</span>
          <HelpTip text="Replays the logs in time order — splitting by session + timestamp — and RANKS each router on the held-out FUTURE it never trained on, the honest test of whether a router generalizes forward rather than memorizing its teacher. Eval labels are enforced to be a strictly-stronger source than the training labels, so a router is never scored against its own labeler (no circularity). It only RANKS routers relative to each other; absolute cost/quality numbers come solely from the dual-arm gold set above. Empty until enough time-stamped log sessions are ingested to form a held-out future window." />
        </span>
      </h3>
      <div className="tablewrap">
        <table style={{ width: "100%" }}>
          <tbody>
            <tr>
              <td className="muted" style={{ textAlign: "center", padding: "28px 0" }}>
                Empty table
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <h3>Harness methods</h3>
      <EvalMethods />
    </section>
  );
}
