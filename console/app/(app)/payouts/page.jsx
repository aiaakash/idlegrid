"use client";

import { useEffect, useState } from "react";
import { fmtUSD, fmtDateTime } from "@/lib/format";

const RAILS = [
  { value: "paypal", label: "PayPal", placeholder: "PayPal email, e.g. you@example.com" },
  { value: "wise", label: "Wise", placeholder: "Wise email / account ID" },
  { value: "upi", label: "UPI", placeholder: "UPI ID, e.g. name@okbank" },
];

export default function PayoutsPage() {
  const [payouts, setPayouts] = useState(null);
  const [me, setMe] = useState(null);
  const [amount, setAmount] = useState("");
  const [rail, setRail] = useState("paypal");
  const [destination, setDestination] = useState("");
  const [ok, setOk] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    try {
      const res = await fetch("/api/console/payouts");
      const j = await res.json();
      setPayouts(j.payouts || []);
    } catch {
      setPayouts([]);
    }
    fetch("/api/console/me").then((r) => r.json()).then(setMe).catch(() => {});
  }
  useEffect(() => { load(); }, []);

  const available = me?.provider_earnings_micro || 0;
  const railMeta = RAILS.find((r) => r.value === rail);

  function setMax() {
    // Keep full precision — don't strand sub-cent dust with toFixed(2).
    const v = available / 1_000_000;
    setAmount(Number.isFinite(v) ? String(Math.floor(v * 10000) / 10000) : "");
  }

  async function request(e) {
    e.preventDefault();
    setOk(""); setErr("");
    const dollars = parseFloat(amount);
    if (!dollars || !Number.isFinite(dollars) || dollars < 10) {
      setErr("minimum payout is $10");
      return;
    }
    if (dollars * 1_000_000 > available) {
      setErr("amount exceeds your available earnings");
      return;
    }
    if (!destination.trim()) {
      setErr(`enter your ${railMeta?.label || "payout"} destination so the admin knows where to send it`);
      return;
    }
    setBusy(true);
    try {
      const res = await fetch("/api/console/payout-request", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          amount_micro: Math.round(dollars * 1_000_000),
          rail,
          rail_ref: destination.trim(),
        }),
      });
      const j = await res.json().catch(() => ({}));
      if (res.ok) {
        setOk(`payout of $${dollars} requested — the admin will send it to your ${railMeta.label} destination`);
        setAmount("");
      } else {
        setErr(j.error || "payout request failed");
      }
    } catch {
      setErr("cannot reach the console — check your connection and retry");
    } finally {
      setBusy(false);
      load();
    }
  }

  return (
    <>
      <div className="card">
        <h2>Request a payout</h2>
        <p className="muted" style={{ marginBottom: 12 }}>
          Payouts are processed manually by the platform admin while automated
          rails are being integrated. Minimum $10. Status flows{" "}
          <span className="mono">requested → approved → paid</span>.
        </p>
        {me && (
          <p className="muted" style={{ marginBottom: 10 }}>
            Available: <span className="mono" style={{ color: "var(--green)" }}>{fmtUSD(available)}</span>
          </p>
        )}
        <form onSubmit={request}>
          <div className="field">
            <label htmlFor="amount">Amount in USD</label>
            <div className="row">
              <input
                id="amount"
                placeholder="amount in USD, e.g. 25"
                inputMode="decimal" autoComplete="off"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                style={{ flex: 1 }}
                disabled={busy}
              />
              <button type="button" className="ghost" onClick={setMax} disabled={busy}>
                Max
              </button>
            </div>
          </div>
          <div className="field">
            <label htmlFor="rail">Rail</label>
            <div className="row">
              <select id="rail" value={rail} onChange={(e) => setRail(e.target.value)}
                disabled={busy} style={{ flex: 1 }}>
                {RAILS.map((r) => (
                  <option key={r.value} value={r.value}>{r.label}</option>
                ))}
              </select>
            </div>
          </div>
          <div className="field">
            <label htmlFor="destination">{railMeta?.label} destination</label>
            <input
              id="destination"
              placeholder={railMeta?.placeholder}
              value={destination}
              onChange={(e) => setDestination(e.target.value)}
              disabled={busy}
              autoComplete="off"
              required
            />
          </div>
          <button disabled={busy}>{busy ? "Requesting…" : "Request payout"}</button>
        </form>
        {ok && <div className="ok" role="status">{ok}</div>}
        {err && <div className="err" role="alert">{err}</div>}
      </div>

      <div className="card">
        <h2>Your payouts</h2>
        {payouts === null ? (
          <div className="card skeleton" style={{ height: 80 }} aria-label="Loading payouts" />
        ) : payouts.length === 0 ? (
          <p className="muted">no payouts yet</p>
        ) : (
          <div className="tablewrap">
            <table>
              <thead><tr><th>ID</th><th style={{ textAlign: "right" }}>Amount</th><th>Status</th><th>Destination</th><th>Requested</th></tr></thead>
              <tbody>
                {payouts.map((p) => (
                  <tr key={p.id}>
                    <td className="mono">#{p.id}</td>
                    <td className="num">{fmtUSD(p.amount_micro)}</td>
                    <td><span className={`badge ${p.status === "paid" ? "ok" : p.status === "approved" ? "" : "warn"}`}>{p.status}</span></td>
                    <td className="muted mono">{p.rail}{p.rail_ref ? ` · ${p.rail_ref}` : ""}</td>
                    <td className="muted" title={fmtDateTime(p.created_at)}>{fmtDateTime(p.created_at)}</td>
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
