import { cookies } from "next/headers";

const API = process.env.CONSOLE_API_URL || "http://localhost:8090";
export const COOKIE = "ig_session";

export async function coordinatorFetch(path, opts = {}) {
  const headers = { "Content-Type": "application/json", ...(opts.headers || {}) };
  let res;
  try {
    res = await fetch(`${API}${path}`, { ...opts, headers, cache: "no-store" });
  } catch (e) {
    // Network-level failure: the console container cannot reach the
    // coordinator. Surface a clear error instead of an uncaught 500.
    return {
      status: 502,
      body: { error: `console cannot reach the coordinator at ${API} — check the CONSOLE_API_URL env var and that both containers share a network` },
    };
  }
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
