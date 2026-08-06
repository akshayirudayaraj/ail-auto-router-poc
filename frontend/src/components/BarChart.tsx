import { fmt, trunc } from "../format";

export interface BarItem {
  label: string;
  value: number;
}

interface BarChartProps {
  items: BarItem[];
  diverging?: boolean;
  color?: string;
  fmtV?: (v: number) => string;
}

// Horizontal bar chart, ported from the old svgBars(). Labels sit in a fixed
// left gutter (truncated to fit); values render just outside each bar's far end;
// text is vertically centered via dominant-baseline so nothing overlaps
// regardless of label length. diverging=true centers the axis at 0.
export function BarChart({ items, diverging, color, fmtV }: BarChartProps) {
  const fv = fmtV || ((v: number) => String(fmt(v)));
  const W = 640,
    rowH = 30,
    padL = 150,
    padR = 66;
  const H = items.length * rowH + 12;
  const maxAbs = Math.max(1e-9, ...items.map((it) => Math.abs(it.value)));
  const plotW = W - padL - padR;
  const zeroX = diverging ? padL + plotW / 2 : padL;
  const scale = diverging ? plotW / 2 / maxAbs : plotW / maxAbs;

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="bars" width="100%" preserveAspectRatio="xMinYMin meet">
      {diverging && (
        <line x1={zeroX} y1={4} x2={zeroX} y2={H - 4} stroke="var(--line)" strokeWidth={1} strokeDasharray="3 3" />
      )}
      {items.map((it, i) => {
        const cy = i * rowH + rowH / 2 + 6;
        const w = Math.abs(it.value) * scale;
        const x = it.value >= 0 ? zeroX : zeroX - w;
        const fill = color || (it.value >= 0 ? "var(--good)" : "var(--bad)");
        const vx = it.value >= 0 ? x + w + 6 : x - 6;
        return (
          <g key={i}>
            <text x={padL - 8} y={cy} textAnchor="end" dominantBaseline="middle" className="bl">
              {trunc(it.label, 18)}
            </text>
            <rect
              x={x}
              y={cy - (rowH - 14) / 2}
              width={Math.max(2, w)}
              height={rowH - 14}
              rx={3}
              fill={fill}
              opacity={0.85}
            />
            <text x={vx} y={cy} textAnchor={it.value >= 0 ? "start" : "end"} dominantBaseline="middle" className="bv">
              {fv(it.value)}
            </text>
          </g>
        );
      })}
    </svg>
  );
}
