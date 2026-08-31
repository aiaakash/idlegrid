import { redirect } from "next/navigation";
import { sessionUser } from "@/lib/session";
import Nav from "@/components/Nav";

export default async function AppLayout({ children }) {
  const user = await sessionUser();
  if (!user) redirect("/login");

  return (
    <div className="shell">
      <Nav role={user.role} />
      <main className="content">{children}</main>
    </div>
  );
}
