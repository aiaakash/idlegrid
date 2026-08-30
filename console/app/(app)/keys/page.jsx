"use client";

import { useEffect, useState } from "react";

export default function KeysPage() {
  const [keys, setKeys] = useState([]);
  const [label, setLabel] = useState("");
  const [fresh, setFresh] = useState(null); // plaintext shown once
  const [err, setErr] = useState("");

  async function load() {
    const res = await fetch("/api/console/keys");
    const j = await res.json();
    setKeys(j.keys || []);
  }
  useEffect(() => { load(); }, []);

  async function create(e) {
    e.preventDefault();
    setErr("");
    const res = await fetch("/api/console/keys", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label }),
    });
    const j = await res.json();
    if (!res.ok) return setErr(j.error || "failed");
    setFresh(j.api_key);
    setLabel("");
    load();
  }

  async function revoke(id) {
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
          <button>Create</button>
        </form>
        {fresh && (
          <div style={{ marginTop: 12 }}>
            <p className="ok">Key created — copy it now, it is shown only once:</p>
            <p className="mono" style={{ background: "var(--panel2)", padding: "10px 12px", borderRadius: 8, marginTop: 6, wordBreak: "break-all" }}>{fresh}</p>
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
                  <td className="muted">{new Date(k.created_at).toLocaleString()}</td>
                  <td>
                    <span className={`badge ${k.revoked ? "warn" : "ok"}`}>
                      {k.revoked ? "revoked" : "active"}
                    </span>
                  </td>
                  <td>
                    {!k.revoked && (
                      <button className="danger" onClick={() => revoke(k.id)}>Revoke</button>
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
