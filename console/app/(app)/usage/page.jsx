"use client";

import { useEffect, useState } from "react";

const fmtUSD = (micro) =>
  `$${(micro / 1_000_000).toFixed(4)}`;

const ago = (ts) => {
  const s = Math.floor((Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
};

export default function UsagePage() {
  const [rows, setRows] = useState([]);
  const [err, setErr] = useState("");

  useEffect(() => {
    (async () => {
      const res = await fetch("/api/console/usage");
      const j = await res.json();
      setRows(j.usage || []);
    })().catch(() => setErr("failed to load"));
  }, []);

  const totIn = rows.reduce((a, u) => a + (u.provider_input_tokens ?? u.est_input_tokens ?? 0), 0);
  const totOut = rows.reduce((a, u) => a + (u.provider_output_tokens ?? u.est_output_tokens ?? 0), 0);
  const totGross = rows.reduce((a, u) => a + (u.gross_micro || 0), 0);

  return (
    <div className="card">
      <h2>Usage — last 100 requests</h2>
      {err && <p className="err">{err}</p>}
      {rows.length === 0 ? (
        <p className="muted">no usage yet</p>
      ) : (
        <table>
          <thead>
            <tr><th>When</th><th>Model</th><th style={{ textAlign: "right" }}>In tok</th><th style={{ textAlign: "right" }}>Out tok</th><th style={{ textAlign: "right" }}>Gross</th><th>Audit</th><th>Status</th></tr>
          </thead>
          <tbody>
            {rows.map((u) => (
              <tr key={u.request_id}>
                <td className="muted" title={new Date(u.created_at).toLocaleString()}>{ago(u.created_at)}</td>
                <td className="mono">{u.model}</td>
                <td className="num">{(u.provider_input_tokens ?? u.est_input_tokens).toLocaleString()}</td>
                <td className="num">{(u.provider_output_tokens ?? u.est_output_tokens).toLocaleString()}</td>
                <td className="num">{fmtUSD(u.gross_micro)}</td>
                <td>
                  <span className={`badge ${u.counts_within_tolerance ? "ok" : "warn"}`}>
                    {u.counts_within_tolerance ? "match" : "flagged"}
                  </span>
                </td>
                <td><span className={`badge ${u.status === "completed" ? "ok" : "warn"}`}>{u.status}</span></td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr>
              <td className="muted">Total</td>
              <td></td>
              <td className="num">{totIn.toLocaleString()}</td>
              <td className="num">{totOut.toLocaleString()}</td>
              <td className="num">{fmtUSD(totGross)}</td>
              <td></td>
              <td></td>
            </tr>
          </tfoot>
        </table>
      )}
    </div>
  );
}
