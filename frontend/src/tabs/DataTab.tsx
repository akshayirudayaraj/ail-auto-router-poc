import { useEffect, useState } from "react";
import { api, type AgenticRow, type LabelBranch, type SessionTrace } from "../api";
import { fmt } from "../format";
import { useConsole } from "../store";
import { ModelChip } from "../components/chips";

type Cat = "internal" | "semi" | "synth";

const CAT_META: Record<
  Cat,
  { title: string; desc: string; waiting?: boolean; match?: (r: AgenticRow) => boolean }
> = {
  internal: {
    title: "Internal logs (Source 1)",
    desc: "Real Claude Code session logs from production/internal use. <b>Not yet wired in</b> — waiting on real logs. Once available these are the strongest signal (real users, real tasks) and feed the router directly.",
    waiting: true,
  },
  semi: {
    title: "Semi-synthetic (Source 2 · SWE-bench Verified)",
    desc: "Real GitHub issues + repositories + hidden test suites (SWE-bench Verified). Nothing is generated except instance selection; each model rung runs an agentic session in the real per-instance container, and outcomes are graded by <b>executing the hidden tests</b> — the strongest, non-circular label.",
    match: (r) => String(r.source) === "swe_verified",
  },
  synth: {
    title: "Synthetic (Source 3 · fully generated)",
    desc: "The task + repo/harness/oracle are generated (by Opus), then each model rung runs an agentic session — outcomes come from <b>execution</b> where an oracle exists, else the offline LLM-judge over a distilled evidence pack. Never templated. (Includes the curated easy/med/hard warm-up tasks.)",
    match: (r) => String(r.source) !== "swe_verified",
  },
};

const DATA_COLS: [string, string][] = [
  ["session", "wrap"],
  ["model", ""],
  ["split", ""],
  ["method", ""],
  ["outcome", ""],
  ["conf", "num"],
  ["reasoning", "wrap muted"],
];

function srcClass(s?: string | null) {
  return s === "executed" ? "ok" : s === "judge" ? "" : "warn";
}

function evidenceBlurb(src?: string, ev?: any): string {
  if (!ev) return "";
  if (src === "executed")
    return ev.note || (ev.fail_to_pass_ok != null ? `tests: F2P ${ev.fail_to_pass_ok} · P2P ${ev.pass_to_pass_ok}` : "");
  if (src === "judge") return (ev.rationale || "").slice(0, 160) + (ev.k_votes > 1 ? ` (k=${ev.k_votes})` : "");
  if (src === "implicit") return `signal: ${ev.signal || "?"}${ev.had_user_reaction === false ? " (weak default)" : ""}`;
  if (ev.rule) return `rule: ${ev.rule}${ev.disagreement_flag ? " · ⚠ judge/heuristic disagreed" : ""}`;
  return "";
}

export function DataTab() {
  const { corpus, labels, corpusLoaded } = useConsole();
  const [cat, setCat] = useState<Cat>("semi");
  const [selected, setSelected] = useState<string | null>(null);

  const meta = CAT_META[cat];
  const rows = meta.match ? corpus.filter(meta.match) : [];

  const reasoningFor = (row: AgenticRow): string => {
    const lab = labels[row.session_id];
    if (!lab) return "";
    const src = row.label_src || undefined;
    const rec: LabelBranch | undefined = (src && lab[src]) || lab.resolved;
    return evidenceBlurb(src || rec?.source, rec?.evidence);
  };

  return (
    <section className="tab active">
      <div className="subtabs">
        {(Object.keys(CAT_META) as Cat[]).map((c) => (
          <button key={c} className={cat === c ? "active" : ""} onClick={() => setCat(c)}>
            {c === "internal" ? "Internal logs" : c === "semi" ? "Semi-synthetic" : "Synthetic"}
          </button>
        ))}
      </div>

      <div className="note" dangerouslySetInnerHTML={{ __html: `<b>${meta.title}.</b> ${meta.desc}` }} />

      {meta.waiting ? (
        <div className="toolbar">
          <div className="empty">⏳ Waiting on real logs — no internal Claude Code sessions ingested yet.</div>
        </div>
      ) : (
        <>
          <div className="toolbar">
            <span className="muted">{corpusLoaded ? `${rows.length} sessions` : "loading…"}</span>
            {corpusLoaded && !rows.length && (
              <span className="muted">
                {" "}
                · none yet — run `make agentic-generate` / `make agentic-swe`, then the offline label engine.
              </span>
            )}
          </div>

          {rows.length > 0 && (
            <div className="tablewrap">
              <table id="data-table">
                <thead>
                  <tr>
                    {DATA_COLS.map(([c, cls]) => (
                      <th key={c} className={cls}>
                        {c}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row) => (
                    <tr
                      key={row.session_id}
                      className={"clickable" + (selected === row.session_id ? " active" : "")}
                      onClick={() => setSelected(row.session_id)}
                    >
                      <td className="wrap">
                        <code>{row.session_id}</code>
                      </td>
                      <td>
                        <ModelChip model={row.served_model} />
                      </td>
                      <td>
                        <span className={"chip " + (row.split === "holdout" ? "warn" : "")}>{row.split || "—"}</span>
                      </td>
                      <td>
                        {row.label_src == null ? (
                          <span className="muted">unlabeled</span>
                        ) : (
                          <span className={"chip " + srcClass(row.label_src)}>{row.label_src}</span>
                        )}
                      </td>
                      <td>
                        {row.outcome == null ? (
                          <span className="muted">—</span>
                        ) : (
                          <span className={"chip " + (row.outcome === 1 ? "ok" : "bad")}>
                            {row.outcome === 1 ? "adequate" : "inadequate"}
                          </span>
                        )}
                      </td>
                      <td className="num">{row.conf == null ? "" : fmt(row.conf)}</td>
                      <td className="wrap muted">{reasoningFor(row)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}

      <div id="data-detail">
        {selected ? (
          <SessionDetail sid={selected} />
        ) : (
          <p className="muted">Select a session row to reconstruct its full trace (turns, tool calls, labels, diff).</p>
        )}
      </div>
    </section>
  );
}

function SessionDetail({ sid }: { sid: string }) {
  const { labels } = useConsole();
  const [reveal, setReveal] = useState(false);
  const [data, setData] = useState<SessionTrace | null>(null);
  const [loading, setLoading] = useState(true);

  // Refetch the trace whenever the session or the reveal flag changes.
  useEffect(() => {
    let alive = true;
    setLoading(true);
    setData(null);
    api<SessionTrace>("/api/agentic/session?id=" + encodeURIComponent(sid) + (reveal ? "&reveal=1" : "")).then((r) => {
      if (!alive) return;
      setData(r);
      setLoading(false);
    });
    return () => {
      alive = false;
    };
  }, [sid, reveal]);

  if (loading) return <p className="muted">loading {sid} …</p>;
  if (!data || data.error) return <p className="muted">{data?.error || "no data"}</p>;

  const rec = data.record || {};
  const lab = labels[sid];

  const headChips: [string, unknown][] = [
    ["prov", rec.provenance],
    ["split", rec.split],
    ["turns", rec.num_turns],
    ["native/rescued", `${rec.native_tool_calls}/${rec.rescued_tool_calls}`],
    ["tok", rec.total_tokens],
    ["wall", (rec.wall_clock_s ?? "—") + "s"],
  ];

  return (
    <>
      <div className="head" style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <span className="role">
          {rec.task_id} · {rec.arm}
        </span>
        <ModelChip model={rec.served_model} />
        {headChips.map(([k, v]) => (
          <span key={k} className="chip">
            {k}: {v == null ? "—" : String(v)}
          </span>
        ))}
        {rec.timed_out && <span className="chip bad">timed_out</span>}
        {rec.empty_patch && <span className="chip bad">empty_patch</span>}
      </div>

      {lab && (
        <>
          <h3>Labels (offline engine)</h3>
          <div className="panel">
            {["executed", "judge", "implicit", "resolved"].map((src) => {
              const v = lab[src];
              if (!v) return null;
              return (
                <div key={src} className="router-row">
                  <span className="rname">{src + (src === "resolved" ? " (canonical)" : "")}</span>
                  <span>
                    <span className={"chip " + (v.outcome === 1 ? "ok" : "bad")}>
                      {v.outcome === 1 ? "adequate" : "inadequate"}
                    </span>
                  </span>
                  <span className="num mono">{v.confidence == null ? "" : "conf " + fmt(v.confidence)}</span>
                  <span className="muted small">{evidenceBlurb(src, v.evidence)}</span>
                </div>
              );
            })}
          </div>
        </>
      )}

      {data.issue && (
        <details open>
          <summary>ISSUE</summary>
          <pre className="content">{data.issue}</pre>
        </details>
      )}

      <h3>Session (reconstructed turns)</h3>
      {(data.turns || []).map((t, i) => (
        <div key={i} className={"turn " + t.role}>
          <div className="head">
            <span className="role">{t.role}</span>
            {t.served_model && <ModelChip model={t.served_model} />}
          </div>
          <div className="content">{t.content || ""}</div>
        </div>
      ))}

      <h3>Tool trace (CC events)</h3>
      <div className="panel">
        <ToolTrace events={data.events || []} />
      </div>

      <h3>Patch (git diff)</h3>
      <pre className="content mono">{data.patch || "(empty)"}</pre>

      {data.oracle && Object.keys(data.oracle).length ? (
        <>
          <h3 className="warn-text">⚠ Oracle (hidden ground truth)</h3>
          {Object.entries(data.oracle).map(([k, v]) => (
            <details key={k}>
              <summary>{k}</summary>
              <pre className="content mono">{v}</pre>
            </details>
          ))}
        </>
      ) : (
        <button className="reveal" onClick={() => setReveal(true)}>
          Reveal oracle (test_patch + gold_patch)
        </button>
      )}
    </>
  );
}

function ToolTrace({ events }: { events: any[] }) {
  const out: React.ReactNode[] = [];
  events.forEach((e, ei) => {
    if (e.type === "assistant") {
      (e.message?.content || []).forEach((b: any, bi: number) => {
        if (b.type === "text" && b.text)
          out.push(
            <div key={`${ei}-${bi}`} className="turn assistant">
              <div className="content">{b.text}</div>
            </div>,
          );
        if (b.type === "tool_use")
          out.push(
            <div key={`${ei}-${bi}`} className="toolcall">
              <span className="chip">→ {b.name}</span>
              <span className="content mono">{JSON.stringify(b.input).slice(0, 300)}</span>
            </div>,
          );
      });
    } else if (e.type === "user") {
      (e.message?.content || []).forEach((b: any, bi: number) => {
        if (b.type === "tool_result") {
          let c = b.content;
          if (Array.isArray(c)) c = c.map((x: any) => x.text || "").join(" ");
          out.push(
            <div key={`${ei}-${bi}`} className="toolcall">
              <span className={"chip " + (b.is_error ? "bad" : "ok")}>{b.is_error ? "✗ result" : "✓ result"}</span>
              <span className="content mono">{String(c || "").slice(0, 300)}</span>
            </div>,
          );
        }
      });
    }
  });
  return <>{out}</>;
}
