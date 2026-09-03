"use client";

// Tiny dependency-free SVG sparkline: area + line, accent-colored.
export default function Sparkline({ points, height = 48, label = "Requests over time" }) {
  if (!points || points.length < 2) return null;
  const w = 240, h = height, pad = 2;
  const max = Math.max(...points, 1);
  const step = (w - pad * 2) / (points.length - 1);
  const xy = points.map((v, i) => [pad + i * step, h - pad - (v / max) * (h - pad * 2)]);
  const line = xy.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  const area = `${pad},${h - pad} ${line} ${(w - pad).toFixed(1)},${h - pad}`;

  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none"
         style={{ width: "100%", height, display: "block" }}
         role="img" aria-label={label}>
      <title>{label}</title>
      <polygon points={area} fill="var(--accent)" opacity="0.12" />
      <polyline points={line} fill="none" stroke="var(--accent)" strokeWidth="1.5"
                strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}
