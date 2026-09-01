"use client";

import { useEffect, useState } from "react";

const fmtUSD = (micro) => `$${(micro / 1_000_000).toFixed(4)}`;

export default function PayoutsPage() {
  const [payouts, setPayouts] = useState([]);
  const [me, setMe] = useState(null);
  const [amount, setAmount] = useState("");
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    const res = await fetch("/api/console/payouts");
    const j = await res.json();
    setPayouts(j.payouts || []);
    fetch("/api/console/me").then((r) => r.json()).then(setMe).catch(() => {});
  }
  useEffect(() => { load(); }, []);

  const available = me?.provider_earnings_micro || 0;

  async function request(e) {
    e.preventDefault();
    setMsg("");
    const dollars = parseFloat(amount);
    if (!dollars || dollars < 10) return setMsg("minimum payout is $10");
    if (dollars * 1_000_000 > available) return setMsg("amount exceeds your available earnings");
    setBusy(true);
    const res = await fetch("/api/console/payout-request", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ amount_micro: Math.round(dollars * 1_000_000) }),
    });
    setBusy(false);
    const j = await res.json();
    setMsg(res.ok ? "payout requested — the platform admin will process it" : j.error || "failed");
    if (res.ok) setAmount("");
    load();
  }

  return (
    <>
      <div className="card">
        <h2>Request a payout</h2>
        <p className="muted" style={{ marginBottom: 12 }}>
          Payouts are processed manually by the platform admin (PayPal / Wise / UPI)
          while automated rails are being integrated. Minimum $10.
        </p>
        {me && (
          <p className="muted" style={{ marginBottom: 10 }}>
            Available: <span className="mono" style={{ color: "var(--green)" }}>{fmtUSD(available)}</span>
          </p>
        )}
        <form onSubmit={request} className="row">
          <input
            placeholder="amount in USD, e.g. 25"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            style={{ flex: 1 }}
          />
          <button type="button" className="ghost" onClick={() => setAmount((available / 1_000_000).toFixed(2))}>
            Max
          </button>
          <button disabled={busy}>{busy ? "Requesting…" : "Request payout"}</button>
        </form>
        {msg && <div className={msg.includes("requested") ? "ok" : "err"}>{msg}</div>}
      </div>

      <div className="card">
        <h2>Your payouts</h2>
        {payouts.length === 0 ? (
          <p className="muted">no payouts yet</p>
        ) : (
          <table>
            <thead><tr><th>ID</th><th>Amount</th><th>Status</th><th>Rail</th><th>Requested</th></tr></thead>
            <tbody>
              {payouts.map((p) => (
                <tr key={p.id}>
                  <td className="mono">#{p.id}</td>
                  <td className="mono">{fmtUSD(p.amount_micro)}</td>
                  <td><span className={`badge ${p.status === "paid" ? "ok" : "warn"}`}>{p.status}</span></td>
                  <td className="muted">{p.rail}{p.rail_ref ? ` (${p.rail_ref})` : ""}</td>
                  <td className="muted">{new Date(p.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
