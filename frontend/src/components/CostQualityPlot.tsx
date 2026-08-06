import { type Anchor, type LeaderRow } from "../api";

interface Pt {
  label: string;
  x: number; // local share 0..1
  y: number; // quality retention 0..1
  kind: "router" | "anchor";
  color: string;
}

// CostQualityPlot places every router at (local share, quality vs always-Opus):
// up = higher quality, right = more local (cheaper). Real routers cluster on the
// quality=100% edge, so inline labels collide — instead the plot carries only
// clean markers and a KEY beside it names each series with exact coordinates
// (which also disambiguates points that sit on top of each other). The three
// anchors (always-frontier / oracle / always-local) bound the achievable region.
export function CostQualityPlot({ leaderboard, anchors }: { leaderboard: LeaderRow[]; anchors: Anchor[] }) {
  const anchorColor: Record<string, string> = {
    "always-frontier": "var(--frontier)",
    oracle: "var(--ink)",
    "always-local": "var(--local)",
  };
  // Distinct categorical hues per learned router — chosen to be legible on white
  // and NOT collide with the anchor colors (purple / ink / green).
  const ROUTER_PALETTE = ["#1f7a78", "#2563eb", "#d97706", "#db2777", "#0891b2", "#7c6f00"];
  const routerPts: Pt[] = leaderboard.map((r, i) => ({
    label: r.router,
    x: (r.metrics["local_share@thr"] as number) ?? 0,
    y: (r.metrics["qual_retention"] as number) ?? 0,
    kind: "router",
    color: ROUTER_PALETTE[i % ROUTER_PALETTE.length],
  }));
  const anchorPts: Pt[] = anchors.map((a) => ({
    label: a.name,
    x: a.local_share,
    y: a.qual_retention,
    kind: "anchor",
    color: anchorColor[a.name] || "var(--muted)",
  }));
  const all = [...anchorPts, ...routerPts];
  if (!all.length) return null;

  const W = 560,
    H = 380,
    padL = 52,
    padR = 22,
    padT = 20,
    padB = 46;
  const plotW = W - padL - padR,
    plotH = H - padT - padB;

  // y zooms to where points live but always tops out at 100% (no >100% tick).
  const minY = Math.min(1, ...all.map((p) => p.y));
  const yLo = Math.max(0, Math.floor((minY - 0.04) * 20) / 20);
  const px = (x: number) => padL + x * plotW;
  const py = (y: number) => padT + (1 - (y - yLo) / (1 - yLo)) * plotH;

  const xTicks = [0, 0.25, 0.5, 0.75, 1];
  const yTicks: number[] = [];
  for (let i = 0; i <= 4; i++) yTicks.push(yLo + ((1 - yLo) * i) / 4);

  const front = [...anchorPts].sort((a, b) => a.x - b.x);
  const frontPath = front.map((p, i) => `${i ? "L" : "M"}${px(p.x).toFixed(1)} ${py(p.y).toFixed(1)}`).join(" ");

  // tiny jitter so markers stacked on the exact same spot stay individually
  // visible; the key carries the true values so this is purely cosmetic.
  const seen = new Map<string, number>();
  const jitter = (p: Pt) => {
    const k = `${Math.round(px(p.x))}:${Math.round(py(p.y))}`;
    const n = seen.get(k) ?? 0;
    seen.set(k, n + 1);
    return n === 0 ? 0 : (n % 2 ? 1 : -1) * Math.ceil(n / 2) * 6;
  };
  const diamond = (x: number, y: number, s: number) => `M${x} ${y - s}L${x + s} ${y}L${x} ${y + s}L${x - s} ${y}Z`;
  const pct = (v: number) => `${Math.round(v * 100)}%`;

  const keyRouters = [...routerPts].sort((a, b) => b.x - a.x);
  const keyAnchors = [...anchorPts].sort((a, b) => b.y - a.y || b.x - a.x);

  const KeyRow = ({ p, glyph }: { p: Pt; glyph: "dot" | "diamond" }) => (
    <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "3px 0" }}>
      <svg width={14} height={14} style={{ flex: "0 0 auto" }} aria-hidden="true">
        {glyph === "dot" ? (
          <circle cx={7} cy={7} r={4.5} fill={p.color} />
        ) : (
          <path d={diamond(7, 7, 5)} fill="var(--panel)" stroke={p.color} strokeWidth={2} />
        )}
      </svg>
      <span className="rname" style={{ fontSize: 12, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>
        {p.label}
      </span>
      <span className="muted" style={{ marginLeft: "auto", fontFamily: "var(--mono)", fontSize: 11.5, whiteSpace: "nowrap" }}>
        {pct(p.x)} · q{pct(p.y)}
      </span>
    </div>
  );

  return (
    <div className="chart">
      <div style={{ display: "flex", gap: 18, alignItems: "flex-start", flexWrap: "wrap" }}>
        <svg viewBox={`0 0 ${W} ${H}`} style={{ flex: "1 1 380px", minWidth: 0 }} preserveAspectRatio="xMinYMin meet">
          {yTicks.map((t, i) => (
            <g key={"y" + i}>
              <line x1={padL} y1={py(t)} x2={W - padR} y2={py(t)} stroke="var(--line)" strokeWidth={1} />
              <text x={padL - 8} y={py(t)} textAnchor="end" dominantBaseline="middle" className="bl">
                {(t * 100).toFixed(0)}%
              </text>
            </g>
          ))}
          {xTicks.map((t, i) => (
            <g key={"x" + i}>
              <line x1={px(t)} y1={padT} x2={px(t)} y2={H - padB} stroke="var(--line)" strokeWidth={1} strokeDasharray="2 3" />
              <text x={px(t)} y={H - padB + 17} textAnchor="middle" className="bl">
                {(t * 100).toFixed(0)}%
              </text>
            </g>
          ))}
          <text x={padL + plotW / 2} y={H - 6} textAnchor="middle" className="bl">
            share kept on local  (right = cheaper)
          </text>
          <text x={13} y={padT + plotH / 2} textAnchor="middle" className="bl" transform={`rotate(-90 13 ${padT + plotH / 2})`}>
            quality vs always-Opus  (up = better)
          </text>

          <path d={frontPath} fill="none" stroke="var(--muted)" strokeWidth={1.5} strokeDasharray="5 4" opacity={0.6} />

          {anchorPts.map((p, i) => (
            <path key={"am" + i} d={diamond(px(p.x) + jitter(p), py(p.y), 6)} fill="var(--panel)" stroke={p.color} strokeWidth={2} />
          ))}
          {routerPts.map((p, i) => (
            <circle key={"rm" + i} cx={px(p.x) + jitter(p)} cy={py(p.y)} r={5.5} fill={p.color} stroke="var(--panel)" strokeWidth={1.5} />
          ))}
        </svg>

        <div style={{ flex: "0 0 210px", minWidth: 190 }}>
          <div className="muted small" style={{ textTransform: "uppercase", letterSpacing: ".05em", fontWeight: 700, margin: "2px 0 4px" }}>
            learned routers
          </div>
          {keyRouters.map((p) => (
            <KeyRow key={p.label} p={p} glyph="dot" />
          ))}
          <div className="muted small" style={{ textTransform: "uppercase", letterSpacing: ".05em", fontWeight: 700, margin: "12px 0 4px" }}>
            reference anchors
          </div>
          {keyAnchors.map((p) => (
            <KeyRow key={p.label} p={p} glyph="diamond" />
          ))}
        </div>
      </div>
      <div className="dist-legend muted small">
        Each point = one router by <b>local share</b> (x) and <b>quality vs always-Opus</b> (y). Values are in the key
        (<span style={{ fontFamily: "var(--mono)" }}>local% · q quality%</span>). Dashed = achievable frontier. Ideal:
        hug the <b>oracle</b> — top, as far right as possible.
      </div>
    </div>
  );
}
