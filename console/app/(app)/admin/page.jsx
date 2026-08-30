"use client";

import { useEffect, useState } from "react";

const fmtUSD = (micro) => `$${(micro / 1_000_000).toFixed(4)}`;

export default function AdminPage() {
  const [tab, setTab] = useState("users");
  const [users, setUsers] = useState([]);
  const [prices, setPrices] = useState({ defaults: [], overrides: [] });
  const [payouts, setPayouts] = useState([]);
  const [msg, setMsg] = useState("");

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

  async function createUser(e) {
    e.preventDefault();
    const f = e.target;
    const res = await fetch("/api/console/admin/users", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: f.email.value, password: f.password.value, role: f.role.value }),
    });
    const j = await res.json();
    setMsg(res.ok ? `user #${j.id} (${j.email}) created` : j.error);
    if (res.ok) { f.reset(); load(); }
  }

  async function setPrice(e) {
    e.preventDefault();
    const f = e.target;
    const res = await fetch("/api/console/admin/prices", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model: f.model.value,
        input_micro_per_1m: Math.round(parseFloat(f.input.value) * 1_000_000),
        output_micro_per_1m: Math.round(parseFloat(f.output.value) * 1_000_000),
      }),
    });
    const j = await res.json();
    setMsg(res.ok ? `price set for ${j.model}` : j.error);
    if (res.ok) load();
  }

  async function payoutAction(id, action) {
    await fetch("/api/console/admin/payouts", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, action, rail: "manual" }),
    });
    load();
  }

  return (
    <>
      <div className="row" style={{ marginBottom: 16 }}>
        {["users", "prices", "payouts"].map((t) => (
          <button key={t} className={tab === t ? "" : "ghost"} onClick={() => setTab(t)}>{t}</button>
        ))}
        <div className="spacer" style={{ flex: 1 }} />
        {msg && <span className="ok">{msg}</span>}
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
              <button>Create</button>
            </form>
          </div>
          <div className="card">
            <h2>Users</h2>
            <table>
              <thead><tr><th>ID</th><th>Email</th><th>Role</th><th>Balance</th><th>Created</th></tr></thead>
              <tbody>
                {users.map((u) => (
                  <tr key={u.id}>
                    <td className="mono">#{u.id}</td>
                    <td>{u.email}</td>
                    <td><span className="badge">{u.role}</span></td>
                    <td className="mono">{fmtUSD(u.developer_balance_micro)}</td>
                    <td className="muted">{new Date(u.created_at).toLocaleDateString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}

      {tab === "prices" && (
        <>
          <div className="card">
            <h2>Set model price (USD per 1M tokens)</h2>
            <form onSubmit={setPrice} className="row">
              <input name="model" placeholder="model id" style={{ flex: 1 }} required />
              <input name="input" placeholder="input $" type="number" step="0.01" required />
              <input name="output" placeholder="output $" type="number" step="0.01" required />
              <button>Set</button>
            </form>
          </div>
          <div className="card">
            <h2>Overrides</h2>
            {prices.overrides?.length === 0 ? (
              <p className="muted">no overrides — default rates apply ($0.05 in / $0.20 out per 1M)</p>
            ) : (
              <table>
                <thead><tr><th>Model</th><th>Input $/1M</th><th>Output $/1M</th></tr></thead>
                <tbody>
                  {(prices.overrides || []).map((p) => (
                    <tr key={p.model}>
                      <td className="mono">{p.model}</td>
                      <td className="mono">${(p.in_micro / 1e6).toFixed(2)}</td>
                      <td className="mono">${(p.out_micro / 1e6).toFixed(2)}</td>
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
          {payouts.length === 0 ? (
            <p className="muted">no payout requests</p>
          ) : (
            <table>
              <thead><tr><th>ID</th><th>User</th><th>Amount</th><th>Status</th><th>Actions</th></tr></thead>
              <tbody>
                {payouts.map((p) => (
                  <tr key={p.id}>
                    <td className="mono">#{p.id}</td>
                    <td className="mono">user #{p.user_id}</td>
                    <td className="mono">{fmtUSD(p.amount_micro)}</td>
                    <td><span className={`badge ${p.status === "paid" ? "ok" : "warn"}`}>{p.status}</span></td>
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
