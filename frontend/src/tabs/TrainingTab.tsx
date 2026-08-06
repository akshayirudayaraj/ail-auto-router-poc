import { useEffect, useState } from "react";
import { signed } from "../format";
import { useConsole } from "../store";
import { ModelChip } from "../components/chips";
import { BarChart } from "../components/BarChart";
import { PairwiseTable, PointwiseTable } from "../components/DatasetTables";
import type { RouterMeta, TrainedOn } from "../api";

// Sources available to a router of a given shape (only ones with data, + "all").
function sourcesForShape(shape: string | undefined, ds: any): string[] {
  const bySrc =
    shape === "pairwise" ? ds?.pairwise?.by_source : ds?.pointwise?.by_source;
  const keys = bySrc ? Object.keys(bySrc) : [];
  return ["all", ...keys];
}

// One-line description of what a router consumed, from its trained_on breakdown.
function trainedLabel(name: string, t?: TrainedOn): string {
  if (!t) return "not fit yet";
  if (name === "routellm-logistic")
    return `${t.pairwise ?? 0} pairwise + ${t.pseudo ?? 0} pointwise pseudo-pairs`;
  return `${t.count} ${t.shape}`;
}

export function TrainingTab() {
  const { routers, fit, fitStatus, runFit, ensureFit, fitRouter } = useConsole();
  const [threshold, setThreshold] = useState(0.5);
  const [srcAll, setSrcAll] = useState("all");
  const [busyAll, setBusyAll] = useState(false);
  // Per-router source selection + in-flight state.
  const [srcByRouter, setSrcByRouter] = useState<Record<string, string>>({});
  const [busyRouter, setBusyRouter] = useState<Record<string, string | boolean>>({});
  const [routerStatus, setRouterStatus] = useState<Record<string, string>>({});

  useEffect(() => {
    ensureFit();
  }, [ensureFit]);

  const ds = fit?.data_summary;
  const training = fit?.training || {};
  const abilities = fit?.abilities || [];

  const onFitAll = async () => {
    setBusyAll(true);
    try {
      await runFit({ source: srcAll, threshold });
    } finally {
      setBusyAll(false); // never wedge the button, even if the fetch throws
    }
  };

  const onFitRouter = async (name: string) => {
    const source = srcByRouter[name] || "all";
    setBusyRouter((b) => ({ ...b, [name]: true }));
    setRouterStatus((s) => ({ ...s, [name]: "" }));
    try {
      const res = await fitRouter(name, source);
      setRouterStatus((s) => ({
        ...s,
        [name]: res.error ? `error: ${res.error}` : `fit on ${res.train_source}`,
      }));
    } catch (e) {
      setRouterStatus((s) => ({ ...s, [name]: `failed: ${String(e)}` }));
    } finally {
      setBusyRouter((b) => ({ ...b, [name]: false }));
    }
  };

  return (
    <section className="tab active">
      {/* ---- training data summary + fit-all ---- */}
      <div className="panel">
        <div className="datasum">
          <b>Training data</b> <span className="muted small">(fused canonical labels)</span>
          {ds ? (
            <div className="datasum-nums">
              <span>
                pointwise <b>{ds.pointwise?.total ?? 0}</b>
                {ds.pointwise?.by_source && (
                  <span className="muted"> ({bySrc(ds.pointwise.by_source)})</span>
                )}
              </span>
              <span>
                pairwise <b>{ds.pairwise?.total ?? 0}</b>
                {ds.pairwise?.by_source && (
                  <span className="muted"> ({bySrc(ds.pairwise.by_source)})</span>
                )}
              </span>
              <span>
                embedded <b>{ds.embedded ?? 0}</b>
              </span>
            </div>
          ) : (
            <div className="muted small">loading…</div>
          )}
        </div>
        <div className="controls">
          <label>
            All-fit source
            <select value={srcAll} onChange={(e) => setSrcAll(e.target.value)}>
              {sourcesForShape("pointwise", ds).map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <label>
            Operating threshold
            <input
              type="number"
              min={0}
              max={1}
              step={0.05}
              value={threshold}
              onChange={(e) => setThreshold(parseFloat(e.target.value))}
            />
          </label>
          <button className="primary" onClick={onFitAll} disabled={busyAll}>
            {busyAll ? "training…" : "Fit all routers"}
          </button>
          {busyAll && <span className="training-badge">● running</span>}
          <span className="muted">{fitStatus}</span>
        </div>
        <p className="muted small">
          Each router consumes a specific data <b>shape</b>: IRT and kNN read <b>pointwise</b>; RouteLLM reads{" "}
          <b>pairwise</b> (plus pointwise pseudo-pairs). Fit them together above, or individually below with their own
          label source. Training on <code>executed</code> makes the gold leaderboard optimistic (the CLI backtest
          refuses it — eval must be stronger than train).
        </p>
      </div>

      {/* ---- per-router cards ---- */}
      <h3>Routing methods</h3>
      <div className="methods">
        {routers.map((r) => (
          <RouterCard
            key={r.name}
            meta={r}
            trained={training[r.name]}
            source={srcByRouter[r.name] || "all"}
            sources={sourcesForShape(r.shape, ds)}
            busy={!!busyRouter[r.name]}
            status={routerStatus[r.name]}
            onSource={(s) => setSrcByRouter((m) => ({ ...m, [r.name]: s }))}
            onFit={() => onFitRouter(r.name)}
          />
        ))}
      </div>

      {/* ---- IRT ability recovery ---- */}
      <h3>
        IRT ability recovery <span className="muted">(θ, reference-centered — higher = more capable)</span>
      </h3>
      {abilities.length ? (
        <>
          <div className="chart">
            <BarChart
              items={abilities.map((a) => ({ label: a.model, value: a.recovered }))}
              diverging
              fmtV={signed}
            />
          </div>
          <div className="tablewrap">
            <table>
              <thead>
                <tr>
                  <th>model</th>
                  <th className="num">planted θ</th>
                  <th className="num">recovered θ</th>
                </tr>
              </thead>
              <tbody>
                {abilities.map((a) => (
                  <tr key={a.model}>
                    <td>
                      <ModelChip model={a.model} />
                    </td>
                    <td className="num">{a.planted == null ? "—" : signed(a.planted)}</td>
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
        <p className="muted small">{fit?.error ? fit.error : "Fit the routers to see IRT ability recovery."}</p>
      )}

      <h3>
        Pointwise rows <span className="muted">(single-arm (model, prompt) → outcome — IRT/kNN training unit)</span>
      </h3>
      <PointwiseTable />

      <h3>
        Pairwise rows <span className="muted">(preference A vs B on the same prompt — logistic router training unit)</span>
      </h3>
      <PairwiseTable />
    </section>
  );
}

// bySrc renders a "{src} {n}" summary, e.g. "executed 36".
function bySrc(m: Record<string, number>): string {
  return Object.entries(m)
    .map(([k, v]) => `${k} ${v}`)
    .join(" · ");
}

function RouterCard({
  meta,
  trained,
  source,
  sources,
  busy,
  status,
  onSource,
  onFit,
}: {
  meta: RouterMeta;
  trained?: TrainedOn;
  source: string;
  sources: string[];
  busy: boolean;
  status?: string;
  onSource: (s: string) => void;
  onFit: () => void;
}) {
  const trainable = meta.trainable !== false && meta.kind === "learned";
  return (
    <div className={"method" + (busy ? " training" : "")}>
      <div className="mhead">
        <span className="rname">{meta.name}</span>
        <span className={"chip kind-" + meta.kind}>{meta.kind}</span>
        {meta.shape && meta.shape !== "none" && <span className="chip shape">{meta.shape}</span>}
      </div>
      <div className="muted small">{meta.description}</div>

      {trainable ? (
        <>
          <div className="mcontrols">
            <select value={source} onChange={(e) => onSource(e.target.value)} disabled={busy}>
              {sources.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
            <button onClick={onFit} disabled={busy}>
              {busy ? "training…" : "Fit"}
            </button>
            {busy && <span className="training-badge">● running</span>}
          </div>
          <div className="muted small trained-on">
            {busy ? "training…" : <>← trained on {trainedLabel(meta.name, trained)}</>}
          </div>
          {status && <div className="muted small">{status}</div>}
        </>
      ) : (
        <div className="muted small trained-on">
          {meta.kind === "baseline" ? "no training (fixed anchor)" : "python artifact / prior — not fit here"}
        </div>
      )}
    </div>
  );
}
