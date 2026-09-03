"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";

export default function LoginPage() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [showPw, setShowPw] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await fetch("/api/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      if (res.ok) {
        router.push("/");
        router.refresh();
      } else if (res.status === 429) {
        setError("too many attempts — wait a minute and retry");
      } else {
        const j = await res.json().catch(() => ({}));
        setError(j.error || "login failed");
      }
    } catch {
      setError("cannot reach the console — check your connection and retry");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="center">
      <form className="card loginbox" onSubmit={submit}>
        <h1 style={{ fontSize: 20, marginBottom: 4 }}>
          <span style={{ background: "linear-gradient(90deg,#6c7bff,#22d3ee)", WebkitBackgroundClip: "text", backgroundClip: "text", color: "transparent" }}>idlegrid</span> console
        </h1>
        <p className="muted" style={{ marginBottom: 18 }}>private inference on idle Macs</p>
        <div className="field">
          <label htmlFor="email">Email</label>
          <input id="email" name="email" type="email" autoComplete="email"
            value={email} onChange={(e) => setEmail(e.target.value)}
            required autoFocus disabled={busy} />
        </div>
        <div className="field">
          <label htmlFor="password">Password</label>
          <div className="row">
            <input id="password" name="password" type={showPw ? "text" : "password"}
              autoComplete="current-password"
              value={password} onChange={(e) => setPassword(e.target.value)}
              required disabled={busy} style={{ flex: 1 }} />
            <button type="button" className="ghost" onClick={() => setShowPw((v) => !v)}
              aria-pressed={showPw} aria-label={showPw ? "Hide password" : "Show password"}>
              {showPw ? "Hide" : "Show"}
            </button>
          </div>
        </div>
        <button disabled={busy} style={{ width: "100%" }}>{busy ? "Signing in…" : "Sign in"}</button>
        {error && <div className="err" role="alert">{error}</div>}
        <p className="hint">Invite-only during beta — ask your admin for an account.</p>
      </form>
    </div>
  );
}
