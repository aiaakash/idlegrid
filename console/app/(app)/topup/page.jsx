"use client";

import { useEffect, useState } from "react";
import { fmtUSD } from "@/lib/format";

const QUICK = [5, 10, 25, 50];

export default function TopupPage() {
  const [amount, setAmount] = useState("10");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [me, setMe] = useState(null);

  useEffect(() => {
    fetch("/api/console/me").then((r) => r.json()).then(setMe).catch(() => {});
  }, []);

  const amountNum = parseFloat(amount);
  const amountValid = Number.isFinite(amountNum) && amountNum >= 5;

  async function topup(e) {
    e.preventDefault();
    setErr("");
    if (!amountValid) {
      setErr("minimum top-up is $5");
      return;
    }
    setBusy(true);
    try {
      const res = await fetch("/api/console/topup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ amount_usd: amountNum }),
      });
      const j = await res.json().catch(() => ({}));
      if (res.ok && j.payment_link) {
        window.location.href = j.payment_link; // Dodo checkout
        return;
      }
      setErr(j.error || "top-up failed");
    } catch {
      setErr("cannot reach the console — check your connection and retry");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="card">
        <h2>Add credit</h2>
        <p className="muted" style={{ marginBottom: 12 }}>
          Prepaid credit is consumed per token — $0.05 / 1M input, $0.20 / 1M
          output unless a model lists its own price. Payments are processed by
          Dodo Payments (cards + global methods, taxes handled at checkout).
        </p>
        <form onSubmit={topup}>
          <div className="field">
            <label htmlFor="amount">Amount in USD (min $5)</label>
            <div className="row">
              <input
                id="amount"
                name="amount"
                type="number" step="0.01" min="5" max="10000"
                inputMode="decimal" autoComplete="off"
                placeholder="amount in USD (min 5)"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                style={{ flex: 1 }}
                required
                disabled={busy}
              />
              <button disabled={busy || !amountValid}>{busy ? "Redirecting…" : "Top up"}</button>
            </div>
          </div>
          <div className="row" role="group" aria-label="Quick amounts" style={{ marginTop: 10 }}>
            {QUICK.map((v) => (
              <button key={v} type="button" className="ghost"
                aria-pressed={String(v) === String(Math.round(amountNum))}
                onClick={() => setAmount(String(v))}>${v}</button>
            ))}
          </div>
        </form>
        <p className="hint">You leave for Dodo Checkout to pay — credit lands automatically on return.</p>
        {err && <div className="err" role="alert">{err}</div>}
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
