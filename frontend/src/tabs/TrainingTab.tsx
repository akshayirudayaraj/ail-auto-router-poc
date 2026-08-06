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

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

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
  const [fitAllAt, setFitAllAt] = useState(""); // timestamp of the last successful Fit-all
  const [fitAllErr, setFitAllErr] = useState("");

  useEffect(() => {
    ensureFit();
  }, [ensureFit]);

  const ds = fit?.data_summary;
  const training = fit?.training || {};
  const abilities = fit?.abilities || [];

  const onFitAll = async () => {
    setBusyAll(true);
    setFitAllAt("");
    setFitAllErr("");
    try {
      // Fitting is near-instant; hold the running state a beat so it's visible.
      const [res] = await Promise.all([runFit({ source: srcAll, threshold }), sleep(450)]);
      if (res.error) setFitAllErr(res.error);
      else setFitAllAt(new Date().toLocaleTimeString());
    } catch (e) {
      setFitAllErr(String(e));
    } finally {
      setBusyAll(false); // never wedge the button, even if the fetch throws
    }
  };

  const onFitRouter = async (name: string) => {
    const source = srcByRouter[name] || "all";
    setBusyRouter((b) => ({ ...b, [name]: true }));
    setRouterStatus((s) => ({ ...s, [name]: "" }));
    try {
      const [res] = await Promise.all([fitRouter(name, source), sleep(450)]);
      setRouterStatus((s) => ({
        ...s,
        [name]: res.error ? `error: ${res.error}` : `✓ fit on ${res.train_source} · ${new Date().toLocaleTimeString()}`,
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
          {busyAll ? (
            <span className="fit-status running">
              <span className="spinner" /> fitting all routers…
            </span>
          ) : fitAllErr ? (
            <span className="fit-status err">✗ {fitAllErr}</span>
          ) : fitAllAt ? (
            <span className="fit-status done">
              ✓ fitted {fit?.train_source} · {fit?.n_pointwise ?? 0} pointwise / {fit?.n_pairwise ?? 0} pairwise ·
              gold {fit?.n_gold ?? 0} · {fitAllAt}
            </span>
          ) : (
            <span className="muted small">{fitStatus}</span>
          )}
        </div>
        <p className="muted small">
          Each router consumes a specific data <b>shape</b>: IRT and kNN read <b>pointwise</b>; RouteLLM reads{" "}
          <b>pairwise</b> (plus pointwise pseudo-pairs). Fit them together above, or individually below with their own
          label source. Training on <code>executed</code> makes the gold leaderboard optimistic (the CLI backtest
          refuses it — eval must be stronger than train).
        </p>
      </div>

      {/* ---- per-router table ---- */}
      <h3>Routing methods</h3>
      <div className="tablewrap">
        <table className="methods-table">
          <thead>
            <tr>
              <th>method</th>
              <th>type</th>
              <th>data shape</th>
              <th>trained on</th>
              <th>label source</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {routers.map((r) => (
              <MethodRow
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
          </tbody>
        </table>
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

function MethodRow({
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
    <tr className={busy ? "training" : ""}>
      <td>
        <div className="rname">{meta.name}</div>
        <div className="muted small">{meta.description}</div>
      </td>
      <td>
        <span className={"chip kind-" + meta.kind}>{meta.kind}</span>
      </td>
      <td>{meta.shape && meta.shape !== "none" ? <span className="chip shape">{meta.shape}</span> : <span className="muted">—</span>}</td>
      <td className="trained-on">
        {trainable ? (
          busy ? (
            <span className="training-badge">● training…</span>
          ) : (
            trainedLabel(meta.name, trained)
          )
        ) : (
          <span className="muted">{meta.kind === "baseline" ? "no training (fixed anchor)" : "python artifact / prior"}</span>
        )}
        {status && <div className="muted small">{status}</div>}
      </td>
      <td>
        {trainable ? (
          <select value={source} onChange={(e) => onSource(e.target.value)} disabled={busy}>
            {sources.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        ) : (
          <span className="muted">—</span>
        )}
      </td>
      <td className="num">
        {trainable ? (
          <button className="primary" onClick={onFit} disabled={busy}>
            {busy ? "training…" : "Fit"}
          </button>
        ) : (
          <span className="muted">—</span>
        )}
      </td>
    </tr>
  );
}
