import { NavLink, Navigate, Route, Routes } from "react-router-dom";
import { DataTab } from "./tabs/DataTab";
import { TrainingTab } from "./tabs/TrainingTab";
import { EvalsTab } from "./tabs/EvalsTab";
import { RouteTab } from "./tabs/RouteTab";

const TABS: { to: string; label: string }[] = [
  { to: "/data", label: "Data" },
  { to: "/training", label: "Training" },
  { to: "/evals", label: "Evals" },
  { to: "/route", label: "Route" },
];

export function App() {
  return (
    <>
      <header>
        <div className="brand">
          <span className="logo">⇌</span>
          <div>
            <h1>AIL Auto Router POC</h1>
            <p className="sub">predictive auto-router · console</p>
          </div>
        </div>
        <nav id="tabs">
          {TABS.map((t) => (
            <NavLink key={t.to} to={t.to} className={({ isActive }) => (isActive ? "active" : "")}>
              {t.label}
            </NavLink>
          ))}
        </nav>
      </header>

      <main>
        {/* Path-based routes. The shared store (ConsoleProvider, above this) keeps
            fit/corpus cached across navigation. The Data list and its deep-linkable
            session-trace detail both render <DataTab/>, which switches on the
            :sessionId param — so the category subtab state survives Back. */}
        <Routes>
          <Route path="/" element={<Navigate to="/data" replace />} />
          <Route path="/data" element={<DataTab />} />
          <Route path="/data/:sessionId" element={<DataTab />} />
          <Route path="/training" element={<TrainingTab />} />
          <Route path="/evals" element={<EvalsTab />} />
          <Route path="/route" element={<RouteTab />} />
          <Route path="*" element={<Navigate to="/data" replace />} />
        </Routes>
      </main>
    </>
  );
}
