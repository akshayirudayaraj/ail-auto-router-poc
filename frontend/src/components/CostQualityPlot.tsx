import { type Anchor, type LeaderRow } from "../api";
import { trunc } from "../format";

interface Pt {
  label: string;
  x: number; // local share 0..1
  y: number; // quality retention 0..1
  kind: "router" | "anchor";
  color: string;
}

// CostQualityPlot places every router as a dot on (local share, quality vs
// always-frontier). Up = higher quality, right = more local (cheaper). The three
// reference anchors (always-frontier, oracle, always-local) are drawn as hollow
// markers joined by the achievable-frontier line, so "how close to the oracle"
// reads straight off the picture. Ideal corner = top-right.
export function CostQualityPlot({ leaderboard, anchors }: { leaderboard: LeaderRow[]; anchors: Anchor[] }) {
  const anchorColor: Record<string, string> = {
    "always-frontier": "var(--frontier)",
    oracle: "var(--accent)",
    "always-local": "var(--local)",
  };
  const routerPts: Pt[] = leaderboard.map((r) => ({
    label: r.router,
    x: (r.metrics["local_share@thr"] as number) ?? 0,
    y: (r.metrics["qual_retention"] as number) ?? 0,
    kind: "router",
    color: "var(--ink)",
  }));
  const anchorPts: Pt[] = anchors.map((a) => ({
    label: a.name,
    x: a.local_share,
    y: a.qual_retention,
    kind: "anchor",
    color: anchorColor[a.name] || "var(--muted)",
  }));
  const all = [...routerPts, ...anchorPts];
  if (!all.length) return null;

  const W = 660,
    H = 380,
    padL = 52,
    padR = 18,
    padT = 18,
    padB = 42;
  const plotW = W - padL - padR,
    plotH = H - padT - padB;

  // y domain: zoom to where the points actually live, but always include 1.0.
  const minY = Math.min(1, ...all.map((p) => p.y));
  const yLo = Math.max(0, Math.floor((minY - 0.04) * 20) / 20); // pad + snap to 0.05
  const yHi = 1.02;
  const px = (x: number) => padL + x * plotW;
  const py = (y: number) => padT + (1 - (y - yLo) / (yHi - yLo)) * plotH;

  const xTicks = [0, 0.25, 0.5, 0.75, 1];
  const yTickN = 4;
  const yTicks = Array.from({ length: yTickN + 1 }, (_, i) => yLo + ((yHi - yLo) * i) / yTickN);

  // achievable-frontier line through the anchors, left→right by local share.
  const front = [...anchorPts].sort((a, b) => a.x - b.x);
  const frontPath = front.map((p, i) => `${i ? "L" : "M"}${px(p.x).toFixed(1)} ${py(p.y).toFixed(1)}`).join(" ");

  return (
    <div className="chart">
      <svg viewBox={`0 0 ${W} ${H}`} width="100%" preserveAspectRatio="xMinYMin meet">
        {/* grid + y ticks */}
        {yTicks.map((t, i) => (
          <g key={"y" + i}>
            <line x1={padL} y1={py(t)} x2={W - padR} y2={py(t)} stroke="var(--line)" strokeWidth={1} />
            <text x={padL - 8} y={py(t)} textAnchor="end" dominantBaseline="middle" className="bl">
              {(t * 100).toFixed(0)}%
            </text>
          </g>
        ))}
        {/* x ticks */}
        {xTicks.map((t, i) => (
          <g key={"x" + i}>
            <line x1={px(t)} y1={padT} x2={px(t)} y2={H - padB} stroke="var(--line)" strokeWidth={1} strokeDasharray="2 3" />
            <text x={px(t)} y={H - padB + 16} textAnchor="middle" className="bl">
              {(t * 100).toFixed(0)}%
            </text>
          </g>
        ))}
        {/* axis titles */}
        <text x={padL + plotW / 2} y={H - 6} textAnchor="middle" className="bl">
          share kept on local  (right = cheaper)
        </text>
        <text x={14} y={padT + plotH / 2} textAnchor="middle" className="bl" transform={`rotate(-90 14 ${padT + plotH / 2})`}>
          quality vs always-frontier  (up = better)
        </text>

        {/* achievable frontier through anchors */}
        <path d={frontPath} fill="none" stroke="var(--muted)" strokeWidth={1.5} strokeDasharray="5 4" opacity={0.7} />

        {/* anchors: hollow diamonds */}
        {anchorPts.map((p, i) => {
          const x = px(p.x),
            y = py(p.y),
            s = 6;
          return (
            <g key={"a" + i}>
              <path
                d={`M${x} ${y - s}L${x + s} ${y}L${x} ${y + s}L${x - s} ${y}Z`}
                fill="var(--panel)"
                stroke={p.color}
                strokeWidth={2}
              />
              <text x={x} y={y - s - 4} textAnchor="middle" className="bl" fill={p.color}>
                {p.label}
              </text>
            </g>
          );
        })}

        {/* routers: filled dots */}
        {routerPts.map((p, i) => {
          const x = px(p.x),
            y = py(p.y);
          return (
            <g key={"r" + i}>
              <circle cx={x} cy={y} r={5} fill="var(--accent)" stroke="var(--panel)" strokeWidth={1.5} />
              <text x={x + 9} y={y} dominantBaseline="middle" className="bv">
                {trunc(p.label, 16)}
              </text>
            </g>
          );
        })}
      </svg>
      <div className="dist-legend muted small">
        <span className="swatch" style={{ background: "var(--accent)" }} /> learned router
        <span className="swatch" style={{ background: "var(--frontier)", marginLeft: 12 }} /> always-frontier
        <span className="swatch" style={{ background: "var(--accent)" }} /> oracle
        <span className="swatch" style={{ background: "var(--local)" }} /> always-local · dashed = achievable frontier.
        Ideal: a router hugging the <b>oracle</b> (top, as far right as possible).
      </div>
    </div>
  );
}
