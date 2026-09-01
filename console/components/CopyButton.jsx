"use client";

import { useState } from "react";

// Copy-to-clipboard button with "Copied!" feedback. ghost-styled.
export default function CopyButton({ text, label = "Copy" }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable (non-secure context) — select fallback not worth it
    }
  }

  return (
    <button type="button" className="ghost" onClick={copy} style={{ whiteSpace: "nowrap" }}>
      {copied ? "✓ Copied" : label}
    </button>
  );
}
