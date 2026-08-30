import { NextResponse } from "next/server";
import { coordinatorFetch, COOKIE } from "@/lib/session";

export async function POST(req) {
  const { email, password } = await req.json().catch(() => ({}));
  if (!email || !password) {
    return NextResponse.json({ error: "email and password required" }, { status: 400 });
  }
  const { status, body } = await coordinatorFetch("/v1/console/login", {
    method: "POST",
    body: JSON.stringify({ email, password }),
  });
  if (status !== 200) {
    return NextResponse.json({ error: body.error || "login failed" }, { status });
  }
  const res = NextResponse.json({ user: body.user });
  // Secure only when the request arrived over https (behind TLS proxy).
  const proto = req.headers.get("x-forwarded-proto") || req.nextUrl.protocol.replace(":", "");
  res.cookies.set(COOKIE, body.token, {
    httpOnly: true,
    sameSite: "lax",
    secure: proto === "https",
    path: "/",
    maxAge: 7 * 24 * 3600,
  });
  return res;
}
