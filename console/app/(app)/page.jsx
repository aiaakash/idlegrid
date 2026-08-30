"use client";

import { useEffect, useState } from "react";

const fmtUSD = (micro) =>
  `$${(micro / 1_000_000).toLocaleString(undefined, { minimumFractionDigits: 4, maximumFractionDigits: 4 })}`;

export default function OverviewPage() {
  const [me, setMe] = useState(null);
  const [usage, setUsage] = useState([]);
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

  if (!me) return <p className="muted">loading…</p>;
  const totalTokens = usage.reduce((a, u) => a + (u.est_output_tokens || 0), 0);

  return (
    <>
      <div className="grid" style={{ marginBottom: 16 }}>
        <div className="card stat">
          <div className="label">Balance</div>
          <div className="value">{fmtUSD(me.developer_balance_micro || 0)}</div>
        </div>
        <div className="card stat">
          <div className="label">Requests</div>
          <div className="value">{usage.length}</div>
        </div>
        <div className="card stat">
          <div className="label">Output tokens (recent)</div>
          <div className="value">{totalTokens.toLocaleString()}</div>
        </div>
        <div className="card stat">
          <div className="label">Role</div>
          <div className="value" style={{ fontSize: 16 }}>{me.role}</div>
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
        <h2>Recent requests</h2>
        {err ? (
          <p className="err">{err}</p>
        ) : usage.length === 0 ? (
          <p className="muted">no requests yet — create an API key and start calling the API</p>
        ) : (
          <table>
            <thead><tr><th>Model</th><th>In</th><th>Out</th><th>Cost</th><th>Status</th><th>When</th></tr></thead>
            <tbody>
              {usage.slice(0, 10).map((u) => (
                <tr key={u.request_id}>
                  <td className="mono">{u.model}</td>
                  <td className="mono">{u.provider_input_tokens ?? u.est_input_tokens}</td>
                  <td className="mono">{u.provider_output_tokens ?? u.est_output_tokens}</td>
                  <td className="mono">{fmtUSD(u.gross_micro)}</td>
                  <td><span className={`badge ${u.status === "completed" ? "ok" : "warn"}`}>{u.status}</span></td>
                  <td className="muted">{new Date(u.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
