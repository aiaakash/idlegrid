import { NextResponse } from "next/server";
import { coordinatorFetch } from "@/lib/session";

const API = process.env.CONSOLE_API_URL || "http://localhost:8090";

export async function GET() {
  const { status, body } = await coordinatorFetch("/healthz");
  return NextResponse.json({
    console: "ok",
    coordinator: status === 200 ? "reachable" : `unreachable (${status})`,
    coordinator_url: API,
    hint: status === 200 ? null : "set CONSOLE_API_URL to the coordinator container name (e.g. http://idlegrid-api:8080) and make sure both apps share a network",
  });
}
