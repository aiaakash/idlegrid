import { cookies } from "next/headers";

const API = process.env.CONSOLE_API_URL || "http://localhost:8090";
export const COOKIE = "ig_session";

export async function coordinatorFetch(path, opts = {}) {
  const headers = { "Content-Type": "application/json", ...(opts.headers || {}) };
  const res = await fetch(`${API}${path}`, { ...opts, headers, cache: "no-store" });
  const body = await res.json().catch(() => ({}));
  return { status: res.status, body };
}

export async function sessionUser() {
  const token = cookies().get(COOKIE)?.value;
  if (!token) return null;
  const { status, body } = await coordinatorFetch("/v1/console/me", {
    headers: { "X-Session-Token": token },
  });
  if (status !== 200) return null;
  return body;
}

export async function requireSessionUser() {
  const user = await sessionUser();
  if (!user) throw new Error("unauthorized");
  return user;
}

export function sessionToken() {
  return cookies().get(COOKIE)?.value || "";
}
