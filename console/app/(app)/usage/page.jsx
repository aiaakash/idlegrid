"use client";

import { useEffect, useState } from "react";

const fmtUSD = (micro) =>
  `$${(micro / 1_000_000).toFixed(4)}`;

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

  return (
    <div className="card">
      <h2>Usage — last 100 requests</h2>
      {err && <p className="err">{err}</p>}
      {rows.length === 0 ? (
        <p className="muted">no usage yet</p>
      ) : (
        <table>
          <thead>
            <tr><th>When</th><th>Model</th><th>In tok</th><th>Out tok</th><th>Gross</th><th>Audit</th><th>Status</th></tr>
          </thead>
          <tbody>
            {rows.map((u) => (
              <tr key={u.request_id}>
                <td className="muted">{new Date(u.created_at).toLocaleString()}</td>
                <td className="mono">{u.model}</td>
                <td className="mono">{u.provider_input_tokens ?? u.est_input_tokens}</td>
                <td className="mono">{u.provider_output_tokens ?? u.est_output_tokens}</td>
                <td className="mono">{fmtUSD(u.gross_micro)}</td>
                <td>
                  <span className={`badge ${u.counts_within_tolerance ? "ok" : "warn"}`}>
                    {u.counts_within_tolerance ? "match" : "flagged"}
                  </span>
                </td>
                <td><span className={`badge ${u.status === "completed" ? "ok" : "warn"}`}>{u.status}</span></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
