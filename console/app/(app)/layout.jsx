import { redirect } from "next/navigation";
import { sessionUser } from "@/lib/session";
import Nav from "@/components/Nav";

export default async function AppLayout({ children }) {
  const user = await sessionUser();
  if (!user) redirect("/login");

  return (
    <div className="shell">
      <a href="#main" className="skip-link">Skip to content</a>
      <Nav role={user.role} email={user.email} balanceMicro={user.developer_balance_micro} />
      <main className="content" id="main" tabIndex={-1}>{children}</main>
    </div>
  );
}
