// Shared formatting helpers — single source of truth for money + time.
// Replaces the 5+ copy-pasted fmtUSD/ago variants across pages.

export function fmtUSD(micro) {
  const v = (micro || 0) / 1_000_000;
  // Adaptive precision: 2 decimals for $1+, 4 for sub-dollar dust.
  const digits = Math.abs(v) >= 1 ? 2 : 4;
  return `$${v.toLocaleString(undefined, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })}`;
}

export function fmtNum(n) {
  return (n ?? 0).toLocaleString();
}

export function ago(ts) {
  if (!ts) return "never";
  const t = new Date(ts).getTime();
  if (Number.isNaN(t)) return "—";
  let s = Math.floor((Date.now() - t) / 1000);
  if (s < 0) s = 0; // future timestamps (clock skew) -> "just now"
  if (s < 5) return "just now";
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export function fmtDateTime(ts) {
  if (!ts) return "—";
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString();
}
