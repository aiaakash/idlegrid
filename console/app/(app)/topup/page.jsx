"use client";

import { useEffect, useState } from "react";

const fmtUSD = (micro) => `$${(micro / 1_000_000).toFixed(4)}`;

export default function TopupPage() {
  const [amount, setAmount] = useState("10");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [me, setMe] = useState(null);

  useEffect(() => {
    fetch("/api/console/me").then((r) => r.json()).then(setMe).catch(() => {});
  }, []);

  async function topup(e) {
    e.preventDefault();
    setBusy(true); setErr(""); setMsg("");
    const res = await fetch("/api/console/topup", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ amount_usd: parseFloat(amount) }),
    });
    setBusy(false);
    const j = await res.json().catch(() => ({}));
    if (res.ok && j.payment_link) {
      window.location.href = j.payment_link; // Dodo checkout
      return;
    }
    setErr(j.error || "top-up failed");
  }

  return (
    <>
      <div className="card">
        <h2>Add credit</h2>
        <p className="muted" style={{ marginBottom: 12 }}>
          Prepaid credit is consumed per token. Payments are processed by Dodo
          Payments (cards + global methods, taxes handled at checkout).
        </p>
        <form onSubmit={topup} className="row">
          <input
            type="number" step="1" min="5"
            placeholder="amount in USD (min 5)"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            style={{ flex: 1 }}
            required
          />
          <button disabled={busy}>{busy ? "Redirecting…" : "Top up"}</button>
        </form>
        <div className="row" style={{ marginTop: 10 }}>
          {[5, 10, 25, 50].map((v) => (
            <button key={v} className="ghost" onClick={() => setAmount(String(v))}>${v}</button>
          ))}
        </div>
        {err && <div className="err">{err}</div>}
        {msg && <div className="ok">{msg}</div>}
      </div>

      {me && (
        <div className="card">
          <h2>Current balance</h2>
          <div className="stat">
            <div className="value">{fmtUSD(me.developer_balance_micro || 0)}</div>
            <p className="muted" style={{ marginTop: 8 }}>
              Requests settle automatically against this balance. When it runs
              dry, inference returns 402 until you top up.
            </p>
          </div>
        </div>
      )}
    </>
  );
}
