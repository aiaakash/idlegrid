"use client";

import { useEffect, useState } from "react";
import CopyButton from "@/components/CopyButton";

const INSTALL_CMD = "curl -fsSL https://raw.githubusercontent.com/aiaakash/idlegrid/main/deploy/install.sh | bash\n~/.idlegrid/bin/idlegrid-provider login";

const fmtUSD = (micro) => `$${(micro / 1_000_000).toFixed(4)}`;

const ago = (ts) => {
  if (!ts) return "never";
  const s = Math.floor((Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 5) return "just now";
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
};

export default function ProviderPage() {
  const [code, setCode] = useState(null);
  const [instructions, setInstructions] = useState("");
  const [me, setMe] = useState(null);
  const [payouts, setPayouts] = useState([]);
  const [nodes, setNodes] = useState([]);

  const loadNodes = () =>
    fetch("/api/console/nodes").then((r) => r.json()).then((j) => setNodes(j.nodes || [])).catch(() => {});

  useEffect(() => {
    fetch("/api/console/enrollment").then((r) => r.json()).then((j) => {
      setCode(j.enrollment_code);
      setInstructions(j.instructions);
    }).catch(() => {});
    fetch("/api/console/me").then((r) => r.json()).then(setMe).catch(() => {});
    fetch("/api/console/payouts").then((r) => r.json()).then((j) => setPayouts(j.payouts || [])).catch(() => {});
    loadNodes();
    const t = setInterval(loadNodes, 10000); // health is live-ish
    return () => clearInterval(t);
  }, []);

  async function revokeNode(nodeID) {
    if (!confirm(`Remove ${nodeID}? Its login token is revoked — it stops earning until re-linked.`)) return;
    const res = await fetch("/api/console/nodes/revoke", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ node_id: nodeID }),
    });
    if (res.ok) loadNodes();
  }

  return (
    <>
      <div className="card">
        <h2>Enroll a Mac to your account</h2>
        <p className="muted" style={{ marginBottom: 12 }}>
          Enrolled Macs earn <b>directly to your account</b> (90% of every
          request they serve). On the Mac you want to enroll:
        </p>
        <div className="copychip">
          <span className="mono">
            curl -fsSL https://raw.githubusercontent.com/aiaakash/idlegrid/main/deploy/install.sh | bash<br/>
            ~/.idlegrid/bin/idlegrid-provider login
          </span>
          <CopyButton text={INSTALL_CMD} label="Copy" />
        </div>
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
          <div style={{ marginTop: 10 }}>
            <CopyButton text={`--enroll-code ${code || ""}`} label="Copy enroll code" />
          </div>
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
        <h2>Your enrolled Macs</h2>
        {nodes.length === 0 ? (
          <p className="muted">no Macs enrolled yet — run the install + login above</p>
        ) : (
          <table>
            <thead><tr><th>Node</th><th>Status</th><th>Last seen</th><th>Errors</th><th></th></tr></thead>
            <tbody>
              {nodes.map((n) => {
                const online = n.last_seen && (Date.now() - new Date(n.last_seen).getTime()) < 15_000;
                return (
                  <tr key={n.node_id}>
                    <td className="mono">{n.node_id}</td>
                    <td><span className={`badge ${online ? "ok" : "warn"}`}>{online ? "online" : "offline"}</span></td>
                    <td className="muted">{ago(n.last_seen)}</td>
                    <td className="mono">{n.error_count}</td>
                    <td>
                      <button className="danger" onClick={() => revokeNode(n.node_id)}>Remove</button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        <p className="muted" style={{ marginTop: 10 }}>
          Removing a Mac unbinds it and revokes its login token — it cannot
          re-enroll without a fresh <span className="mono">idlegrid-provider login</span>.
        </p>
      </div>

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
