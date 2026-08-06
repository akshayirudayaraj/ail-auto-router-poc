import { useEffect } from "react";
import { type FitResult } from "../api";
import { useConsole } from "../store";
import { BarChart } from "../components/BarChart";

const LEADER_COLS = [
  "aiq",
  "auc",
  "ece",
  "escalation@thr",
  "quality@thr",
  "qual_retention",
  "cost_vs_local",
  "under_escal_cellB",
  "over_escalation",
];

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

// For each router, the fraction of gold prompts it sends to LOCAL vs FRONTIER
// (@ threshold), alongside the quality it retains vs always-frontier. The win
// condition: match always-frontier's quality (qual_retention ≈ 1.0) while
// keeping a HIGH share local (low escalation = low cost).
function RoutingDist({ fit }: { fit: FitResult }) {
  const nGold = fit.n_gold || 0;
  const rows = [...(fit.leaderboard || [])].sort(
    (a, b) => ((a.metrics["escalation@thr"] ?? 0) as number) - ((b.metrics["escalation@thr"] ?? 0) as number),
  );

  return (
    <>
      <div id="evals-dist">
        {rows.map((row) => {
          const esc = (row.metrics["escalation@thr"] ?? 0) as number;
          const qr = row.metrics["qual_retention"];
          const froN = Math.round(esc * nGold),
            locN = nGold - froN;
          const locPct = Math.round((1 - esc) * 100),
            froPct = 100 - locPct;

          let qBadge = <span className="muted small">—</span>;
          if (qr != null) {
            const good = qr >= 0.98;
            qBadge = (
              <span className={"chip " + (good ? "ok" : qr >= 0.9 ? "warn" : "bad")}>
                quality {(qr * 100).toFixed(0)}% of frontier
              </span>
            );
          }

          return (
            <div key={row.router} className="dist-row">
              <span className="rname">{row.router}</span>
              <div className="stack">
                {locPct > 0 && (
                  <span className="seg loc" style={{ width: `${locPct}%` }}>
                    {locPct >= 12 ? `${locPct}%` : ""}
                  </span>
                )}
                {froPct > 0 && (
                  <span className="seg fro" style={{ width: `${froPct}%` }}>
                    {froPct >= 12 ? `${froPct}%` : ""}
                  </span>
                )}
              </div>
              <span className="dist-counts muted small">
                {nGold ? `${locN} local · ${froN} frontier` : `${locPct}% local · ${froPct}% frontier`}
              </span>
              {qBadge}
            </div>
          );
        })}
      </div>
      <div className="dist-legend muted small">
        <span className="swatch loc" /> routed to local (gpt-oss:20b, cheap) <span className="swatch fro" /> escalated
        to frontier (opus). Ideal: a learned router near always-frontier's quality while keeping a high local share.
      </div>
    </>
  );
}

export function EvalsTab() {
  const { fit, ensureFit } = useConsole();

  useEffect(() => {
    ensureFit();
  }, [ensureFit]);

  const hasGold = !!(fit && !fit.error && fit.has_gold && (fit.leaderboard || []).length);

  let best = -1;
  if (hasGold) (fit!.leaderboard || []).forEach((x) => (best = Math.max(best, (x.metrics.aiq as number) || 0)));

  return (
    <section className="tab active">
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
          <h3>
            Dual-arm gold leaderboard{" "}
            <span className="muted">(absolute cost/quality — the only trustworthy anchor)</span>
          </h3>
          <div className="chart">
            <BarChart
              items={(fit!.leaderboard || []).map((x) => ({ label: x.router, value: (x.metrics.aiq as number) || 0 }))}
              color="var(--accent)"
              fmtV={(v) => v.toFixed(3)}
            />
          </div>
          <div className="tablewrap">
            <table>
              <thead>
                <tr>
                  <th>router</th>
                  {LEADER_COLS.map((c) => (
                    <th key={c} className="num">
                      {c}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {(fit!.leaderboard || []).map((row) => (
                  <tr key={row.router}>
                    <td className="rname">{row.router}</td>
                    {LEADER_COLS.map((m) => {
                      const v = row.metrics[m];
                      const isBest = m === "aiq" && v != null && (v as number) > 0 && Math.abs((v as number) - best) < 1e-9;
                      return (
                        <td
                          key={m}
                          className="num"
                          style={isBest ? { fontWeight: 700, color: "var(--good)" } : undefined}
                        >
                          {v == null ? "" : (v as number).toFixed(3)}
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p
            className="muted small"
            dangerouslySetInnerHTML={{
              __html:
                "AIQ = area under the cost/quality hull (higher = more quality per unit cost; best highlighted). under_escal_cellB = stayed local but frontier would have passed (the costly miss). Only these gold numbers are absolute.",
            }}
          />

          <h3>
            Routing distribution{" "}
            <span className="muted">(local vs frontier @ threshold — cost vs quality trade-off)</span>
          </h3>
          <RoutingDist fit={fit!} />
        </>
      )}

      <h3>Harness methods</h3>
      <EvalMethods />
    </section>
  );
}
