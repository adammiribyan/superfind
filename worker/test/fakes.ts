import { handle } from "../src/index.ts";

export const ID = "0f8e2c1a-5b7d-4e3f-9a21-6c4d8b0e7f12";
export const GOOD = { schema: 1, id: ID, version: "0.12.0", os: "darwin", arch: "arm64", method: "script", events: [] };

export interface Sent {
  url: string;
  method: string;
  headers: Record<string, string>;
  body: string | undefined;
}

export function world(opts: { tag?: string; sig?: number; github?: "down"; posthog?: "hang" | "down" } = {}) {
  const sent: Sent[] = [];
  const waits: Promise<unknown>[] = [];
  const store = new Map<string, string>();
  let clock = new Date("2026-10-11T12:00:00Z");
  const env: Record<string, string> = { POSTHOG_KEY: "phc_test" };
  const fetch = async (url: string, init: RequestInit = {}): Promise<Response> => {
    sent.push({
      url,
      method: init.method ?? "GET",
      headers: Object.fromEntries(new Headers(init.headers).entries()),
      body: init.body as string | undefined,
    });
    if (url.startsWith("https://us.i.posthog.com/")) {
      if (opts.posthog === "down") throw new TypeError("network down");
      return opts.posthog === "hang" ? new Promise(() => {}) : new Response("{}");
    }
    if (opts.github === "down") throw new TypeError("network down");
    if (url.endsWith("/releases/latest")) {
      const tag = opts.tag ?? "v0.12.0";
      return new Response(null, { status: 302, headers: { location: `https://github.com/adammiribyan/superfind/releases/tag/${tag}` } });
    }
    if (url.endsWith("/checksums.txt.minisig")) return new Response(null, { status: opts.sig ?? 302 });
    return new Response(null, { status: 404 });
  };
  const cache = {
    async match(key: string) {
      const v = store.get(key);
      return v === undefined ? undefined : new Response(v);
    },
    async put(key: string, res: Response) {
      store.set(key, await res.text());
    },
  } as unknown as Cache;
  return {
    sent,
    waits,
    env,
    opts,
    github: () => sent.filter((s) => s.url.startsWith("https://github.com/")).length,
    posthog: () => sent.filter((s) => s.url.startsWith("https://us.i.posthog.com/")),
    at(iso: string) {
      clock = new Date(iso);
    },
    async call(body: unknown, init: { method?: string; path?: string; headers?: Record<string, string> } = {}) {
      const method = init.method ?? "POST";
      const request = new Request(`https://superfind.dev${init.path ?? "/v1/checkin"}`, {
        method,
        headers: init.headers,
        body: method === "POST" || method === "PUT" ? (typeof body === "string" ? body : JSON.stringify(body)) : undefined,
      });
      const res = await handle(request, { env, fetch, cache, waitUntil: (p) => waits.push(p), now: () => clock });
      return { status: res.status, body: await res.json(), headers: Object.fromEntries(res.headers.entries()) };
    },
  };
}
