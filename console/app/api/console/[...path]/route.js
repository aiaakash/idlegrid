import { NextResponse } from "next/server";
import { sessionToken } from "@/lib/session";

const API = process.env.CONSOLE_API_URL || "http://localhost:8090";

// Generic authed proxy: /api/console/<path> -> coordinator /v1/console/<path>
// with the session cookie forwarded as X-Session-Token.
// NOTE: Next 15 — catch-all params are a Promise and MUST be awaited.
async function proxy(req, { params }) {
  const token = sessionToken();
  if (!token) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }
  const { path: segments } = await params;
  const path = segments?.join("/") || "";
  const url = `${API}/v1/console/${path}${req.nextUrl.search}`;
  const init = {
    method: req.method,
    headers: { "Content-Type": "application/json", "X-Session-Token": token },
  };
  if (req.method !== "GET") {
    init.body = await req.text(); // DELETE carries a JSON body too
  }
  const res = await fetch(url, { ...init, cache: "no-store" });
  const body = await res.json().catch(() => ({}));
  return NextResponse.json(body, { status: res.status });
}

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const DELETE = proxy;
