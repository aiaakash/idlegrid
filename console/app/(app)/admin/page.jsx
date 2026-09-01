"use client";

import { useEffect, useMemo, useState } from "react";

const fmtUSD = (micro) => `$${(micro / 1_000_000).toFixed(4)}`;

const ago = (ts) => {
  const s = Math.floor((Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
};

export default function AdminPage() {
  const [tab, setTab] = useState("users");
  const [users, setUsers] = useState([]);
  const [prices, setPrices] = useState({ defaults: [], overrides: [] });
  const [payouts, setPayouts] = useState([]);
  const [msg, setMsg] = useState(null); // { ok, text } shown per-tab
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    const [u, p, po] = await Promise.all([
      fetch("/api/console/admin/users").then((r) => r.json()),
      fetch("/api/console/admin/prices").then((r) => r.json()),
      fetch("/api/console/admin/payouts").then((r) => r.json()),
    ]);
    setUsers(u.users || []);
    setPrices(p);
    setPayouts(po.payouts || []);
  }
  useEffect(() => { load(); }, []);

  const flash = (ok, text) => {
    setMsg({ ok, text });
    setTimeout(() => setMsg((m) => (m?.text === text ? null : m)), 4000);
  };

  async function createUser(e) {
    e.preventDefault();
    const f = e.target;
    setBusy(true);
    const res = await fetch("/api/console/admin/users", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: f.email.value, password: f.password.value, role: f.role.value }),
    });
    setBusy(false);
    const j = await res.json();
    flash(res.ok, res.ok ? `user #${j.id} (${j.email}) created` : j.error);
    if (res.ok) { f.reset(); load(); }
  }

  async function setPrice(e) {
    e.preventDefault();
    const f = e.target;
    setBusy(true);
    const res = await fetch("/api/console/admin/prices", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model: f.model.value,
        input_micro_per_1m: Math.round(parseFloat(f.input.value) * 1_000_000),
        output_micro_per_1m: Math.round(parseFloat(f.output.value) * 1_000_000),
      }),
    });
    setBusy(false);
    const j = await res.json();
    flash(res.ok, res.ok ? `price set for ${j.model}` : j.error);
    if (res.ok) { f.reset(); load(); }
  }

  async function payoutAction(id, action) {
    const label = action === "approve" ? "Approve" : "Mark paid";
    if (!confirm(`${label} payout #${id}?`)) return;
    let rail = "manual", rail_ref = "";
    if (action === "markpaid") {
      rail_ref = prompt("Payment reference (txn id / UPI ref / PayPal link), optional:") || "";
    }
    const res = await fetch("/api/console/admin/payouts", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, action, rail, rail_ref }),
    });
    const j = await res.json().catch(() => ({}));
    flash(res.ok, res.ok ? `payout #${id} ${action === "approve" ? "approved" : "marked paid"}` : j.error || "failed");
    load();
  }

  const filteredUsers = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return users;
    return users.filter((u) => u.email.toLowerCase().includes(q) || u.role.includes(q) || String(u.id) === q);
  }, [users, query]);

  const pendingPayouts = payouts.filter((p) => p.status !== "paid");
  const shownPayouts = query.trim()
    ? payouts.filter((p) => p.status.includes(query.trim().toLowerCase()) || String(p.user_id) === query.trim() || String(p.id) === query.trim())
    : [...pendingPayouts, ...payouts.filter((p) => p.status === "paid")];

  return (
    <>
      <div className="row" style={{ marginBottom: 16, borderBottom: "1px solid var(--border)", paddingBottom: 10 }}>
        {["users", "prices", "payouts"].map((t) => (
          <button
            key={t}
            className="ghost"
            onClick={() => { setTab(t); setMsg(null); setQuery(""); }}
            style={tab === t
              ? { color: "var(--text)", borderColor: "var(--accent)", background: "rgba(124,92,255,.12)" }
              : { border: "none" }}
          >
            {t}{t === "payouts" && pendingPayouts.length > 0 ? ` (${pendingPayouts.length})` : ""}
          </button>
        ))}
        <div className="spacer" style={{ flex: 1 }} />
        <input
          placeholder={tab === "users" ? "filter by email / role / id" : tab === "payouts" ? "filter by status / user / id" : ""}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={{ width: 230, visibility: tab === "prices" ? "hidden" : "visible" }}
        />
        {msg && <span className={msg.ok ? "ok" : "err"}>{msg.text}</span>}
      </div>

      {tab === "users" && (
        <>
          <div className="card">
            <h2>Create user</h2>
            <form onSubmit={createUser} className="row">
              <input name="email" placeholder="email" style={{ flex: 1 }} required />
              <input name="password" placeholder="password (min 8)" type="password" required />
              <select name="role" defaultValue="developer">
                <option value="developer">developer</option>
                <option value="provider_owner">provider_owner</option>
                <option value="admin">admin</option>
              </select>
              <button disabled={busy}>{busy ? "Creating…" : "Create"}</button>
            </form>
          </div>
          <div className="card">
            <h2>Users <span className="muted" style={{ fontWeight: 400 }}>({filteredUsers.length})</span></h2>
            {filteredUsers.length === 0 ? (
              <p className="muted">{query ? "no users match the filter" : "no users yet"}</p>
            ) : (
              <table>
                <thead><tr><th>ID</th><th>Email</th><th>Role</th><th style={{ textAlign: "right" }}>Balance</th><th>Created</th></tr></thead>
                <tbody>
                  {filteredUsers.map((u) => (
                    <tr key={u.id}>
                      <td className="mono">#{u.id}</td>
                      <td>{u.email}</td>
                      <td><span className={`badge ${u.role === "admin" ? "warn" : ""}`}>{u.role}</span></td>
                      <td className="num">{fmtUSD(u.developer_balance_micro)}</td>
                      <td className="muted" title={new Date(u.created_at).toLocaleString()}>{ago(u.created_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </>
      )}

      {tab === "prices" && (
        <>
          <div className="card">
            <h2>Set model price (USD per 1M tokens)</h2>
            <form onSubmit={setPrice} className="row">
              <input name="model" placeholder="model id" style={{ flex: 1 }} required />
              <input name="input" placeholder="input $" type="number" step="0.01" min="0" required />
              <input name="output" placeholder="output $" type="number" step="0.01" min="0" required />
              <button disabled={busy}>{busy ? "Saving…" : "Set"}</button>
            </form>
          </div>
          <div className="card">
            <h2>Overrides</h2>
            {prices.overrides?.length === 0 ? (
              <p className="muted">no overrides — default rates apply ($0.05 in / $0.20 out per 1M)</p>
            ) : (
              <table>
                <thead><tr><th>Model</th><th style={{ textAlign: "right" }}>Input $/1M</th><th style={{ textAlign: "right" }}>Output $/1M</th></tr></thead>
                <tbody>
                  {(prices.overrides || []).map((p) => (
                    <tr key={p.model}>
                      <td className="mono">{p.model}</td>
                      <td className="num">${(p.in_micro / 1e6).toFixed(2)}</td>
                      <td className="num">${(p.out_micro / 1e6).toFixed(2)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </>
      )}

      {tab === "payouts" && (
        <div className="card">
          <h2>Payout queue</h2>
          {shownPayouts.length === 0 ? (
            <p className="muted">{query ? "no payouts match the filter" : "no payout requests"}</p>
          ) : (
            <table>
              <thead><tr><th>ID</th><th>User</th><th style={{ textAlign: "right" }}>Amount</th><th>Status</th><th>Rail ref</th><th>Actions</th></tr></thead>
              <tbody>
                {shownPayouts.map((p) => (
                  <tr key={p.id}>
                    <td className="mono">#{p.id}</td>
                    <td className="mono">user #{p.user_id}</td>
                    <td className="num">{fmtUSD(p.amount_micro)}</td>
                    <td><span className={`badge ${p.status === "paid" ? "ok" : p.status === "approved" ? "" : "warn"}`}>{p.status}</span></td>
                    <td className="muted mono">{p.rail_ref || "—"}</td>
                    <td className="row">
                      {p.status === "requested" && (
                        <button className="ghost" onClick={() => payoutAction(p.id, "approve")}>Approve</button>
                      )}
                      {p.status !== "paid" && (
                        <button className="ghost" onClick={() => payoutAction(p.id, "markpaid")}>Mark paid</button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </>
  );
}
