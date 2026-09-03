"use client";

import { useEffect, useMemo, useState } from "react";
import { fmtUSD, fmtNum, ago, fmtDateTime } from "@/lib/format";

function statusBadge(status) {
  if (status === "completed") return "ok";
  if (status === "failed" || status === "timeout" || status === "cancelled") return "err";
  return "warn";
}

export default function UsagePage() {
  const [rows, setRows] = useState(null);
  const [err, setErr] = useState("");
  const [query, setQuery] = useState("");
  const [model, setModel] = useState("all");

  useEffect(() => {
    (async () => {
      try {
        const res = await fetch("/api/console/usage");
        const j = await res.json();
        setRows(j.usage || []);
      } catch {
        setErr("failed to load");
        setRows([]);
      }
    })();
  }, []);

  const models = useMemo(() => {
    if (!rows) return [];
    return [...new Set(rows.map((r) => r.model).filter(Boolean))].sort();
  }, [rows]);

  const filtered = useMemo(() => {
    if (!rows) return [];
    const q = query.trim().toLowerCase();
    return rows.filter((u) => {
      if (model !== "all" && u.model !== model) return false;
      if (!q) return true;
      return (
        u.model?.toLowerCase().includes(q) ||
        u.status?.toLowerCase().includes(q) ||
        u.request_id?.toLowerCase().includes(q)
      );
    });
  }, [rows, query, model]);

  const totIn = filtered.reduce((a, u) => a + (u.provider_input_tokens ?? u.est_input_tokens ?? 0), 0);
  const totOut = filtered.reduce((a, u) => a + (u.provider_output_tokens ?? u.est_output_tokens ?? 0), 0);
  const totGross = filtered.reduce((a, u) => a + (u.gross_micro || 0), 0);

  function exportCSV() {
    const head = "when,request_id,model,in_tokens,out_tokens,gross_usd,status,audit\n";
    const body = filtered.map((u) =>
      [
        new Date(u.created_at).toISOString(),
        u.request_id,
        `"${(u.model || "").replace(/"/g, '""')}"`,
        u.provider_input_tokens ?? u.est_input_tokens ?? 0,
        u.provider_output_tokens ?? u.est_output_tokens ?? 0,
        ((u.gross_micro || 0) / 1_000_000).toFixed(4),
        u.status,
        u.counts_within_tolerance ? "verified" : "flagged",
      ].join(",")
    ).join("\n");
    const blob = new Blob([head + body], { type: "text/csv" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "idlegrid-usage.csv";
    a.click();
    URL.revokeObjectURL(a.href);
  }

  return (
    <div className="card">
      <div className="row" style={{ marginBottom: 12 }}>
        <h2 style={{ margin: 0 }}>Usage — last 100 requests</h2>
        <div className="spacer" style={{ flex: 1 }} />
        {rows && rows.length > 0 && (
          <button type="button" className="ghost" onClick={exportCSV}>Export CSV</button>
        )}
      </div>
      <div className="row" style={{ marginBottom: 12, flexWrap: "wrap" }}>
        <input
          placeholder="filter by model / status / request id"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={{ flex: 1, minWidth: 200 }}
          aria-label="Filter usage"
        />
        <select value={model} onChange={(e) => setModel(e.target.value)} aria-label="Filter by model">
          <option value="all">all models</option>
          {models.map((m) => (
            <option key={m} value={m}>{m}</option>
          ))}
        </select>
      </div>
      {err && <p className="err" role="alert">{err}</p>}
      {rows === null ? (
        <div className="card skeleton" style={{ height: 200 }} aria-label="Loading usage" />
      ) : rows.length === 0 ? (
        <p className="muted">no usage yet — create an <a href="/keys">API key</a> and start calling the API</p>
      ) : filtered.length === 0 ? (
        <p className="muted">no requests match the filter</p>
      ) : (
        <div className="tablewrap">
          <table>
            <thead>
              <tr><th>When</th><th>Model</th><th style={{ textAlign: "right" }}>In tok</th><th style={{ textAlign: "right" }}>Out tok</th><th style={{ textAlign: "right" }}>Cost</th><th>Audit</th><th>Status</th></tr>
            </thead>
            <tbody>
              {filtered.map((u) => (
                <tr key={u.request_id} title={u.request_id}>
                  <td className="muted" title={fmtDateTime(u.created_at)}>{ago(u.created_at)}</td>
                  <td className="mono">{u.model}</td>
                  <td className="num">{fmtNum(u.provider_input_tokens ?? u.est_input_tokens)}</td>
                  <td className="num">{fmtNum(u.provider_output_tokens ?? u.est_output_tokens)}</td>
                  <td className="num" title="Charged to developer balance">{fmtUSD(u.gross_micro)}</td>
                  <td>
                    <span className={`badge ${u.counts_within_tolerance ? "ok" : "warn"}`}
                      title={u.counts_within_tolerance
                        ? "Provider token count matches coordinator estimate (±25%)"
                        : "Provider count differs >25% from estimate — flagged for audit"}>
                      {u.counts_within_tolerance ? "verified" : "flagged"}
                    </span>
                  </td>
                  <td><span className={`badge ${statusBadge(u.status)}`}>{u.status}</span></td>
                </tr>
              ))}
            </tbody>
            <tfoot>
              <tr>
                <td className="muted">Total{filtered.length !== rows.length ? ` (${filtered.length} shown)` : ""}</td>
                <td></td>
                <td className="num">{fmtNum(totIn)}</td>
                <td className="num">{fmtNum(totOut)}</td>
                <td className="num">{fmtUSD(totGross)}</td>
                <td></td>
                <td></td>
              </tr>
            </tfoot>
          </table>
        </div>
      )}
    </div>
  );
}
