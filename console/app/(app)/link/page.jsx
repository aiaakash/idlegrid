"use client";

import { useEffect, useState } from "react";

// Approve a provider device login (`idlegrid-provider login` on the Mac
// prints the code). Approving binds that Mac's token to this account.
function formatCode(raw) {
  const clean = raw.toUpperCase().replace(/[^A-Z0-9]/g, "").slice(0, 8);
  if (clean.length <= 4) return clean;
  return `${clean.slice(0, 4)}-${clean.slice(4)}`;
}

export default function LinkPage() {
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  useEffect(() => {
    const q = new URLSearchParams(window.location.search).get("code");
    if (q) setCode(formatCode(q));
  }, []);

  const valid = /^[A-Z0-9]{4}-[A-Z0-9]{4}$/.test(code);

  async function approve(e) {
    e.preventDefault();
    setBusy(true); setErr(""); setMsg("");
    try {
      const res = await fetch("/api/console/device/approve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ user_code: code }),
      });
      const j = await res.json().catch(() => ({}));
      if (res.ok) {
        setMsg("Mac linked to your account — you can return to the terminal.");
        setCode("");
      } else {
        setErr(j.error || "approval failed — check the code and retry (codes expire after ~10 min)");
      }
    } catch {
      setErr("cannot reach the console — check your connection and retry");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card">
      <h2>Link a Mac</h2>
      <p className="muted" style={{ marginBottom: 12 }}>
        Run <span className="mono">idlegrid-provider login</span> on the Mac
        you want to enroll, then enter the code it shows here. The Mac receives
        a token bound to this account — earnings flow to you. Codes expire
        after about 10 minutes.
      </p>
      <form onSubmit={approve}>
        <div className="field">
          <label htmlFor="linkcode">Device code</label>
          <div className="row">
            <input
              id="linkcode"
              className="mono"
              value={code}
              onChange={(e) => setCode(formatCode(e.target.value))}
              placeholder="ABCD-2345"
              maxLength={9}
              style={{ flex: 1, letterSpacing: 2, textTransform: "uppercase" }}
              required
              disabled={busy}
              autoComplete="one-time-code"
              inputMode="text"
              aria-describedby="linkcode-hint"
            />
            <button disabled={busy || !valid}>{busy ? "Approving…" : "Approve & link"}</button>
          </div>
          <p className="hint" id="linkcode-hint">Format: 4 characters, dash, 4 characters. Dashes are added automatically.</p>
        </div>
      </form>
      {err && <div className="err" role="alert">{err}</div>}
      {msg && (
        <div className="ok" role="status">
          {msg} <a href="/provider">Check your Macs →</a>
        </div>
      )}
    </div>
  );
}
