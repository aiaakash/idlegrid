"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";

const links = [
  ["/", "Overview"],
  ["/topup", "Top-up"],
  ["/keys", "API Keys"],
  ["/usage", "Usage"],
  ["/provider", "Provider"],
  ["/link", "Link a Mac"],
  ["/payouts", "Payouts"],
  ["/admin", "Admin"],
];

export default function Nav({ role }) {
  const path = usePathname();
  const router = useRouter();

  async function logout() {
    await fetch("/api/logout", { method: "POST" });
    router.push("/login");
    router.refresh();
  }

  return (
    <header className="top">
      <h1><span>idlegrid</span></h1>
      <nav>
        {links
          .filter(([href]) => href !== "/admin" || role === "admin")
          .map(([href, label]) => (
            <Link key={href} href={href} className={path === href ? "active" : ""}>
              {label}
            </Link>
          ))}
      </nav>
      <div className="spacer" />
      <button className="ghost" onClick={logout}>Sign out</button>
    </header>
  );
}
