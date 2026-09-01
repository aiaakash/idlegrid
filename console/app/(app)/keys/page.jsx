"use client";

import { useEffect, useState } from "react";
import CopyButton from "@/components/CopyButton";

const ago = (ts) => {
  const s = Math.floor((Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
};

export default function KeysPage() {
  const [keys, setKeys] = useState([]);
  const [label, setLabel] = useState("");
  const [fresh, setFresh] = useState(null); // plaintext shown once
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    const res = await fetch("/api/console/keys");
    const j = await res.json();
    setKeys(j.keys || []);
  }
  useEffect(() => { load(); }, []);

  async function create(e) {
    e.preventDefault();
    setErr(""); setBusy(true);
    const res = await fetch("/api/console/keys", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label }),
    });
    setBusy(false);
    const j = await res.json();
    if (!res.ok) return setErr(j.error || "failed");
    setFresh(j.api_key);
    setLabel("");
    load();
  }

  async function revoke(id, keyLabel) {
    if (!confirm(`Revoke key "${keyLabel || "#" + id}"? Apps using it stop working immediately.`)) return;
    await fetch("/api/console/keys", {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id }),
    });
    load();
  }

  return (
    <>
      <div className="card">
        <h2>Create API key</h2>
        <form onSubmit={create} className="row">
          <input
            placeholder="label (e.g. my-app)"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            style={{ flex: 1 }}
          />
          <button disabled={busy}>{busy ? "Creating…" : "Create"}</button>
        </form>
        {fresh && (
          <div style={{ marginTop: 12 }}>
            <p className="ok">Key created — copy it now, it is shown only once:</p>
            <div className="copychip" style={{ marginTop: 6 }}>
              <span className="mono">{fresh}</span>
              <CopyButton text={fresh} label="Copy key" />
            </div>
            <p className="muted" style={{ marginTop: 6 }}>Use it as: <code>Authorization: Bearer {fresh.slice(0, 9)}…</code> with base_url <code>https://api.sqlguroo.com/v1</code></p>
          </div>
        )}
        {err && <div className="err">{err}</div>}
      </div>

      <div className="card">
        <h2>Your keys</h2>
        {keys.length === 0 ? (
          <p className="muted">no keys yet</p>
        ) : (
          <table>
            <thead><tr><th>ID</th><th>Label</th><th>Created</th><th>Status</th><th></th></tr></thead>
            <tbody>
              {keys.map((k) => (
                <tr key={k.id}>
                  <td className="mono">#{k.id}</td>
                  <td>{k.label || "—"}</td>
                  <td className="muted" title={new Date(k.created_at).toLocaleString()}>{ago(k.created_at)}</td>
                  <td>
                    <span className={`badge ${k.revoked ? "warn" : "ok"}`}>
                      {k.revoked ? "revoked" : "active"}
                    </span>
                  </td>
                  <td style={{ textAlign: "right" }}>
                    {!k.revoked && (
                      <button className="danger" onClick={() => revoke(k.id, k.label)}>Revoke</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
