"use client";

import { useEffect, useState } from "react";
import Sparkline from "@/components/Sparkline";

const fmtUSD = (micro) =>
  `$${(micro / 1_000_000).toLocaleString(undefined, { minimumFractionDigits: 4, maximumFractionDigits: 4 })}`;

const ago = (ts) => {
  const s = Math.floor((Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
};

export default function OverviewPage() {
  const [me, setMe] = useState(null);
  const [usage, setUsage] = useState(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    (async () => {
      try {
        const meRes = await fetch("/api/console/me");
        setMe(await meRes.json());
        const uRes = await fetch("/api/console/usage");
        const u = await uRes.json();
        setUsage(u.usage || []);
      } catch {
        setErr("failed to load");
      }
    })();
  }, []);

  if (!me || usage === null) {
    return (
      <>
        <div className="grid" style={{ marginBottom: 16 }}>
          {[...Array(4)].map((_, i) => <div key={i} className="card skeleton" style={{ height: 84 }} />)}
        </div>
        <div className="card skeleton" style={{ height: 220 }} />
      </>
    );
  }

  const balance = me.developer_balance_micro || 0;
  const totalTokens = usage.reduce((a, u) => a + (u.est_output_tokens || 0), 0);
  const earnings = me.provider_earnings_micro || 0;

  // Requests per hour for the last 24h (for the sparkline).
  const buckets = Array(24).fill(0);
  const now = Date.now();
  for (const u of usage) {
    const hAgo = Math.floor((now - new Date(u.created_at).getTime()) / 3_600_000);
    if (hAgo >= 0 && hAgo < 24) buckets[23 - hAgo]++;
  }
  const req24h = buckets.reduce((a, b) => a + b, 0);

  return (
    <>
      {balance < 1_000_000 && (
        <div className="banner warn">
          ⚠ Balance is under $1 — <a href="/topup">top up</a> before requests start failing with 402.
        </div>
      )}

      <div className="grid" style={{ marginBottom: 16 }}>
        <div className="card stat">
          <div className="label">Balance</div>
          <div className="value">{fmtUSD(balance)}</div>
        </div>
        <div className="card stat">
          <div className="label">Requests (recent)</div>
          <div className="value">{usage.length}</div>
        </div>
        <div className="card stat">
          <div className="label">Output tokens (recent)</div>
          <div className="value">{totalTokens.toLocaleString()}</div>
        </div>
        {earnings > 0 && (
          <div className="card stat">
            <div className="label">Provider earnings</div>
            <div className="value" style={{ color: "var(--green)" }}>{fmtUSD(earnings)}</div>
          </div>
        )}
      </div>

      {me.role === "admin" && (
        <div className="grid" style={{ marginBottom: 16 }}>
          <div className="card stat">
            <div className="label">Platform revenue</div>
            <div className="value">{fmtUSD(me.platform_revenue_micro || 0)}</div>
          </div>
          <div className="card stat">
            <div className="label">Provider earnings (escrow)</div>
            <div className="value">{fmtUSD(me.provider_earnings_escrow_micro || 0)}</div>
          </div>
        </div>
      )}

      <div className="card">
        <div className="row" style={{ marginBottom: 4 }}>
          <h2 style={{ margin: 0 }}>Requests — last 24h</h2>
          <div className="spacer" style={{ flex: 1 }} />
          <span className="muted">{req24h} total</span>
        </div>
        {req24h > 0 ? (
          <Sparkline points={buckets} />
        ) : (
          <p className="muted">no requests in the last 24 hours</p>
        )}
      </div>

      <div className="card">
        <h2>Recent requests</h2>
        {err ? (
          <p className="err">{err}</p>
        ) : usage.length === 0 ? (
          <p className="muted">no requests yet — create an <a href="/keys">API key</a> and start calling the API</p>
        ) : (
          <table>
            <thead><tr><th>Model</th><th style={{ textAlign: "right" }}>In</th><th style={{ textAlign: "right" }}>Out</th><th style={{ textAlign: "right" }}>Cost</th><th>Status</th><th>When</th></tr></thead>
            <tbody>
              {usage.slice(0, 10).map((u) => (
                <tr key={u.request_id}>
                  <td className="mono">{u.model}</td>
                  <td className="num">{(u.provider_input_tokens ?? u.est_input_tokens).toLocaleString()}</td>
                  <td className="num">{(u.provider_output_tokens ?? u.est_output_tokens).toLocaleString()}</td>
                  <td className="num">{fmtUSD(u.gross_micro)}</td>
                  <td><span className={`badge ${u.status === "completed" ? "ok" : "warn"}`}>{u.status}</span></td>
                  <td className="muted" title={new Date(u.created_at).toLocaleString()}>{ago(u.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
