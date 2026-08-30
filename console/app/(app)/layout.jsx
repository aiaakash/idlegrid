import { redirect } from "next/navigation";
import { sessionUser } from "@/lib/session";
import Nav from "@/components/Nav";

export default async function AppLayout({ children }) {
  const user = await sessionUser();
  if (!user) redirect("/login");

  return (
    <div className="container">
      <Nav role={user.role} />
      {children}
    </div>
  );
}
