import { type Checkin, parseCheckin, posthogBatch, reply } from "./checkin.ts";
import { latest } from "./latest.ts";

export interface Env {
  POSTHOG_KEY?: string;
  RETRY_AFTER_S?: string;
}

export interface Deps {
  env: Env;
  waitUntil(p: Promise<unknown>): void;
  fetch(input: string, init?: RequestInit): Promise<Response>;
  cache: Cache;
  now(): Date;
}

const MAX_BODY = 16 * 1024;
const POSTHOG_BATCH = "https://us.i.posthog.com/batch/";

export default {
  fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    return handle(request, {
      env,
      waitUntil: (p) => ctx.waitUntil(p),
      fetch: (input, init) => fetch(input, init),
      cache: (caches as unknown as { default: Cache }).default,
      now: () => new Date(),
    });
  },
};

export async function handle(request: Request, deps: Deps): Promise<Response> {
  const { pathname } = new URL(request.url);
  if (pathname === "/v1/health") {
    return request.method === "GET" ? json(200, { ok: true }) : notAllowed("GET");
  }
  if (pathname !== "/v1/checkin") return json(404, { error: "not found" });
  if (request.method !== "POST") return notAllowed("POST");

  const text = await readCapped(request, MAX_BODY);
  if (text === undefined) return json(413, { error: "body is over 16 KiB" });
  const parsed = parseCheckin(text);
  if ("error" in parsed) return json(400, parsed);

  const now = deps.now();
  const key = deps.env.POSTHOG_KEY;
  if (key) deps.waitUntil(forward(deps.fetch, key, parsed.checkin, now));
  return json(200, reply(await latest(deps.fetch, deps.cache, now), deps.env.RETRY_AFTER_S));
}

async function forward(fetch: Deps["fetch"], key: string, c: Checkin, now: Date): Promise<void> {
  try {
    await fetch(POSTHOG_BATCH, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: await posthogBatch(c, key, now),
      signal: AbortSignal.timeout(5000),
    });
  } catch {}
}

async function readCapped(request: Request, max: number): Promise<string | undefined> {
  if (!request.body) return "";
  const reader = request.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > max) {
      await reader.cancel();
      return undefined;
    }
    chunks.push(value);
  }
  const bytes = new Uint8Array(size);
  let at = 0;
  for (const c of chunks) {
    bytes.set(c, at);
    at += c.byteLength;
  }
  return new TextDecoder().decode(bytes);
}

function notAllowed(allow: string): Response {
  return json(405, { error: "method not allowed" }, { allow });
}

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json", "cache-control": "no-store", ...headers },
  });
}
