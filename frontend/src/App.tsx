import { useState } from "react";
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

export function App() {
  const [tab, setTab] = useState<TabId>("data");

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
      </header>

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
