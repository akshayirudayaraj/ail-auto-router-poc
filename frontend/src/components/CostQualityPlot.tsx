import { type Anchor, type LeaderRow } from "../api";
import { trunc } from "../format";

interface Pt {
  label: string;
  x: number; // local share 0..1
  y: number; // quality retention 0..1
  kind: "router" | "anchor";
  color: string;
}

// One placed text label + a leader line back to its marker.
interface Placed {
  pt: Pt;
  mx: number;
  my: number; // marker pixel position
  lx: number;
  ly: number; // label pixel position
  anchor: "start" | "end";
}

// CostQualityPlot places every router as a dot on (local share, quality vs
// always-frontier). Up = higher quality, right = more local (cheaper). The three
// reference anchors (always-frontier, oracle, always-local) are drawn as hollow
// markers joined by the achievable-frontier line, so "how close to the oracle"
// reads straight off the picture. Ideal corner = top-right.
//
// Real routers cluster near quality=100%, so a naive inline label collides. We
// place each label beside its marker and dodge collisions into stacked lanes
// (greedy over horizontal overlap), drawing a thin leader when a label is moved.
export function CostQualityPlot({ leaderboard, anchors }: { leaderboard: LeaderRow[]; anchors: Anchor[] }) {
  const anchorColor: Record<string, string> = {
    "always-frontier": "var(--frontier)",
    oracle: "var(--ink)",
    "always-local": "var(--local)",
  };
  const routerPts: Pt[] = leaderboard.map((r) => ({
    label: r.router,
    x: (r.metrics["local_share@thr"] as number) ?? 0,
    y: (r.metrics["qual_retention"] as number) ?? 0,
    kind: "router",
    color: "var(--accent)",
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

  const W = 680,
    H = 384,
    padL = 54,
    padR = 20,
    padT = 24,
    padB = 46;
  const plotW = W - padL - padR,
    plotH = H - padT - padB;

  // y domain zooms to where points live but always tops out at 1.0 (no >100%).
  const minY = Math.min(1, ...all.map((p) => p.y));
  const yLo = Math.max(0, Math.floor((minY - 0.04) * 20) / 20); // pad + snap to 0.05
  const px = (x: number) => padL + x * plotW;
  const py = (y: number) => padT + (1 - (y - yLo) / (1 - yLo)) * plotH;

  const xTicks = [0, 0.25, 0.5, 0.75, 1];
  const yTicks: number[] = [];
  for (let i = 0; i <= 4; i++) yTicks.push(yLo + ((1 - yLo) * i) / 4);

  // achievable-frontier line through the anchors, left→right by local share.
  const front = [...anchorPts].sort((a, b) => a.x - b.x);
  const frontPath = front.map((p, i) => `${i ? "L" : "M"}${px(p.x).toFixed(1)} ${py(p.y).toFixed(1)}`).join(" ");

  // ---- label placement with lane-dodging ----
  const LANE = 15,
    CH = 6.2; // px per char (approx), lane height
  const midX = padL + plotW / 2;
  const truncName = (s: string) => trunc(s, 17);
  const placed: Placed[] = [];
  const laneBoxes: { x0: number; x1: number; y: number }[] = [];
  // process left-to-right so leaders read cleanly
  [...all]
    .map((pt) => ({ pt, mx: px(pt.x), my: py(pt.y) }))
    .sort((a, b) => a.mx - b.mx || b.my - a.my)
    .forEach(({ pt, mx, my }) => {
      const w = truncName(pt.label).length * CH + 6;
      const side: "start" | "end" = mx > midX ? "end" : "start";
      const lx = side === "start" ? mx + 9 : mx - 9;
      const x0 = side === "start" ? lx : lx - w;
      const x1 = side === "start" ? lx + w : lx;
      // find the lowest lane (offset from my) with no horizontal overlap
      let lane = 0;
      // eslint-disable-next-line no-constant-condition
      while (true) {
        const ly = my + lane * LANE;
        const hit = laneBoxes.some((b) => Math.abs(b.y - ly) < LANE - 1 && !(x1 < b.x0 || x0 > b.x1));
        if (!hit || lane > 10) {
          const clampedY = Math.min(Math.max(ly, padT + 4), H - padB - 4);
          laneBoxes.push({ x0, x1, y: clampedY });
          placed.push({ pt, mx, my, lx, ly: clampedY, anchor: side });
          break;
        }
        lane++;
      }
    });

  const diamond = (x: number, y: number, s: number) => `M${x} ${y - s}L${x + s} ${y}L${x} ${y + s}L${x - s} ${y}Z`;

  return (
    <div className="chart">
      <svg viewBox={`0 0 ${W} ${H}`} width="100%" preserveAspectRatio="xMinYMin meet">
        {/* y grid + labels */}
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
            <text x={px(t)} y={H - padB + 17} textAnchor="middle" className="bl">
              {(t * 100).toFixed(0)}%
            </text>
          </g>
        ))}
        {/* axis titles */}
        <text x={padL + plotW / 2} y={H - 6} textAnchor="middle" className="bl">
          share kept on local  (right = cheaper)
        </text>
        <text x={13} y={padT + plotH / 2} textAnchor="middle" className="bl" transform={`rotate(-90 13 ${padT + plotH / 2})`}>
          quality vs always-frontier  (up = better)
        </text>

        {/* achievable frontier through anchors */}
        <path d={frontPath} fill="none" stroke="var(--muted)" strokeWidth={1.5} strokeDasharray="5 4" opacity={0.6} />

        {/* markers */}
        {anchorPts.map((p, i) => (
          <path key={"am" + i} d={diamond(px(p.x), py(p.y), 6)} fill="var(--panel)" stroke={p.color} strokeWidth={2} />
        ))}
        {routerPts.map((p, i) => (
          <circle key={"rm" + i} cx={px(p.x)} cy={py(p.y)} r={5} fill="var(--accent)" stroke="var(--panel)" strokeWidth={1.5} />
        ))}

        {/* leaders + labels (dodged) */}
        {placed.map((pl, i) => {
          const moved = Math.abs(pl.ly - pl.my) > 4;
          const lxEnd = pl.anchor === "start" ? pl.lx - 3 : pl.lx + 3;
          return (
            <g key={"lb" + i}>
              {moved && (
                <line x1={pl.mx} y1={pl.my} x2={lxEnd} y2={pl.ly} stroke="var(--line-2)" strokeWidth={1} />
              )}
              <text
                x={pl.lx}
                y={pl.ly}
                textAnchor={pl.anchor}
                dominantBaseline="middle"
                className={pl.pt.kind === "anchor" ? "bl" : "bv"}
                fill={pl.pt.color}
                style={{ fontWeight: pl.pt.kind === "anchor" ? 600 : 500 }}
              >
                {truncName(pl.pt.label)}
              </text>
            </g>
          );
        })}
      </svg>
      <div className="dist-legend muted small">
        <span className="swatch" style={{ background: "var(--accent)" }} /> learned router
        <span className="swatch" style={{ background: "var(--frontier)", marginLeft: 10 }} /> always-frontier
        <span className="swatch" style={{ background: "var(--ink)" }} /> oracle
        <span className="swatch" style={{ background: "var(--local)" }} /> always-local · dashed = achievable frontier.
        Ideal: a router hugging the <b>oracle</b> (top, as far right as possible).
      </div>
    </div>
  );
}
