import { NextResponse } from "next/server";
import { coordinatorFetch, COOKIE, sessionToken } from "@/lib/session";

export async function POST() {
  const token = sessionToken();
  if (token) {
    await coordinatorFetch("/v1/console/logout", {
      method: "POST",
      headers: { "X-Session-Token": token },
    });
  }
  const res = NextResponse.json({ ok: true });
  res.cookies.set(COOKIE, "", { path: "/", maxAge: 0 });
  return res;
}
