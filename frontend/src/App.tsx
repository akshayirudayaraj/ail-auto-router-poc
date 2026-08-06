import { useState } from "react";
import { useConsole } from "./store";
import { DataTab } from "./tabs/DataTab";
import { TrainingTab } from "./tabs/TrainingTab";
import { EvalsTab } from "./tabs/EvalsTab";
import { RouteTab } from "./tabs/RouteTab";

type TabId = "data" | "training" | "evals" | "route";
const TABS: { id: TabId; label: string }[] = [
  { id: "data", label: "Data" },
  { id: "training", label: "Training" },
  { id: "evals", label: "Evals" },
  { id: "route", label: "Route" },
];

function StatBar() {
  const { summary } = useConsole();
  if (!summary) return <div className="statbar" />;
  const c = summary.counts || {};
  const stat = (k: string, v: React.ReactNode) => (
    <span className="stat">
      <b>{v}</b> {k}
    </span>
  );
  return (
    <div className="statbar">
      {stat("local", (summary.local_models || []).join(" · ") || "—")}
      {stat("frontier", summary.frontier_model || "—")}
      {stat("pointwise", (c.pointwise_implicit ?? 0) + (c.pointwise_judge ?? 0))}
      {stat("pairwise", c.pairwise ?? 0)}
      {stat("gold", c.gold ?? 0)}
    </div>
  );
}

export function App() {
  const { summary } = useConsole();
  const [tab, setTab] = useState<TabId>("data");

  const pill = summary
    ? `anthropic: ${summary.anthropic ? "live" : "off"} · embed: ${summary.embed_model}`
    : "…";

  return (
    <>
      <header>
        <div className="brand">
          <span className="logo">⇌</span>
          <div>
            <h1>ail-routing-test</h1>
            <p className="sub">predictive auto-router · console</p>
          </div>
        </div>
        <nav id="tabs">
          {TABS.map((t) => (
            <button key={t.id} className={tab === t.id ? "active" : ""} onClick={() => setTab(t.id)}>
              {t.label}
            </button>
          ))}
        </nav>
        <div id="backend-pill" className="pill">
          {pill}
        </div>
      </header>

      <StatBar />

      <main>
        {/* Each tab stays mounted-on-demand; switching preserves per-tab state
            for the session (fit results, selected row) via the shared store. */}
        {tab === "data" && <DataTab />}
        {tab === "training" && <TrainingTab />}
        {tab === "evals" && <EvalsTab />}
        {tab === "route" && <RouteTab />}
      </main>
    </>
  );
}
