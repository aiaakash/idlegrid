"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { fmtUSD } from "@/lib/format";

const groups = [
  {
    label: "Develop",
    links: [
      ["/", "Overview"],
      ["/keys", "API Keys"],
      ["/usage", "Usage"],
      ["/topup", "Top up"],
    ],
  },
  {
    label: "Earn",
    links: [
      ["/provider", "Provider"],
      ["/link", "Link Mac"],
      ["/payouts", "Payouts"],
    ],
  },
];

const adminGroup = {
  label: "Manage",
  links: [["/admin", "Admin"]],
};

export default function Nav({ role, email, balanceMicro }) {
  const path = usePathname();
  const router = useRouter();
  const balance = balanceMicro ?? 0;

  async function logout() {
    await fetch("/api/logout", { method: "POST" });
    router.push("/login");
    router.refresh();
  }

  const allGroups =
    role === "admin" ? [...groups, adminGroup] : groups;

  return (
    <aside className="sidebar">
      <div className="topbar">
        <h1><span>idlegrid</span></h1>
      </div>
      {typeof balanceMicro === "number" && (
        <Link href="/topup" className="balance" title="Developer credit — click to top up"
          style={{ textDecoration: "none", color: "inherit" }}>
          <div className="label">Balance</div>
          <div className={`value ${balance < 1_000_000 ? "low" : ""}`}>
            {fmtUSD(balance)}
          </div>
        </Link>
      )}
      <nav aria-label="Console sections">
        {allGroups.map((g) => (
          <div key={g.label}>
            <div className="navgroup" aria-hidden="true">{g.label}</div>
            {g.links.map(([href, label]) => {
              const active = path === href;
              return (
                <Link
                  key={href}
                  href={href}
                  className={active ? "active" : ""}
                  aria-current={active ? "page" : undefined}
                >
                  {label}
                </Link>
              );
            })}
          </div>
        ))}
      </nav>
      <div className="spacer" />
      {email && (
        <div className="whoami">
          <div className="email" title={email}>{email}</div>
          <span className={`badge ${role === "admin" ? "warn" : ""}`}>{role}</span>
        </div>
      )}
      <button className="ghost" onClick={logout}>Sign out</button>
    </aside>
  );
}
