"use strict";
const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const el = (tag, attrs = {}, ...kids) => {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") n.className = v;
    else if (k === "html") n.innerHTML = v;
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v);
  }
  for (const kid of kids) n.append(kid?.nodeType ? kid : document.createTextNode(kid ?? ""));
  return n;
};
const SVGNS = "http://www.w3.org/2000/svg";
const svg = (tag, attrs = {}, ...kids) => {
  const n = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
  for (const kid of kids) n.append(kid?.nodeType ? kid : document.createTextNode(kid ?? ""));
  return n;
};
const api = async (path, opts) => (await fetch(path, opts)).json();
const fmt = (x) => (typeof x === "number" ? (Number.isInteger(x) ? x : x.toFixed(3)) : x);
const signed = (x) => (x >= 0 ? "+" : "") + Number(x).toFixed(2);

let STATE = { summary: null, localSet: new Set(), frontier: "", routers: [], fit: null, modelArm: {} };
// isLocal prefers the AUTHORITATIVE arm seen in the run records (modelArm, built
// from the corpus) over the config roster — the roster can be stale/misresolved on
// old data dirs, but a session's arm is ground truth. This keeps opus (frontier)
// and gpt-oss:20b (local) reliably distinct-colored in the Data view.
function isLocal(m) {
  if (m in STATE.modelArm) return STATE.modelArm[m] === "local";
  return STATE.localSet.has(m);
}
function modelChip(m) {
  if (!m) return el("span", { class: "muted" }, "—");
  return el("span", { class: "chip " + (isLocal(m) ? "model-local" : "model-frontier") }, m);
}

// ---- tabs ----
$$("#tabs button").forEach((b) =>
  b.addEventListener("click", () => {
    $$("#tabs button").forEach((x) => x.classList.remove("active"));
    b.classList.add("active");
    $$("main > .tab").forEach((t) => t.classList.remove("active"));
    $("#" + b.dataset.tab).classList.add("active");
    if (b.dataset.tab === "data" && !corpusLoaded) loadCorpus();
    if (b.dataset.tab === "training") ensureFit().then(renderTraining);
    if (b.dataset.tab === "evals") ensureFit().then(renderEvals);
  })
);

// =====================================================================
// DATA — reconstructed sessions by source category
// =====================================================================
let corpusLoaded = false, corpusRows = [], corpusLabels = {}, curCat = "semi";
const CAT_META = {
  internal: {
    title: "Internal logs (Source 1)",
    desc: "Real Claude Code session logs from production/internal use. <b>Not yet wired in</b> — waiting on real logs. Once available these are the strongest signal (real users, real tasks) and feed the router directly.",
    waiting: true, sources: [],
  },
  semi: {
    title: "Semi-synthetic (Source 2 · SWE-bench Verified)",
    desc: "Real GitHub issues + repositories + hidden test suites (SWE-bench Verified). Nothing is generated except instance selection; each model rung runs an agentic session in the real per-instance container, and outcomes are graded by <b>executing the hidden tests</b> — the strongest, non-circular label.",
    match: (row) => String(row.source) === "swe_verified",
  },
  synth: {
    title: "Synthetic (Source 3 · fully generated)",
    desc: "The task + repo/harness/oracle are generated (by Opus), then each model rung runs an agentic session — outcomes come from <b>execution</b> where an oracle exists, else the offline LLM-judge over a distilled evidence pack. Never templated. (Includes the curated easy/med/hard warm-up tasks.)",
    match: (row) => String(row.source) !== "swe_verified",
  },
};
const DATA_COLS = [
  ["session", "wrap"], ["model", ""], ["split", ""],
  ["method", ""], ["outcome", ""], ["conf", "num"], ["reasoning", "wrap muted"],
];

$$("#data-cats button").forEach((b) =>
  b.addEventListener("click", () => {
    $$("#data-cats button").forEach((x) => x.classList.remove("active"));
    b.classList.add("active");
    curCat = b.dataset.cat;
    renderData();
  })
);

async function loadCorpus() {
  corpusLoaded = true;
  const [r, lb] = await Promise.all([api("/api/agentic"), api("/api/labels")]);
  corpusRows = r.rows || [];
  corpusLabels = lb.by_session || {};
  // Authoritative model→arm map from the run records (drives chip colors).
  corpusRows.forEach((row) => {
    if (row.served_model && row.arm) STATE.modelArm[row.served_model] = row.arm;
  });
  renderData();
}

function catRows(cat) {
  const m = CAT_META[cat];
  return m.match ? corpusRows.filter(m.match) : [];
}

function labelMethod(row) {
  return row.label_src || "—";
}
function reasoningFor(row) {
  const lab = corpusLabels[row.session_id];
  if (!lab) return "";
  // prefer the canonical (resolved) evidence, else the branch that produced it
  const src = row.label_src;
  const rec = (src && lab[src]) || lab.resolved;
  return evidenceBlurb(src || (rec && rec.source), rec && rec.evidence);
}

function renderData() {
  const m = CAT_META[curCat];
  $("#data-desc").innerHTML = `<b>${m.title}.</b> ${m.desc}`;
  const tb = $("#data-toolbar"); tb.innerHTML = "";
  const t = $("#data-table"); t.innerHTML = "";
  $("#data-detail").innerHTML = `<p class="muted">Select a session row to reconstruct its full trace (turns, tool calls, labels, diff).</p>`;

  if (m.waiting) {
    t.innerHTML = "";
    tb.append(el("div", { class: "empty" }, "⏳ Waiting on real logs — no internal Claude Code sessions ingested yet."));
    return;
  }
  const rows = catRows(curCat);
  tb.append(el("span", { class: "muted" }, `${rows.length} sessions`));
  if (!rows.length) {
    tb.append(el("span", { class: "muted" }, " · none yet — run `make agentic-generate` / `make agentic-swe`, then the offline label engine."));
    return;
  }
  const thead = el("tr");
  DATA_COLS.forEach(([c, cls]) => thead.append(el("th", { class: cls }, c)));
  t.append(el("thead", {}, thead));
  const body = el("tbody");
  rows.forEach((row) => {
    const tr = el("tr", { class: "clickable", onclick: () => selectSession(row.session_id, false, tr) });
    DATA_COLS.forEach(([c, cls]) => {
      if (c === "session") return tr.append(el("td", { class: cls }, el("code", {}, row.session_id)));
      if (c === "model") return tr.append(el("td", {}, modelChip(row.served_model)));
      if (c === "split") return tr.append(el("td", {}, el("span", { class: "chip " + (row.split === "holdout" ? "warn" : "") }, row.split || "—")));
      if (c === "method") {
        if (row.label_src == null) return tr.append(el("td", {}, el("span", { class: "muted" }, "unlabeled")));
        return tr.append(el("td", {}, el("span", { class: "chip " + srcClass(row.label_src) }, labelMethod(row))));
      }
      if (c === "outcome")
        return tr.append(el("td", {}, row.outcome == null ? el("span", { class: "muted" }, "—")
          : el("span", { class: "chip " + (row.outcome === 1 ? "ok" : "bad") }, row.outcome === 1 ? "adequate" : "inadequate")));
      if (c === "conf") return tr.append(el("td", { class: cls }, row.conf == null ? "" : fmt(row.conf)));
      if (c === "reasoning") return tr.append(el("td", { class: cls }, reasoningFor(row)));
    });
    body.append(tr);
  });
  t.append(body);
}

function srcClass(s) { return s === "executed" ? "ok" : s === "judge" ? "" : "warn"; }
function evidenceBlurb(src, ev) {
  if (!ev) return "";
  if (src === "executed") return ev.note || (ev.fail_to_pass_ok != null ? `tests: F2P ${ev.fail_to_pass_ok} · P2P ${ev.pass_to_pass_ok}` : "");
  if (src === "judge") return (ev.rationale || "").slice(0, 160) + (ev.k_votes > 1 ? ` (k=${ev.k_votes})` : "");
  if (src === "implicit") return `signal: ${ev.signal || "?"}${ev.had_user_reaction === false ? " (weak default)" : ""}`;
  if (ev.rule) return `rule: ${ev.rule}${ev.disagreement_flag ? " · ⚠ judge/heuristic disagreed" : ""}`;
  return "";
}

// ---- session trace drill-in (shared by Data) ----
async function selectSession(sid, reveal, tr) {
  if (tr) { $$("#data-table tr").forEach((x) => x.classList.remove("active")); tr.classList.add("active"); }
  const d = $("#data-detail"); d.innerHTML = "";
  d.append(el("p", { class: "muted" }, "loading " + sid + " …"));
  const r = await api("/api/agentic/session?id=" + encodeURIComponent(sid) + (reveal ? "&reveal=1" : ""));
  d.innerHTML = "";
  if (r.error) { d.append(el("p", { class: "muted" }, r.error)); return; }
  const rec = r.record || {};
  const head = el("div", { class: "head" },
    el("span", { class: "role" }, rec.task_id + " · " + rec.arm), modelChip(rec.served_model));
  [["prov", rec.provenance], ["split", rec.split], ["turns", rec.num_turns],
   ["native/rescued", `${rec.native_tool_calls}/${rec.rescued_tool_calls}`],
   ["tok", rec.total_tokens], ["wall", (rec.wall_clock_s ?? "—") + "s"]].forEach(([k, v]) =>
    head.append(el("span", { class: "chip" }, k + ": " + (v == null ? "—" : v))));
  if (rec.timed_out) head.append(el("span", { class: "chip bad" }, "timed_out"));
  if (rec.empty_patch) head.append(el("span", { class: "chip bad" }, "empty_patch"));
  d.append(head);

  const lab = corpusLabels[sid];
  if (lab) {
    d.append(el("h3", {}, "Labels (offline engine)"));
    const lt = el("div", { class: "panel" });
    ["executed", "judge", "implicit", "resolved"].forEach((src) => {
      const v = lab[src]; if (!v) return;
      lt.append(el("div", { class: "router-row" },
        el("span", { class: "rname" }, src + (src === "resolved" ? " (canonical)" : "")),
        el("span", {}, el("span", { class: "chip " + (v.outcome === 1 ? "ok" : "bad") }, v.outcome === 1 ? "adequate" : "inadequate")),
        el("span", { class: "num mono" }, v.confidence == null ? "" : "conf " + fmt(v.confidence)),
        el("span", { class: "muted small" }, evidenceBlurb(src, v.evidence))));
    });
    d.append(lt);
  }

  if (r.issue) d.append(el("details", { open: "" }, el("summary", {}, "ISSUE"), el("pre", { class: "content" }, r.issue)));

  d.append(el("h3", {}, "Session (reconstructed turns)"));
  (r.turns || []).forEach((t) => {
    const h = el("div", { class: "head" }, el("span", { class: "role" }, t.role));
    if (t.served_model) h.append(modelChip(t.served_model));
    d.append(el("div", { class: "turn " + t.role }, h, el("div", { class: "content" }, t.content || "")));
  });

  d.append(el("h3", {}, "Tool trace (CC events)"));
  const ev = el("div", { class: "panel" });
  (r.events || []).forEach((e) => {
    if (e.type === "assistant") {
      (e.message?.content || []).forEach((b) => {
        if (b.type === "text" && b.text) ev.append(el("div", { class: "turn assistant" }, el("div", { class: "content" }, b.text)));
        if (b.type === "tool_use") ev.append(el("div", { class: "toolcall" },
          el("span", { class: "chip" }, "→ " + b.name),
          el("span", { class: "content mono" }, JSON.stringify(b.input).slice(0, 300))));
      });
    } else if (e.type === "user") {
      (e.message?.content || []).forEach((b) => {
        if (b.type === "tool_result") {
          let c = b.content;
          if (Array.isArray(c)) c = c.map((x) => x.text || "").join(" ");
          ev.append(el("div", { class: "toolcall" },
            el("span", { class: "chip " + (b.is_error ? "bad" : "ok") }, b.is_error ? "✗ result" : "✓ result"),
            el("span", { class: "content mono" }, String(c || "").slice(0, 300))));
        }
      });
    }
  });
  d.append(ev);

  d.append(el("h3", {}, "Patch (git diff)"));
  d.append(el("pre", { class: "content mono" }, r.patch || "(empty)"));

  if (Object.keys(r.oracle || {}).length) {
    d.append(el("h3", { class: "warn-text" }, "⚠ Oracle (hidden ground truth)"));
    for (const [k, v] of Object.entries(r.oracle)) d.append(el("details", {}, el("summary", {}, k), el("pre", { class: "content mono" }, v)));
  } else {
    d.append(el("button", { class: "reveal", onclick: () => selectSession(sid, true) }, "Reveal oracle (test_patch + gold_patch)"));
  }
}

// =====================================================================
// TRAINING + EVALS (shared fit)
// =====================================================================
$("#fit-run").addEventListener("click", async () => {
  const btn = $("#fit-run"); btn.disabled = true; $("#fit-status").textContent = "fitting…";
  STATE.fit = await api("/api/fit", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ train_source: $("#fit-source").value, threshold: parseFloat($("#fit-threshold").value) }),
  });
  btn.disabled = false;
  if (STATE.fit.error) { $("#fit-status").textContent = STATE.fit.error; return; }
  $("#fit-status").textContent = `fit ${STATE.fit.n_pointwise} pointwise / ${STATE.fit.n_pairwise} pairwise (source: ${STATE.fit.train_source})`;
  renderTraining(); renderEvals();
});

async function ensureFit() {
  if (STATE.fit && !STATE.fit.error) return STATE.fit;
  STATE.fit = await api("/api/fit", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ train_source: $("#fit-source").value, threshold: parseFloat($("#fit-threshold").value) }),
  });
  return STATE.fit;
}

function renderTraining() {
  // methods
  const mbox = $("#training-methods"); mbox.innerHTML = "";
  STATE.routers.forEach((r) => {
    mbox.append(el("div", { class: "method" },
      el("div", { class: "mhead" }, el("span", { class: "rname" }, r.name), el("span", { class: "chip kind-" + r.kind }, r.kind)),
      el("div", { class: "muted small" }, r.description)));
  });

  const r = STATE.fit;
  const at = $("#training-abilities"); at.innerHTML = "";
  const chart = $("#training-ability-chart"); chart.innerHTML = "";
  if (!r || r.error || !(r.abilities || []).length) {
    $("#training-note").textContent = r && r.error ? r.error : "Fit the routers to see IRT ability recovery.";
    return;
  }
  at.append(el("thead", {}, thr(["model", "planted θ", "recovered θ"], ["", "num", "num"])));
  const ab = el("tbody");
  r.abilities.forEach((a) => ab.append(el("tr", {},
    el("td", {}, modelChip(a.model)),
    el("td", { class: "num" }, a.planted == null ? "—" : signed(a.planted)),
    el("td", { class: "num" }, signed(a.recovered)))));
  at.append(ab);
  chart.append(svgBars(r.abilities.map((a) => ({ label: a.model, value: a.recovered })), { diverging: true, fmt: signed }));
  $("#training-note").innerHTML = "θ is reference-centered (local rung = 0). Only the <b>ordering and sign</b> of the gaps matter for routing; magnitudes compress under noisy labels.";
}

function renderEvals() {
  const r = STATE.fit;
  const empty = $("#evals-empty"); empty.innerHTML = "";
  const lt = $("#evals-leaderboard"); lt.innerHTML = "";
  const chart = $("#evals-aiq-chart"); chart.innerHTML = "";
  const legend = $("#evals-legend"); legend.textContent = "";

  $("#evals-dist").innerHTML = "";
  if (!r || r.error || !r.has_gold || !(r.leaderboard || []).length) {
    empty.innerHTML = "No dual-arm <b>gold</b> set yet (executed holdout not populated). Absolute cost/quality numbers appear once grading runs and <code>make agentic-materialize</code> writes gold rows. The harness methods below still describe what will be measured.";
    renderEvalMethods();
    return;
  }
  const cols = ["aiq", "auc", "ece", "escalation@thr", "quality@thr", "qual_retention", "cost_vs_local", "under_escal_cellB", "over_escalation"];
  lt.append(el("thead", {}, thr(["router", ...cols], ["", ...cols.map(() => "num")])));
  const body = el("tbody");
  let best = -1; r.leaderboard.forEach((x) => best = Math.max(best, x.metrics.aiq || 0));
  r.leaderboard.forEach((row) => {
    const tr = el("tr", {}, el("td", { class: "rname" }, row.router));
    cols.forEach((m) => {
      const v = row.metrics[m];
      const td = el("td", { class: "num" }, v == null ? "" : v.toFixed(3));
      if (m === "aiq" && v > 0 && Math.abs(v - best) < 1e-9) { td.style.fontWeight = "700"; td.style.color = "var(--good)"; }
      tr.append(td);
    });
    body.append(tr);
  });
  lt.append(body);
  chart.append(svgBars(r.leaderboard.map((x) => ({ label: x.router, value: x.metrics.aiq || 0 })), { fmt: (v) => v.toFixed(3), color: "var(--accent)" }));
  legend.innerHTML = "AIQ = area under the cost/quality hull (higher = more quality per unit cost; best highlighted). " +
    "under_escal_cellB = stayed local but frontier would have passed (the costly miss). Only these gold numbers are absolute.";
  renderRoutingDist(r);
  renderEvalMethods();
}

// renderRoutingDist: for each router, what fraction of gold prompts it sends to
// LOCAL vs FRONTIER (@ the operating threshold), alongside the quality it retains
// vs always-frontier. The win condition: match always-frontier's quality
// (qual_retention ≈ 1.0) while keeping a HIGH share local (low escalation = low cost).
function renderRoutingDist(r) {
  const box = $("#evals-dist"); box.innerHTML = "";
  const nGold = r.n_gold || 0;
  // order: always-local, learned routers (by ascending escalation), always-frontier
  const rows = [...r.leaderboard].sort((a, b) =>
    (a.metrics["escalation@thr"] ?? 0) - (b.metrics["escalation@thr"] ?? 0));

  rows.forEach((row) => {
    const esc = row.metrics["escalation@thr"] ?? 0;         // frontier fraction
    const qr = row.metrics["qual_retention"];               // vs always-frontier
    const froN = Math.round(esc * nGold), locN = nGold - froN;
    const locPct = Math.round((1 - esc) * 100), froPct = 100 - locPct;

    const stack = el("div", { class: "stack" });
    if (locPct > 0) stack.append(el("span", { class: "seg loc", style: `width:${locPct}%` }, locPct >= 12 ? `${locPct}%` : ""));
    if (froPct > 0) stack.append(el("span", { class: "seg fro", style: `width:${froPct}%` }, froPct >= 12 ? `${froPct}%` : ""));

    // quality-retention badge: green if it essentially matches always-frontier
    let qBadge = el("span", { class: "muted small" }, "—");
    if (qr != null) {
      const good = qr >= 0.98;
      qBadge = el("span", { class: "chip " + (good ? "ok" : qr >= 0.9 ? "warn" : "bad") }, "quality " + (qr * 100).toFixed(0) + "% of frontier");
    }
    box.append(el("div", { class: "dist-row" },
      el("span", { class: "rname" }, row.router),
      stack,
      el("span", { class: "dist-counts muted small" }, nGold ? `${locN} local · ${froN} frontier` : `${locPct}% local · ${froPct}% frontier`),
      qBadge));
  });

  box.append(el("div", { class: "dist-legend muted small" },
    el("span", { class: "swatch loc" }), " routed to local (gpt-oss:20b, cheap) ",
    el("span", { class: "swatch fro" }), " escalated to frontier (opus). ",
    "Ideal: a learned router near always-frontier's quality while keeping a high local share."));
}

const EVAL_METHODS = [
  ["dual-arm-gold", "Both arms' outcomes known → RouterBench-style cost/quality curve, AIQ, and escalation cells. The only trustworthy ABSOLUTE anchor."],
  ["temporal-backtest", "Splits logs by session+time and RANKS routers on held-out future. Enforces eval labels be a strictly-stronger source than train (no circularity)."],
  ["off-policy-ips-dr", "Estimates the reward of DEPLOYING each router from logs via IPS + doubly-robust. Refuses on deterministic logs (no propensities) — expected here."],
  ["guardrail-suite", "Matched perturbation probes: escalation must rise with difficulty and not flip on off-topic keyword injection."],
];
function renderEvalMethods() {
  const box = $("#evals-methods"); box.innerHTML = "";
  EVAL_METHODS.forEach(([n, d]) => box.append(el("div", { class: "method" },
    el("div", { class: "mhead" }, el("span", { class: "rname" }, n)),
    el("div", { class: "muted small" }, d))));
}

// =====================================================================
// ROUTE — live routing, selectable algorithm
// =====================================================================
const ROUTE_EXAMPLES = [
  "Reverse a string in Go.",
  "Add a --verbose flag to this CLI using the flag package.",
  "Implement a thread-safe LRU cache in Go with O(1) Get/Put using a map and a doubly linked list.",
  "Implement lock-free MPSC queue in Go with atomic CAS, ABA-safe, correct memory ordering, race-free.",
];
function initExamples() {
  const box = $("#route-examples");
  box.append(el("span", { class: "muted small" }, "try:"));
  ROUTE_EXAMPLES.forEach((ex) => box.append(el("span", { class: "ex", onclick: () => { $("#route-prompt").value = ex; } }, ex.slice(0, 42) + (ex.length > 42 ? "…" : ""))));
}
$("#route-run").addEventListener("click", runRoute);
async function runRoute() {
  const prompt = $("#route-prompt").value.trim();
  if (!prompt) { $("#route-status").textContent = "enter a prompt"; return; }
  const btn = $("#route-run"); btn.disabled = true; $("#route-status").textContent = "embedding + scoring…";
  const r = await api("/api/route", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ prompt, turn_type: $("#route-turntype").value, threshold: parseFloat($("#route-threshold").value) }),
  });
  btn.disabled = false;
  if (r.error) { $("#route-status").textContent = r.error; return; }
  $("#route-status").textContent = r.embedding_dim ? `embedded (${r.embedding_dim}-d)` : "no embedding (" + (r.embed_error || "offline") + ") — using feature priors";

  const pick = $("#route-router").value; // "" = all/majority
  const out = $("#route-result"); out.innerHTML = "";

  let verdict, sub;
  if (pick) {
    const one = r.routers.find((x) => x.name === pick);
    verdict = one ? (one.escalate ? "↑ escalate → frontier" : "→ stay local") : "router not found";
    sub = one ? `${pick} scored ${one.score.toFixed(3)} at threshold ${r.threshold}. Local = ${r.local_model}, frontier = ${r.frontier_model}.` : "";
  } else {
    const esc = r.escalate_votes, tot = r.total_routers, maj = esc > tot / 2;
    verdict = maj ? "↑ escalate → frontier" : "→ stay local";
    sub = `${esc}/${tot} routers vote escalate at threshold ${r.threshold}. Local = ${r.local_model}, frontier = ${r.frontier_model}.`;
  }
  out.append(el("div", { class: "verdict" }, el("div", { class: "big" }, verdict), el("div", { class: "muted" }, sub)));

  const table = el("div", { class: "panel" });
  table.append(el("div", { class: "router-row", html: "<b>router</b><b>escalation score</b><b style='text-align:right'>score</b><b style='text-align:right'>decision</b>" }));
  r.routers.forEach((rt) => {
    const bar = el("div", { class: "bar" }, el("span", { style: `width:${Math.round(rt.score * 100)}%` }));
    const row = el("div", { class: "router-row" + (pick === rt.name ? " picked" : "") },
      el("span", { class: "rname" }, rt.name), bar,
      el("span", { class: "num mono", style: "text-align:right" }, rt.score.toFixed(3)),
      el("span", { style: "text-align:right" }, el("span", { class: "chip " + (rt.escalate ? "warn" : "ok") }, rt.escalate ? "frontier" : "local")));
    table.append(row);
  });
  out.append(el("h3", {}, "Per-router decision"), table);

  out.append(el("h3", {}, "Prompt features ", el("span", { class: "muted" }, "(model-free, computed pre-generation)")));
  const fg = el("div", { class: "feat-grid" });
  r.features.forEach((f) => fg.append(el("div", { class: "feat" }, el("span", { class: "fname" }, f.name), el("span", { class: "fval" }, String(fmt(f.value))))));
  out.append(fg);
}

// =====================================================================
// charts + helpers
// =====================================================================
const trunc = (s, n) => { s = String(s); return s.length > n ? s.slice(0, n - 1) + "…" : s; };

// svgBars: horizontal bar chart. items=[{label,value}]. diverging=true centers at 0.
// Labels sit in a fixed left gutter (truncated to fit); values render just outside
// each bar's far end (clamped into the plot); text is vertically centered on the
// bar via dominant-baseline — so nothing overlaps regardless of label length.
function svgBars(items, opts = {}) {
  const fmtV = opts.fmt || ((v) => String(fmt(v)));
  const W = 640, rowH = 30, padL = 150, padR = 66, H = items.length * rowH + 12;
  const s = svg("svg", { viewBox: `0 0 ${W} ${H}`, class: "bars", width: "100%", preserveAspectRatio: "xMinYMin meet" });
  const maxAbs = Math.max(1e-9, ...items.map((it) => Math.abs(it.value)));
  const plotW = W - padL - padR;
  const zeroX = opts.diverging ? padL + plotW / 2 : padL;
  const scale = opts.diverging ? (plotW / 2) / maxAbs : plotW / maxAbs;
  if (opts.diverging) s.append(svg("line", { x1: zeroX, y1: 4, x2: zeroX, y2: H - 4, stroke: "var(--line)", "stroke-width": 1, "stroke-dasharray": "3 3" }));
  items.forEach((it, i) => {
    const cy = i * rowH + rowH / 2 + 6;
    const w = Math.abs(it.value) * scale;
    const x = it.value >= 0 ? zeroX : zeroX - w;
    const color = opts.color || (it.value >= 0 ? "var(--good)" : "var(--bad)");
    s.append(svg("text", { x: padL - 8, y: cy, "text-anchor": "end", "dominant-baseline": "middle", class: "bl" }, trunc(it.label, 18)));
    s.append(svg("rect", { x, y: cy - (rowH - 14) / 2, width: Math.max(2, w), height: rowH - 14, rx: 3, fill: color, opacity: 0.85 }));
    const vx = it.value >= 0 ? x + w + 6 : x - 6;
    s.append(svg("text", { x: vx, y: cy, "text-anchor": it.value >= 0 ? "start" : "end", "dominant-baseline": "middle", class: "bv" }, fmtV(it.value)));
  });
  return s;
}

function thr(cells, classes) {
  const t = el("tr");
  cells.forEach((c, i) => t.append(el("th", { class: classes[i] || "" }, c)));
  return t;
}

// =====================================================================
// boot
// =====================================================================
async function loadSummary() {
  const s = await api("/api/summary");
  STATE.summary = s;
  STATE.localSet = new Set(s.local_models || []);
  STATE.frontier = s.frontier_model;
  $("#backend-pill").textContent = "anthropic: " + (s.anthropic ? "live" : "off") + " · embed: " + s.embed_model;
  const c = s.counts || {};
  const bar = $("#statbar"); bar.innerHTML = "";
  const stat = (k, v) => bar.append(el("span", { class: "stat" }, el("b", {}, String(v)), " " + k));
  stat("local", (s.local_models || []).join(" · ") || "—");
  stat("frontier", s.frontier_model || "—");
  stat("pointwise", (c.pointwise_implicit ?? 0) + (c.pointwise_judge ?? 0));
  stat("pairwise", c.pairwise ?? 0);
  stat("gold", c.gold ?? 0);
}
async function loadRouters() {
  const r = await api("/api/routers");
  STATE.routers = r.routers || [];
  const sel = $("#route-router");
  STATE.routers.filter((x) => x.kind !== "baseline").forEach((x) => sel.append(el("option", { value: x.name }, x.name)));
}

(async function boot() {
  await Promise.all([loadSummary(), loadRouters()]);
  initExamples();
  loadCorpus();
})();
