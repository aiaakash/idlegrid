"use client";

import { useEffect, useState } from "react";
import Sparkline from "@/components/Sparkline";
import { fmtUSD, fmtNum, ago, fmtDateTime } from "@/lib/format";

function statusBadge(status) {
  if (status === "completed") return "ok";
  if (status === "failed" || status === "timeout" || status === "cancelled") return "err";
  return "warn";
}

export default function OverviewPage() {
  const [me, setMe] = useState(null);
  const [usage, setUsage] = useState(null);
  const [err, setErr] = useState("");
  const [bannerDismissed, setBannerDismissed] = useState(false);

  async function load() {
    setErr("");
    try {
      const [meRes, uRes] = await Promise.all([
        fetch("/api/console/me"),
        fetch("/api/console/usage"),
      ]);
      if (!meRes.ok) throw new Error("failed to load account");
      setMe(await meRes.json());
      const u = await uRes.json();
      setUsage(u.usage || []);
    } catch {
      setErr("failed to load — check your connection and retry");
    }
  }

  useEffect(() => { load(); }, []);

  if (err && (!me || usage === null)) {
    return (
      <div className="card">
        <p className="err" role="alert">{err}</p>
        <div style={{ marginTop: 10 }}>
          <button className="ghost" onClick={load}>Retry</button>
        </div>
      </div>
    );
  }

  if (!me || usage === null) {
    return (
      <>
        <div className="grid" style={{ marginBottom: 16 }}>
          {[...Array(4)].map((_, i) => <div key={i} className="card skeleton" style={{ height: 84 }} aria-hidden="true" />)}
        </div>
        <div className="card skeleton" style={{ height: 220 }} aria-label="Loading overview" />
      </>
    );
  }

  const balance = me.developer_balance_micro || 0;
  const totalTokens = usage.reduce((a, u) => a + (u.provider_output_tokens ?? u.est_output_tokens ?? 0), 0);
  const earnings = me.provider_earnings_micro || 0;

  // Requests per hour for the last 24h (for the sparkline).
  // NOTE: /usage returns the last 100 rows, so the chart undercounts
  // when traffic exceeds 100 req / 24h.
  const buckets = Array(24).fill(0);
  const now = Date.now();
  for (const u of usage) {
    const hAgo = Math.floor((now - new Date(u.created_at).getTime()) / 3_600_000);
    if (hAgo >= 0 && hAgo < 24) buckets[23 - hAgo]++;
  }
  const req24h = buckets.reduce((a, b) => a + b, 0);
  const truncated = usage.length >= 100;

  return (
    <>
      {balance < 1_000_000 && !bannerDismissed && (
        <div className="banner warn" role="alert">
          <span>⚠ Balance is under $1 — <a href="/topup">top up</a> before requests start failing with 402.</span>
          <div className="spacer" style={{ flex: 1 }} />
          <button className="ghost" onClick={() => setBannerDismissed(true)} aria-label="Dismiss low-balance warning">Dismiss</button>
        </div>
      )}

      <div className="grid" style={{ marginBottom: 16 }}>
        <div className="card stat">
          <div className="label">Balance</div>
          <div className="value">{fmtUSD(balance)}</div>
        </div>
        <div className="card stat">
          <div className="label">Requests · last 100</div>
          <div className="value">{fmtNum(usage.length)}</div>
        </div>
        <div className="card stat">
          <div className="label">Output tokens · last 100</div>
          <div className="value">{fmtNum(totalTokens)}</div>
        </div>
        <div className="card stat">
          <div className="label">Provider earnings</div>
          <div className="value" style={{ color: earnings > 0 ? "var(--green)" : undefined }}>{fmtUSD(earnings)}</div>
        </div>
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
          <span className="muted">{req24h} total{truncated ? " (of last 100)" : ""}</span>
        </div>
        {req24h > 0 ? (
          <Sparkline points={buckets} label={`Requests per hour, ${req24h} total in last 24 hours`} />
        ) : (
          <p className="muted">no requests in the last 24 hours</p>
        )}
      </div>

      <div className="card">
        <div className="row" style={{ marginBottom: 12 }}>
          <h2 style={{ margin: 0 }}>Recent requests</h2>
          <div className="spacer" style={{ flex: 1 }} />
          <a href="/usage" className="muted">View all →</a>
        </div>
        {err ? (
          <p className="err" role="alert">{err}</p>
        ) : usage.length === 0 ? (
          <div>
            <p className="muted">no requests yet</p>
            <div className="steps" aria-label="Get started">
              <span className="step">1 · <a href="/topup">Add $5 credit</a></span>
              <span className="step">2 · <a href="/keys">Create an API key</a></span>
              <span className="step">3 · Call <code>/v1/chat/completions</code></span>
            </div>
          </div>
        ) : (
          <div className="tablewrap">
            <table>
              <thead><tr><th>Model</th><th style={{ textAlign: "right" }}>In</th><th style={{ textAlign: "right" }}>Out</th><th style={{ textAlign: "right" }}>Cost</th><th>Status</th><th>When</th></tr></thead>
              <tbody>
                {usage.slice(0, 10).map((u) => (
                  <tr key={u.request_id} title={u.request_id}>
                    <td className="mono">{u.model}</td>
                    <td className="num">{fmtNum(u.provider_input_tokens ?? u.est_input_tokens)}</td>
                    <td className="num">{fmtNum(u.provider_output_tokens ?? u.est_output_tokens)}</td>
                    <td className="num" title="Charged to developer balance">{fmtUSD(u.gross_micro)}</td>
                    <td><span className={`badge ${statusBadge(u.status)}`}>{u.status}</span></td>
                    <td className="muted" title={fmtDateTime(u.created_at)}>{ago(u.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </>
  );
}
