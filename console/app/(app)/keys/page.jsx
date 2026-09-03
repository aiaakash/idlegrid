"use client";

import { useEffect, useState } from "react";
import CopyButton from "@/components/CopyButton";
import { ago, fmtDateTime } from "@/lib/format";

const API_BASE = process.env.NEXT_PUBLIC_API_BASE_URL || "https://api.sqlguroo.com/v1";

export default function KeysPage() {
  const [keys, setKeys] = useState(null);
  const [label, setLabel] = useState("");
  const [fresh, setFresh] = useState(null); // plaintext shown once
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmRevoke, setConfirmRevoke] = useState(null); // key id awaiting confirm

  async function load() {
    try {
      const res = await fetch("/api/console/keys");
      const j = await res.json();
      setKeys(j.keys || []);
    } catch {
      setKeys([]);
    }
  }
  useEffect(() => { load(); }, []);

  async function create(e) {
    e.preventDefault();
    setErr(""); setBusy(true);
    try {
      const res = await fetch("/api/console/keys", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ label: label.trim() }),
      });
      const j = await res.json();
      if (!res.ok) {
        setErr(j.error || "failed to create key");
        return;
      }
      setFresh(j.api_key);
      setLabel("");
      await load();
    } catch {
      setErr("cannot reach the console — check your connection and retry");
    } finally {
      setBusy(false);
    }
  }

  async function revoke(id) {
    setConfirmRevoke(null);
    try {
      await fetch("/api/console/keys", {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id }),
      });
    } finally {
      load();
    }
  }

  const curlExample = fresh
    ? `curl ${API_BASE}/chat/completions \\\n  -H "Authorization: Bearer ${fresh}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"Qwen2.5-0.5B-Instruct-4bit","messages":[{"role":"user","content":"Say hello"}]}'`
    : "";

  return (
    <>
      <div className="card">
        <h2>Create API key</h2>
        <form onSubmit={create}>
          <div className="field">
            <label htmlFor="keylabel">Label</label>
            <div className="row">
              <input
                id="keylabel"
                placeholder="label (e.g. my-app)"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                style={{ flex: 1 }}
                disabled={busy}
                autoComplete="off"
                maxLength={64}
              />
              <button disabled={busy}>{busy ? "Creating…" : "Create"}</button>
            </div>
          </div>
        </form>
        {fresh && (
          <div style={{ marginTop: 12 }}>
            <p className="ok" role="status">Key created — copy it now, it is shown only once:</p>
            <div className="copychip" style={{ marginTop: 6 }}>
              <span className="mono">{fresh}</span>
              <CopyButton text={fresh} label="Copy key" />
            </div>
            <div className="copychip" style={{ marginTop: 8 }}>
              <span className="mono" style={{ whiteSpace: "pre-wrap" }}>{curlExample}</span>
              <CopyButton text={curlExample} label="Copy curl" />
            </div>
            <p className="muted" style={{ marginTop: 6 }}>Base URL: <code>{API_BASE}</code> · Header: <code>Authorization: Bearer …</code></p>
          </div>
        )}
        {err && <div className="err" role="alert">{err}</div>}
      </div>

      <div className="card">
        <h2>Your keys</h2>
        {keys === null ? (
          <div className="card skeleton" style={{ height: 80 }} aria-label="Loading keys" />
        ) : keys.length === 0 ? (
          <p className="muted">no keys yet — create one above to start calling the API</p>
        ) : (
          <div className="tablewrap">
            <table>
              <thead><tr><th>ID</th><th>Label</th><th>Created</th><th>Status</th><th><span style={{ display: "none" }}>Actions</span></th></tr></thead>
              <tbody>
                {keys.map((k) => (
                  <tr key={k.id}>
                    <td className="mono">#{k.id}</td>
                    <td>{k.label || "—"}</td>
                    <td className="muted" title={fmtDateTime(k.created_at)}>{ago(k.created_at)}</td>
                    <td>
                      <span className={`badge ${k.revoked ? "warn" : "ok"}`}>
                        {k.revoked ? "revoked" : "active"}
                      </span>
                    </td>
                    <td style={{ textAlign: "right" }}>
                      {!k.revoked && confirmRevoke !== k.id && (
                        <button className="danger" onClick={() => setConfirmRevoke(k.id)}>Revoke</button>
                      )}
                      {!k.revoked && confirmRevoke === k.id && (
                        <span className="confirmbar">
                          <span className="muted">Revoke? Apps using it break.</span>
                          <button className="danger" onClick={() => revoke(k.id)}>Yes, revoke</button>
                          <button className="ghost" onClick={() => setConfirmRevoke(null)}>Keep</button>
                        </span>
                      )}
                    </td>
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
