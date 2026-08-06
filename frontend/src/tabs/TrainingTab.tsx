import { useEffect, useState } from "react";
import { signed } from "../format";
import { useConsole } from "../store";
import { ModelChip } from "../components/chips";
import { BarChart } from "../components/BarChart";
import { PairwiseTable, PointwiseTable } from "../components/DatasetTables";

export function TrainingTab() {
  const { routers, fit, fitParams, fitStatus, runFit, ensureFit } = useConsole();
  const [source, setSource] = useState(fitParams.source);
  const [threshold, setThreshold] = useState(fitParams.threshold);
  const [busy, setBusy] = useState(false);

  // Fit on first view (cached across tab switches by the store).
  useEffect(() => {
    ensureFit();
  }, [ensureFit]);

  const onFit = async () => {
    setBusy(true);
    await runFit({ source, threshold });
    setBusy(false);
  };

  const abilities = fit?.abilities || [];

  return (
    <section className="tab active">
      <div className="panel">
        <div className="controls">
          <label>
            Train label source
            <select value={source} onChange={(e) => setSource(e.target.value)}>
              <option value="implicit">implicit</option>
              <option value="judge">judge</option>
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
          <button className="primary" onClick={onFit} disabled={busy}>
            Fit routers
          </button>
          <span className="muted">{fitStatus}</span>
        </div>
      </div>

      <h3>Routing methods</h3>
      <div className="methods">
        {routers.map((r) => (
          <div key={r.name} className="method">
            <div className="mhead">
              <span className="rname">{r.name}</span>
              <span className={"chip kind-" + r.kind}>{r.kind}</span>
            </div>
            <div className="muted small">{r.description}</div>
          </div>
        ))}
      </div>

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
        <p className="muted small">
          {fit?.error ? fit.error : "Fit the routers to see IRT ability recovery."}
        </p>
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
