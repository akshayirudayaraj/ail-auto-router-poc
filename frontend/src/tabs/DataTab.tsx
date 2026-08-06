import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api, type AgenticRow, type LabelBranch, type SessionTrace } from "../api";
import { fmt, taskOf } from "../format";
import { useConsole } from "../store";
import { ModelChip } from "../components/chips";
import { ChatView } from "../components/ChatView";

type Cat = "internal" | "semi" | "synth";

const CAT_META: Record<
  Cat,
  { title: string; desc: string; waiting?: boolean; match?: (r: AgenticRow) => boolean }
> = {
  internal: {
    title: "Internal logs (Source 1)",
    desc: "Real Claude Code session logs from production/internal use — real users, real tasks, and (crucially) real multi-turn <b>user reactions</b> the implicit miner reads. They carry <b>no hidden-test oracle</b>, so outcomes come from the weak sources: <b>implicit</b> (behavior heuristics) + <b>judge</b> (frontier-as-judge over a distilled evidence pack). <b>Currently one illustrative sample</b> wiring this path end-to-end; replace with real logs as they land.",
    match: (r) => String(r.source) === "internal_usage",
  },
  semi: {
    title: "Semi-synthetic (Source 2 · SWE-bench Verified)",
    desc: "Real GitHub issues + repositories + hidden test suites (SWE-bench Verified). Nothing is generated except instance selection; each model rung runs an agentic session in the real per-instance container, and outcomes are graded by <b>executing the hidden tests</b> — the strongest, non-circular label.",
    match: (r) => String(r.source) === "swe_verified",
  },
  synth: {
    title: "Synthetic (Source 3 · fully generated)",
    desc: "The task + repo/harness/oracle are generated (by Opus), then each model rung runs an agentic session — outcomes come from <b>execution</b> where an oracle exists, else the offline LLM-judge over a distilled evidence pack. Never templated. (Includes the curated easy/med/hard warm-up tasks.)",
    match: (r) => String(r.source) !== "swe_verified" && String(r.source) !== "internal_usage",
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
  // The selected session lives in the URL (/data/:sessionId) so a trace is
  // deep-linkable and the browser Back button works. DataTab renders for both
  // /data and /data/:sessionId, so `cat` survives navigating into a trace.
  const navigate = useNavigate();
  const { sessionId } = useParams();
  const selected = sessionId ? decodeURIComponent(sessionId) : null;
  const openSession = (id: string) => navigate("/data/" + encodeURIComponent(id));

  const meta = CAT_META[cat];
  // Only surface rows that resolve to a real trace — legacy records with an
  // empty session_id can't be opened (the trace endpoint 400s on a missing id),
  // so hiding them keeps every visible row clickable.
  const rows = (meta.match ? corpus.filter(meta.match) : []).filter((r) => r.session_id);

  const reasoningFor = (row: AgenticRow): string => {
    const lab = labels[row.session_id];
    if (!lab) return "";
    const src = row.label_src || undefined;
    const rec: LabelBranch | undefined = (src && lab[src]) || lab.resolved;
    return evidenceBlurb(src || rec?.source, rec?.evidence);
  };

  // Detail "page": clicking a row swaps the list out for the full chat trace
  // with a Back breadcrumb (below the top navbar), rather than an inline panel.
  // A dual-arm task has both a local and a frontier session; the arm switcher
  // flips between the two traces for the same task.
  if (selected) {
    const task = taskOf(selected);
    const armSid: Record<string, string> = {};
    corpus.forEach((s) => {
      if (s.session_id && s.arm && taskOf(s.session_id) === task && !armSid[s.arm]) armSid[s.arm] = s.session_id;
    });
    const curArm = corpus.find((s) => s.session_id === selected)?.arm || selected.split("__").slice(-2)[0] || "";
    if (curArm && !armSid[curArm]) armSid[curArm] = selected;
    const armOrder = ["local", "frontier"].filter((a) => armSid[a]);
    return (
      <section className="tab active">
        <div className="crumb">
          <button className="back" onClick={() => navigate("/data")}>
            ← Back
          </button>
          <span className="crumb-sid">
            <code>{task}</code>
          </span>
          {armOrder.length > 1 && (
            <select
              className="arm-switch"
              value={curArm}
              onChange={(e) => {
                const sid = armSid[e.target.value];
                if (sid) openSession(sid);
              }}
              title="Switch between the local and frontier arm of this task"
            >
              {armOrder.map((a) => (
                <option key={a} value={a}>
                  {a} arm
                </option>
              ))}
            </select>
          )}
        </div>
        <div id="data-detail">
          <SessionDetail sid={selected} />
        </div>
      </section>
    );
  }

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
                      onClick={() => openSession(row.session_id)}
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

      <h3>Conversation</h3>
      <ChatView turns={data.turns || []} events={data.events || []} model={rec.served_model} />

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

