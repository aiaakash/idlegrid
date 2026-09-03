"use client";

import { useEffect, useMemo, useState } from "react";
import { fmtUSD, ago, fmtDateTime } from "@/lib/format";

const TABS = ["users", "prices", "payouts"];

export default function AdminPage() {
  const [tab, setTab] = useState("users");
  const [users, setUsers] = useState(null);
  const [prices, setPrices] = useState(null);
  const [payouts, setPayouts] = useState(null);
  const [loadErr, setLoadErr] = useState("");
  const [msg, setMsg] = useState(null); // { ok, text } shown per-tab
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmPayout, setConfirmPayout] = useState(null); // {id, action}
  const [railRef, setRailRef] = useState("");

  async function load() {
    setLoadErr("");
    try {
      const [u, p, po] = await Promise.all([
        fetch("/api/console/admin/users").then((r) => r.json()),
        fetch("/api/console/admin/prices").then((r) => r.json()),
        fetch("/api/console/admin/payouts").then((r) => r.json()),
      ]);
      setUsers(u.users || []);
      setPrices(p);
      setPayouts(po.payouts || []);
    } catch {
      setLoadErr("failed to load admin data — retry");
    }
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
    try {
      const res = await fetch("/api/console/admin/users", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: f.email.value.trim(), password: f.password.value, role: f.role.value }),
      });
      const j = await res.json();
      flash(res.ok, res.ok ? `user #${j.id} (${j.email}) created` : j.error || "failed");
      if (res.ok) { f.reset(); load(); }
    } catch {
      flash(false, "cannot reach the console");
    } finally {
      setBusy(false);
    }
  }

  async function setPrice(e) {
    e.preventDefault();
    const f = e.target;
    setBusy(true);
    try {
      const res = await fetch("/api/console/admin/prices", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          model: f.model.value.trim(),
          input_micro_per_1m: Math.round(parseFloat(f.input.value) * 1_000_000),
          output_micro_per_1m: Math.round(parseFloat(f.output.value) * 1_000_000),
        }),
      });
      const j = await res.json();
      flash(res.ok, res.ok ? `price set for ${f.model.value.trim()}` : j.error || "failed");
      if (res.ok) { f.reset(); load(); }
    } catch {
      flash(false, "cannot reach the console");
    } finally {
      setBusy(false);
    }
  }

  async function payoutAction(id, action) {
    let rail = "manual";
    let ref = "";
    if (action === "markpaid") {
      ref = railRef.trim();
    }
    setConfirmPayout(null);
    setRailRef("");
    try {
      const res = await fetch("/api/console/admin/payouts", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id, action, rail, rail_ref: ref }),
      });
      const j = await res.json().catch(() => ({}));
      flash(res.ok, res.ok ? `payout #${id} ${action === "approve" ? "approved" : "marked paid"}` : j.error || "failed");
    } catch {
      flash(false, "cannot reach the console");
    } finally {
      load();
    }
  }

  const userEmail = useMemo(() => {
    const m = new Map();
    for (const u of users || []) m.set(u.id, u.email);
    return m;
  }, [users]);

  const filteredUsers = useMemo(() => {
    if (!users) return [];
    const q = query.trim().toLowerCase();
    if (!q) return users;
    return users.filter((u) => u.email.toLowerCase().includes(q) || u.role.includes(q) || String(u.id) === q);
  }, [users, query]);

  const pendingPayouts = (payouts || []).filter((p) => p.status !== "paid");
  const shownPayouts = useMemo(() => {
    if (!payouts) return [];
    const q = query.trim().toLowerCase();
    if (!q) return [...pendingPayouts, ...payouts.filter((p) => p.status === "paid")];
    return payouts.filter((p) =>
      p.status.includes(q) ||
      String(p.user_id) === query.trim() ||
      String(p.id) === query.trim() ||
      (userEmail.get(p.user_id) || "").toLowerCase().includes(q)
    );
  }, [payouts, query, pendingPayouts, userEmail]);

  return (
    <>
      <div className="row" role="tablist" aria-label="Admin sections"
        style={{ marginBottom: 16, borderBottom: "1px solid var(--border)", paddingBottom: 10 }}>
        {TABS.map((t) => (
          <button
            key={t}
            role="tab"
            aria-selected={tab === t}
            type="button"
            className={`ghost ${tab === t ? "selected" : ""}`}
            onClick={() => { setTab(t); setMsg(null); setQuery(""); }}
            style={tab !== t ? { border: "none" } : undefined}
          >
            {t}{t === "payouts" && pendingPayouts.length > 0 ? ` (${pendingPayouts.length})` : ""}
          </button>
        ))}
        <div className="spacer" style={{ flex: 1 }} />
        {tab !== "prices" && (
          <input
            placeholder={tab === "users" ? "filter by email / role / id" : "filter by status / user / id"}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            style={{ width: 230 }}
            aria-label={tab === "users" ? "Filter users" : "Filter payouts"}
          />
        )}
        {msg && <span className={msg.ok ? "ok" : "err"} role="alert" style={{ marginTop: 0 }}>{msg.text}</span>}
      </div>
      {loadErr && (
        <div className="banner warn" role="alert">
          <span>{loadErr}</span>
          <div className="spacer" style={{ flex: 1 }} />
          <button type="button" className="ghost" onClick={load}>Retry</button>
        </div>
      )}

      {tab === "users" && (
        <>
          <div className="card">
            <h2>Create user</h2>
            <form onSubmit={createUser} className="row" style={{ flexWrap: "wrap" }}>
              <input name="email" type="email" autoComplete="email" placeholder="email" style={{ flex: 1, minWidth: 180 }} required disabled={busy} />
              <input name="password" placeholder="password (min 8)" type="password" autoComplete="new-password" required minLength={8} disabled={busy} />
              <select name="role" defaultValue="developer" disabled={busy} aria-label="Role">
                <option value="developer">developer</option>
                <option value="provider_owner">provider_owner</option>
                <option value="admin">admin</option>
              </select>
              <button disabled={busy}>{busy ? "Creating…" : "Create"}</button>
            </form>
          </div>
          <div className="card">
            <h2>Users <span className="muted" style={{ fontWeight: 400 }}>({filteredUsers.length})</span></h2>
            {users === null ? (
              <div className="card skeleton" style={{ height: 120 }} aria-label="Loading users" />
            ) : filteredUsers.length === 0 ? (
              <p className="muted">{query ? "no users match the filter" : "no users yet"}</p>
            ) : (
              <div className="tablewrap">
                <table>
                  <thead><tr><th>ID</th><th>Email</th><th>Role</th><th style={{ textAlign: "right" }}>Balance</th><th>Created</th></tr></thead>
                  <tbody>
                    {filteredUsers.map((u) => (
                      <tr key={u.id}>
                        <td className="mono">#{u.id}</td>
                        <td>{u.email}</td>
                        <td><span className={`badge ${u.role === "admin" ? "warn" : ""}`}>{u.role}</span></td>
                        <td className="num">{fmtUSD(u.developer_balance_micro)}</td>
                        <td className="muted" title={fmtDateTime(u.created_at)}>{ago(u.created_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </>
      )}

      {tab === "prices" && (
        <>
          <div className="card">
            <h2>Set model price (USD per 1M tokens)</h2>
            <form onSubmit={setPrice} className="row" style={{ flexWrap: "wrap" }}>
              <input name="model" placeholder="model id" style={{ flex: 1, minWidth: 160 }} required disabled={busy} autoComplete="off" />
              <input name="input" placeholder="input $" type="number" step="0.01" min="0.01" required disabled={busy} />
              <input name="output" placeholder="output $" type="number" step="0.01" min="0.01" required disabled={busy} />
              <button disabled={busy}>{busy ? "Saving…" : "Set"}</button>
            </form>
          </div>
          <div className="card">
            <h2>Default rates</h2>
            {(prices?.defaults || []).length === 0 ? (
              <p className="muted">defaults unavailable</p>
            ) : (
              <div className="tablewrap">
                <table>
                  <thead><tr><th>Model</th><th style={{ textAlign: "right" }}>Input $/1M</th><th style={{ textAlign: "right" }}>Output $/1M</th></tr></thead>
                  <tbody>
                    {(prices.defaults || []).map((p) => (
                      <tr key={p.model}>
                        <td className="mono">{p.model}</td>
                        <td className="num">${(p.input_micro_per_1m / 1e6).toFixed(2)}</td>
                        <td className="num">${(p.output_micro_per_1m / 1e6).toFixed(2)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
          <div className="card">
            <h2>Overrides</h2>
            {!prices ? (
              <div className="card skeleton" style={{ height: 80 }} aria-label="Loading prices" />
            ) : prices.overrides?.length === 0 ? (
              <p className="muted">no overrides — default rates apply ($0.05 in / $0.20 out per 1M)</p>
            ) : (
              <div className="tablewrap">
                <table>
                  <thead><tr><th>Model</th><th style={{ textAlign: "right" }}>Input $/1M</th><th style={{ textAlign: "right" }}>Output $/1M</th></tr></thead>
                  <tbody>
                    {(prices.overrides || []).map((p) => (
                      <tr key={p.model}>
                        <td className="mono">{p.model}</td>
                        <td className="num">${(p.input_micro_per_1m / 1e6).toFixed(2)}</td>
                        <td className="num">${(p.output_micro_per_1m / 1e6).toFixed(2)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </>
      )}

      {tab === "payouts" && (
        <div className="card">
          <h2>Payout queue</h2>
          {payouts === null ? (
            <div className="card skeleton" style={{ height: 120 }} aria-label="Loading payouts" />
          ) : shownPayouts.length === 0 ? (
            <p className="muted">{query ? "no payouts match the filter" : "no payout requests"}</p>
          ) : (
            <div className="tablewrap">
              <table>
                <thead><tr><th>ID</th><th>User</th><th style={{ textAlign: "right" }}>Amount</th><th>Status</th><th>Destination</th><th>Actions</th></tr></thead>
                <tbody>
                  {shownPayouts.map((p) => (
                    <tr key={p.id}>
                      <td className="mono">#{p.id}</td>
                      <td className="mono" title={userEmail.get(p.user_id) || ""}>
                        {userEmail.get(p.user_id) || `user #${p.user_id}`}
                      </td>
                      <td className="num">{fmtUSD(p.amount_micro)}</td>
                      <td><span className={`badge ${p.status === "paid" ? "ok" : p.status === "approved" ? "" : "warn"}`}>{p.status}</span></td>
                      <td className="muted mono">{p.rail}{p.rail_ref ? ` · ${p.rail_ref}` : " —"}</td>
                      <td>
                        {confirmPayout?.id === p.id ? (
                          <span className="confirmbar" style={{ justifyContent: "flex-start", flexWrap: "wrap" }}>
                            {confirmPayout.action === "markpaid" && (
                              <input
                                placeholder="Payment ref (txn / UPI ref), optional"
                                value={railRef}
                                onChange={(e) => setRailRef(e.target.value)}
                                style={{ minWidth: 200 }}
                                autoFocus
                              />
                            )}
                            <button type="button" className="ghost"
                              onClick={() => payoutAction(p.id, confirmPayout.action)}>
                              Confirm {confirmPayout.action === "approve" ? "approve" : "mark paid"}
                            </button>
                            <button type="button" className="ghost" onClick={() => { setConfirmPayout(null); setRailRef(""); }}>
                              Cancel
                            </button>
                          </span>
                        ) : (
                          <span className="row">
                            {p.status === "requested" && (
                              <button type="button" className="ghost" onClick={() => setConfirmPayout({ id: p.id, action: "approve" })}>Approve</button>
                            )}
                            {p.status !== "paid" && (
                              <button type="button" className="ghost" onClick={() => setConfirmPayout({ id: p.id, action: "markpaid" })}>Mark paid</button>
                            )}
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
      )}
    </>
  );
}
