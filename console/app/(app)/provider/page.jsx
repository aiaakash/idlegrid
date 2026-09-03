"use client";

import { useEffect, useState } from "react";
import CopyButton from "@/components/CopyButton";
import { fmtUSD, ago } from "@/lib/format";

const INSTALL_STEP1 = "curl -fsSL https://raw.githubusercontent.com/aiaakash/idlegrid/main/deploy/install.sh | bash";
const INSTALL_STEP2 = "~/.idlegrid/bin/idlegrid-provider login";
const INSTALL_CMD = `${INSTALL_STEP1}\n${INSTALL_STEP2}`;
const ONLINE_MS = 30_000; // polling is 10s — 30s threshold avoids flappy online/offline

export default function ProviderPage() {
  const [code, setCode] = useState(null);
  const [codeFailed, setCodeFailed] = useState(false);
  const [instructions, setInstructions] = useState("");
  const [me, setMe] = useState(null);
  const [nodes, setNodes] = useState(null);
  const [confirmRemove, setConfirmRemove] = useState(null);

  const loadNodes = () =>
    fetch("/api/console/nodes").then((r) => r.json()).then((j) => setNodes(j.nodes || [])).catch(() => {});

  useEffect(() => {
    fetch("/api/console/enrollment").then((r) => r.json()).then((j) => {
      setCode(j.enrollment_code || null);
      setInstructions(j.instructions || "");
      if (!j.enrollment_code) setCodeFailed(true);
    }).catch(() => setCodeFailed(true));
    fetch("/api/console/me").then((r) => r.json()).then(setMe).catch(() => {});
    loadNodes();
    const t = setInterval(() => {
      if (!document.hidden) loadNodes();
    }, 10000);
    const onVis = () => { if (!document.hidden) loadNodes(); };
    document.addEventListener("visibilitychange", onVis);
    return () => { clearInterval(t); document.removeEventListener("visibilitychange", onVis); };
  }, []);

  async function revokeNode(nodeID) {
    setConfirmRemove(null);
    const res = await fetch("/api/console/nodes/revoke", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ node_id: nodeID }),
    });
    if (res.ok) loadNodes();
  }

  const onlineCount = (nodes || []).filter(
    (n) => n.last_seen && Date.now() - new Date(n.last_seen).getTime() < ONLINE_MS
  ).length;

  return (
    <>
      <div className="card">
        <h2>Enroll a Mac to your account</h2>
        <p className="muted" style={{ marginBottom: 12 }}>
          Enrolled Macs earn <b>directly to your account</b> (90% of every
          request they serve). On the Mac you want to enroll, run each line:
        </p>
        <div className="copychip" style={{ marginBottom: 8 }}>
          <span className="mono">{INSTALL_STEP1}</span>
          <CopyButton text={INSTALL_STEP1} label="Copy" />
        </div>
        <div className="copychip">
          <span className="mono">{INSTALL_STEP2}</span>
          <CopyButton text={INSTALL_CMD} label="Copy both" />
        </div>
        <p className="muted" style={{ marginTop: 8 }}>
          <span className="mono">login</span> shows a short code — approve it
          on the <a href="/link">Link Mac</a> page and the Mac is enrolled.
          No codes to copy.
        </p>
        <details style={{ marginTop: 12 }}>
          <summary className="muted">Legacy: manual install with enrollment code</summary>
          {codeFailed ? (
            <p className="err" style={{ marginTop: 8 }}>could not load enrollment code — reload the page to retry</p>
          ) : (
            <>
              <p className="mono" style={{ background: "var(--panel2)", padding: "10px 12px", borderRadius: 8, wordBreak: "break-all", marginTop: 8 }}>
                curl -fsSL https://raw.githubusercontent.com/aiaakash/idlegrid/main/deploy/install.sh | bash -s -- \<br />
                &nbsp;&nbsp;--code &lt;network-join-code&gt; --enroll-code <b>{code || "…"}</b>
              </p>
              <div style={{ marginTop: 10 }}>
                <CopyButton text={code ? `--enroll-code ${code}` : ""} label="Copy enroll code" />
              </div>
            </>
          )}
        </details>
        {code && instructions && <p className="muted" style={{ marginTop: 10 }}>{instructions}</p>}
      </div>

      {me ? (
        <div className="card stat">
          <div className="label">Your provider earnings</div>
          <div className="value">{fmtUSD(me.provider_earnings_micro || 0)}</div>
          <p className="muted" style={{ marginTop: 8 }}>
            90% of every request served by your enrolled Macs.{" "}
            <a href="/payouts">Request a payout →</a>
          </p>
        </div>
      ) : (
        <div className="card skeleton" style={{ height: 84 }} aria-label="Loading earnings" />
      )}

      <div className="card">
        <div className="row" style={{ marginBottom: 12 }}>
          <h2 style={{ margin: 0 }}>Your enrolled Macs{nodes ? ` (${onlineCount} online)` : ""}</h2>
          <div className="spacer" style={{ flex: 1 }} />
          <button type="button" className="ghost" onClick={loadNodes}>Refresh</button>
        </div>
        {nodes === null ? (
          <div className="card skeleton" style={{ height: 80 }} aria-label="Loading nodes" />
        ) : nodes.length === 0 ? (
          <div>
            <p className="muted">no Macs enrolled yet — run the install + login above</p>
            <div className="steps" aria-label="Enroll progress">
              <span className="step">1 · Install provider</span>
              <span className="step">2 · <a href="/link">Approve code</a></span>
              <span className="step">3 · Serve first request</span>
            </div>
          </div>
        ) : (
          <div className="tablewrap">
            <table>
              <thead><tr><th>Node</th><th>Status</th><th>Last seen</th><th style={{ textAlign: "right" }}>Errors</th><th><span style={{ display: "none" }}>Actions</span></th></tr></thead>
              <tbody>
                {nodes.map((n) => {
                  const online = n.last_seen && (Date.now() - new Date(n.last_seen).getTime()) < ONLINE_MS;
                  return (
                    <tr key={n.node_id}>
                      <td className="mono">{n.node_id}</td>
                      <td><span className={`badge ${online ? "ok" : ""}`}>{online ? "online" : "offline"}</span></td>
                      <td className="muted" title={n.last_seen ? new Date(n.last_seen).toLocaleString() : ""}>{ago(n.last_seen)}</td>
                      <td className="num">{n.error_count}</td>
                      <td style={{ textAlign: "right" }}>
                        {confirmRemove !== n.node_id ? (
                          <button className="danger" onClick={() => setConfirmRemove(n.node_id)}>Remove</button>
                        ) : (
                          <span className="confirmbar">
                            <span className="muted">Remove? It stops earning.</span>
                            <button className="danger" onClick={() => revokeNode(n.node_id)}>Yes, remove</button>
                            <button className="ghost" onClick={() => setConfirmRemove(null)}>Keep</button>
                          </span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        <p className="muted" style={{ marginTop: 10 }}>
          Removing a Mac unbinds it and revokes its login token — it cannot
          re-enroll without a fresh <span className="mono">idlegrid-provider login</span>.
        </p>
      </div>

      <div className="card">
        <div className="row">
          <h2 style={{ margin: 0 }}>Payouts</h2>
          <div className="spacer" style={{ flex: 1 }} />
          <a href="/payouts" className="muted">Manage payouts →</a>
        </div>
        <p className="muted" style={{ marginTop: 8 }}>
          Earnings, history, and payout requests live on the{" "}
          <a href="/payouts">Payouts page</a>.
        </p>
      </div>
    </>
  );
}
