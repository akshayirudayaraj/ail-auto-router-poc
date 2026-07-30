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
const api = async (path, opts) => (await fetch(path, opts)).json();
const fmt = (x) => (typeof x === "number" ? (Number.isInteger(x) ? x : x.toFixed(3)) : x);

let STATE = { summary: null, localSet: new Set(), frontier: "" };

function isLocal(m) { return STATE.localSet.has(m); }
function modelChip(m) {
  if (!m) return el("span");
  return el("span", { class: "chip " + (isLocal(m) ? "model-local" : "model-frontier") }, m);
}

// ---- tabs ----
$$("#tabs button").forEach((b) =>
  b.addEventListener("click", () => {
    $$("#tabs button").forEach((x) => x.classList.remove("active"));
    b.classList.add("active");
    $$(".tab").forEach((t) => t.classList.remove("active"));
    $("#" + b.dataset.tab).classList.add("active");
    if (b.dataset.tab === "traces" && !tracesLoaded) loadTraces();
    if (b.dataset.tab === "data" && !dataLoaded) loadData("pointwise");
  })
);

// ---- overview ----
async function loadOverview() {
  const s = await api("/api/summary");
  STATE.summary = s;
  STATE.localSet = new Set(s.local_models || []);
  STATE.frontier = s.frontier_model;
  $("#backend-pill").textContent =
    "anthropic: " + (s.anthropic ? "live" : "off") + " · embed: " + s.embed_model;

  const c = s.counts || {};
  const cards = [
    ["Raw sessions", "traces", "—"],
    ["Pointwise (implicit)", c.pointwise_implicit ?? 0],
    ["Pointwise (judge)", c.pointwise_judge ?? 0],
    ["Pairwise", c.pairwise ?? 0],
    ["Gold (dual-arm)", c.gold ?? 0],
    ["Local rung", (s.local_models || []).join(" · ")],
    ["Frontier rung", s.frontier_model],
  ];
  const wrap = $("#overview-cards");
  wrap.innerHTML = "";
  for (const [lbl, val] of cards) {
    wrap.append(el("div", { class: "card" }, el("div", { class: "lbl" }, lbl), el("div", { class: "big" }, String(val))));
  }
  $("#overview-note").innerHTML = s.has_data
    ? `Data loaded. Browse <b>Traces</b> and <b>Labeled data</b>, then <b>Fit &amp; evaluate</b> the routers and <b>Route a prompt</b> live. Backend embeddings via <code>${s.embed_model}</code>; frontier/judge via ${s.anthropic ? "Claude (live)" : "— (offline)"}.`
    : `No dataset found. Run <code>make all</code> (or <code>make gen extract</code>) first, then reload.`;
}

// ---- traces ----
let tracesLoaded = false, traceData = [];
async function loadTraces() {
  const r = await api("/api/traces");
  traceData = r.sessions || [];
  const list = $("#trace-list");
  list.innerHTML = "";
  if (r.error) { list.append(el("div", { class: "item" }, r.error)); return; }
  traceData.forEach((s, i) => {
    list.append(el("div", { class: "item", onclick: () => selectTrace(i) },
      el("div", { class: "sid" }, s.session_id + " · " + s.num_turns + " turns"),
      el("div", { class: "task" }, s.task)));
  });
  tracesLoaded = true;
  if (traceData.length) selectTrace(0);
}
function selectTrace(i) {
  $$("#trace-list .item").forEach((x, j) => x.classList.toggle("active", j === i));
  const s = traceData[i];
  const d = $("#trace-detail");
  d.innerHTML = "";
  for (const t of s.turns) {
    const head = el("div", { class: "head" }, el("span", { class: "role" }, t.role));
    if (t.served_model) head.append(modelChip(t.served_model));
    if (t.propensity != null) head.append(el("span", { class: "chip" }, "π=" + fmt(t.propensity)));
    if (t.mined_signal) {
      const fail = ["switch", "paste_error", "negative", "retry"].includes(t.mined_signal);
      head.append(el("span", { class: "chip " + (t.mined_outcome === 1 ? "ok" : "bad") },
        "mined: " + (t.mined_outcome === 1 ? "adequate" : "inadequate")));
      head.append(el("span", { class: "chip" }, t.mined_signal + " @" + fmt(t.mined_confidence)));
    }
    if (t.truth_adequate != null)
      head.append(el("span", { class: "chip truth " + (t.truth_adequate ? "ok" : "bad") },
        "truth: " + (t.truth_adequate ? "adequate" : "inadequate")));
    if (t.truth_signal) head.append(el("span", { class: "chip truth" }, "truth sig: " + t.truth_signal));
    d.append(el("div", { class: "turn " + t.role }, head, el("div", { class: "content" }, t.content)));
  }
}

// ---- data ----
let dataLoaded = false;
const dataCols = {
  pointwise: [["prompt", "wrap"], ["model", ""], ["outcome", "num"], ["source", ""], ["confidence", "num"], ["turn_type", ""], ["hard_kw", "num"], ["tokens", "num"], ["session_id", ""]],
  pairwise: [["prompt", "wrap"], ["model_a", ""], ["model_b", ""], ["preferred", ""], ["source", ""]],
  gold: [["prompt", "wrap"], ["outcome_local", "num"], ["outcome_frontier", "num"], ["cell", ""], ["cost_local", "num"], ["cost_frontier", "num"], ["executable", ""]],
};
$$("#data-subtabs button").forEach((b) =>
  b.addEventListener("click", () => {
    $$("#data-subtabs button").forEach((x) => x.classList.remove("active"));
    b.classList.add("active");
    loadData(b.dataset.ds);
  })
);
async function loadData(ds) {
  dataLoaded = true;
  let url = "/api/" + ds;
  const tb = $("#data-toolbar"); tb.innerHTML = "";
  if (ds === "pointwise") {
    const sel = el("select", { onchange: (e) => loadData2(ds, e.target.value ? "?source=" + e.target.value : "") },
      el("option", { value: "" }, "all sources"),
      el("option", { value: "implicit" }, "implicit"),
      el("option", { value: "judge" }, "judge"));
    tb.append(el("span", {}, "filter:"), sel);
  }
  loadData2(ds, "");
}
async function loadData2(ds, qs) {
  const r = await api("/api/" + ds + qs);
  const cols = dataCols[ds];
  const t = $("#data-table"); t.innerHTML = "";
  const thead = el("tr");
  cols.forEach(([c, cls]) => thead.append(el("th", { class: cls }, c)));
  t.append(el("thead", {}, thead));
  const tb = el("tbody");
  (r.rows || []).forEach((row) => {
    const tr = el("tr");
    cols.forEach(([c, cls]) => {
      let v = row[c];
      if (c === "cell") return tr.append(el("td", {}, cellChip(v)));
      if (c === "model" || c === "model_a" || c === "model_b") return tr.append(el("td", {}, modelChip(v)));
      if ((c === "outcome" || c === "outcome_local" || c === "outcome_frontier") && typeof v === "number")
        return tr.append(el("td", { class: cls }, el("span", { class: "chip " + (v === 1 ? "ok" : "bad") }, v === 1 ? "1" : "0")));
      tr.append(el("td", { class: cls }, v == null ? "" : String(fmt(v))));
    });
    tb.append(tr);
  });
  t.append(tb);
  $("#data-toolbar").dataset.total = r.total || 0;
  updateDataCount(r.total || 0);
}
function updateDataCount(n) {
  let tag = $("#data-count");
  if (!tag) { tag = el("span", { id: "data-count", class: "muted" }); $("#data-toolbar").append(tag); }
  tag.textContent = n + " rows";
}
function cellChip(v) {
  const map = { "frontier-rescues": "ok", "both-pass": "", "local-only": "warn", "both-fail": "bad" };
  return el("span", { class: "chip " + (map[v] || "") }, v);
}

// ---- fit ----
$("#fit-run").addEventListener("click", runFit);
async function runFit() {
  const btn = $("#fit-run"); btn.disabled = true;
  $("#fit-status").textContent = "fitting…";
  const body = JSON.stringify({ train_source: $("#fit-source").value, threshold: parseFloat($("#fit-threshold").value) });
  const r = await api("/api/fit", { method: "POST", headers: { "Content-Type": "application/json" }, body });
  btn.disabled = false;
  if (r.error) { $("#fit-status").textContent = r.error; return; }
  $("#fit-status").textContent = `fit ${r.n_pointwise} pointwise / ${r.n_pairwise} pairwise rows (source: ${r.train_source})`;

  // abilities
  const at = $("#fit-abilities"); at.innerHTML = "";
  at.append(el("thead", {}, tr(["model", "planted θ", "recovered θ"], ["", "num", "num"])));
  const ab = el("tbody");
  (r.abilities || []).forEach((a) =>
    ab.append(el("tr", {}, el("td", {}, modelChip(a.model)),
      el("td", { class: "num" }, a.planted == null ? "—" : signed(a.planted)),
      el("td", { class: "num" }, signed(a.recovered)))));
  at.append(ab);

  // leaderboard
  const lt = $("#fit-leaderboard"); lt.innerHTML = "";
  if (!r.has_gold || !(r.leaderboard || []).length) {
    lt.append(el("tbody", {}, el("tr", {}, el("td", {}, "No gold set — run `make extract` to build one."))));
    $("#fit-legend").textContent = "";
    return;
  }
  const metricCols = ["aiq", "auc", "ece", "escalation@thr", "quality@thr", "qual_retention", "cost_vs_local", "under_escal_cellB", "over_escalation"];
  lt.append(el("thead", {}, tr(["router", ...metricCols], ["", ...metricCols.map(() => "num")])));
  const lb = el("tbody");
  // find best AIQ
  let bestAiq = -1; (r.leaderboard).forEach((x) => bestAiq = Math.max(bestAiq, x.metrics.aiq || 0));
  r.leaderboard.forEach((row) => {
    const trr = el("tr", {}, el("td", { class: "rname" }, row.router));
    metricCols.forEach((m) => {
      const v = row.metrics[m];
      const td = el("td", { class: "num" }, v == null ? "" : v.toFixed(3));
      if (m === "aiq" && Math.abs(v - bestAiq) < 1e-9 && v > 0) td.style.fontWeight = "700", td.style.color = "var(--good)";
      trr.append(td);
    });
    lb.append(trr);
  });
  lt.append(lb);
  $("#fit-legend").innerHTML = "AIQ = area under cost/quality hull (higher better; best highlighted). " +
    "under_escal_cellB = stayed local but frontier would have passed (lower better). " +
    "Only these gold numbers are absolute.";
}

// ---- route ----
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
  const body = JSON.stringify({ prompt, turn_type: $("#route-turntype").value, threshold: parseFloat($("#route-threshold").value) });
  const r = await api("/api/route", { method: "POST", headers: { "Content-Type": "application/json" }, body });
  btn.disabled = false;
  if (r.error) { $("#route-status").textContent = r.error; return; }
  $("#route-status").textContent = r.embedding_dim ? `embedded (${r.embedding_dim}-d)` : "no embedding (" + (r.embed_error || "offline") + ") — using feature priors";

  const out = $("#route-result"); out.innerHTML = "";
  const esc = r.escalate_votes, tot = r.total_routers;
  const majority = esc > tot / 2;
  out.append(el("div", { class: "verdict" },
    el("div", { class: "big " }, majority ? "↑ escalate" : "→ stay local"),
    el("div", { class: "muted" }, `${esc}/${tot} routers vote escalate at threshold ${r.threshold}. `
      + `Local = ${r.local_model}, frontier = ${r.frontier_model}.`)));

  const table = el("div", { class: "panel" });
  table.append(el("div", { class: "router-row", html: "<b>router</b><b>escalation score</b><b style='text-align:right'>score</b><b style='text-align:right'>decision</b>" }));
  r.routers.forEach((rt) => {
    const bar = el("div", { class: "bar" }, el("span", { style: `width:${Math.round(rt.score * 100)}%` }));
    table.append(el("div", { class: "router-row" },
      el("span", { class: "rname" }, rt.name),
      bar,
      el("span", { class: "num", style: "text-align:right;font-family:var(--mono)" }, rt.score.toFixed(3)),
      el("span", { style: "text-align:right" }, el("span", { class: "chip " + (rt.escalate ? "warn" : "ok") }, rt.escalate ? "frontier" : "local"))));
  });
  out.append(el("h3", {}, "Per-router decision"), table);

  out.append(el("h3", {}, "Prompt features ", el("span", { class: "muted" }, "(model-free, computed pre-generation)")));
  const fg = el("div", { class: "feat-grid" });
  r.features.forEach((f) => fg.append(el("div", { class: "feat" }, el("span", { class: "fname" }, f.name), el("span", { class: "fval" }, String(fmt(f.value))))));
  out.append(fg);
}

// ---- helpers ----
function tr(cells, classes) {
  const t = el("tr");
  cells.forEach((c, i) => t.append(el("th", { class: classes[i] || "" }, c)));
  return t;
}
function signed(x) { return (x >= 0 ? "+" : "") + x.toFixed(2); }

// ---- boot ----
loadOverview();
initExamples();
