"use client";

import { useEffect, useState } from "react";

const fmtUSD = (micro) => `$${(micro / 1_000_000).toFixed(4)}`;

export default function ProviderPage() {
  const [code, setCode] = useState(null);
  const [instructions, setInstructions] = useState("");
  const [me, setMe] = useState(null);
  const [payouts, setPayouts] = useState([]);

  useEffect(() => {
    fetch("/api/console/enrollment").then((r) => r.json()).then((j) => {
      setCode(j.enrollment_code);
      setInstructions(j.instructions);
    }).catch(() => {});
    fetch("/api/console/me").then((r) => r.json()).then(setMe).catch(() => {});
    fetch("/api/console/payouts").then((r) => r.json()).then((j) => setPayouts(j.payouts || [])).catch(() => {});
  }, []);

  return (
    <>
      <div className="card">
        <h2>Enroll a Mac to your account</h2>
        <p className="muted" style={{ marginBottom: 12 }}>
          Enrolled Macs earn <b>directly to your account</b> (90% of every
          request they serve). On the Mac you want to enroll:
        </p>
        <p className="mono" style={{ background: "var(--panel2)", padding: "10px 12px", borderRadius: 8, wordBreak: "break-all" }}>
          curl -fsSL https://raw.githubusercontent.com/aiaakash/idlegrid/main/deploy/install.sh | bash<br/>
          ~/.idlegrid/bin/idlegrid-provider login
        </p>
        <p className="muted" style={{ marginTop: 8 }}>
          <span className="mono">login</span> shows a short code — approve it
          on the <a href="/link">Link a Mac</a> page and the Mac is enrolled.
          No codes to copy.
        </p>
        <details style={{ marginTop: 12 }}>
          <summary className="muted">Legacy: manual install with enrollment code</summary>
          <p className="mono" style={{ background: "var(--panel2)", padding: "10px 12px", borderRadius: 8, wordBreak: "break-all", marginTop: 8 }}>
            curl -fsSL https://raw.githubusercontent.com/aiaakash/idlegrid/main/deploy/install.sh | bash -s -- \<br/>
            &nbsp;&nbsp;--code &lt;network-join-code&gt; --enroll-code <b>{code || "…"}</b>
          </p>
          <button
            className="ghost"
            style={{ marginTop: 10 }}
            onClick={() => navigator.clipboard.writeText(`--enroll-code ${code}`)}
          >
            Copy enroll code
          </button>
        </details>
        {code && instructions && <p className="muted" style={{ marginTop: 10 }}>{instructions}</p>}
      </div>

      {me && (
        <div className="card stat">
          <div className="label">Your provider earnings</div>
          <div className="value">{fmtUSD(me.provider_earnings_micro || 0)}</div>
          <p className="muted" style={{ marginTop: 8 }}>
            90% of every request served by your enrolled Macs. Request a payout
            from the Payouts page.
          </p>
        </div>
      )}

      <div className="card">
        <h2>Your payouts</h2>
        {payouts.length === 0 ? (
          <p className="muted">no payouts yet</p>
        ) : (
          <table>
            <thead><tr><th>ID</th><th>Amount</th><th>Status</th></tr></thead>
            <tbody>
              {payouts.map((p) => (
                <tr key={p.id}>
                  <td className="mono">#{p.id}</td>
                  <td className="mono">{fmtUSD(p.amount_micro)}</td>
                  <td><span className={`badge ${p.status === "paid" ? "ok" : "warn"}`}>{p.status}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
