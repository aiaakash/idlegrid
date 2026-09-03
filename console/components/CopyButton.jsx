"use client";

import { useState } from "react";

// Copy-to-clipboard button with "Copied!" feedback. ghost-styled.
export default function CopyButton({ text, label = "Copy" }) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);

  async function copy() {
    setFailed(false);
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        throw new Error("no clipboard API");
      }
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Fallback for non-secure contexts (http) / old browsers.
      try {
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        const ok = document.execCommand("copy");
        document.body.removeChild(ta);
        if (!ok) throw new Error("execCommand failed");
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      } catch {
        setFailed(true);
        setTimeout(() => setFailed(false), 2500);
      }
    }
  }

  return (
    <button type="button" className="ghost" onClick={copy} style={{ whiteSpace: "nowrap" }}
      aria-live="polite" title={failed ? "Copy failed — select the text manually" : `Copy to clipboard`}>
      {copied ? "✓ Copied" : failed ? "Copy failed" : label}
    </button>
  );
}
