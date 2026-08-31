"use client";

import { useEffect, useState } from "react";

// Approve a provider device login (`idlegrid-provider login` on the Mac
// prints the code). Approving binds that Mac's token to this account.
export default function LinkPage() {
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  useEffect(() => {
    const q = new URLSearchParams(window.location.search).get("code");
    if (q) setCode(q.toUpperCase());
  }, []);

  async function approve(e) {
    e.preventDefault();
    setBusy(true); setErr(""); setMsg("");
    const res = await fetch("/api/console/device/approve", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ user_code: code }),
    });
    setBusy(false);
    const j = await res.json().catch(() => ({}));
    if (res.ok) {
      setMsg("Mac linked to your account — you can return to the terminal.");
      setCode("");
    } else {
      setErr(j.error || "approval failed");
    }
  }

  return (
    <div className="card">
      <h2>Link a Mac</h2>
      <p className="muted" style={{ marginBottom: 12 }}>
        Run <span className="mono">idlegrid-provider login</span> on the Mac
        you want to enroll, then enter the code it shows here. The Mac receives
        a token bound to this account — earnings flow to you.
      </p>
      <form onSubmit={approve} className="row">
        <input
          className="mono"
          value={code}
          onChange={(e) => setCode(e.target.value.toUpperCase())}
          placeholder="ABCD-2345"
          maxLength={9}
          style={{ flex: 1, letterSpacing: 2 }}
          required
        />
        <button disabled={busy}>{busy ? "Approving…" : "Approve & link"}</button>
      </form>
      {err && <div className="err">{err}</div>}
      {msg && <div className="ok">{msg}</div>}
    </div>
  );
}
