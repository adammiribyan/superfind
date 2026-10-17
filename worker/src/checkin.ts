const OSES = ["darwin", "linux"] as const;
const ARCHES = ["amd64", "arm64"] as const;
const METHODS = ["script", "homebrew", "go", "checkout", "unknown"] as const;
const OUTCOMES = ["installed", "unchanged", "failed"] as const;
const MAX_HOURS_AGO = 24 * 366;

export type OS = (typeof OSES)[number];
export type Arch = (typeof ARCHES)[number];
export type Method = (typeof METHODS)[number];

export interface Event {
  name: string;
  id: string;
  hours_ago: number;
  props: Record<string, string | number | boolean>;
}

export interface Checkin {
  schema: 1;
  id: string;
  version: string;
  os: OS;
  arch: Arch;
  method: Method;
  events: Event[];
}

export interface Reply {
  v: 1;
  latest: string | null;
  retry_after_s?: number;
}

type EventRule = (props: Record<string, unknown>) => Event["props"] | undefined;
export const EVENTS: Record<string, EventRule> = {
  upgraded: ({ method, outcome }) =>
    oneOf(method, METHODS) && oneOf(outcome, OUTCOMES) ? { method, outcome } : undefined,
  share_written: () => ({}),
  share_received: () => ({}),
  share_started: () => ({}),
};

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const VERSION = /^(0|[1-9]\d{0,3})\.(0|[1-9]\d{0,3})\.(0|[1-9]\d{0,3})$/;

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function oneOf<T extends string>(v: unknown, list: readonly T[]): v is T {
  return typeof v === "string" && (list as readonly string[]).includes(v);
}

export function parseCheckin(text: string): { checkin: Checkin } | { error: string } {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    return { error: "body is not JSON" };
  }
  if (!isObject(raw)) return { error: "body is not a JSON object" };
  if (raw.schema !== 1) return { error: "upgrade" };
  const { id, version, os, arch, method } = raw;
  if (typeof id !== "string" || !UUID_V4.test(id)) return { error: "id is not a version 4 UUID" };
  if (typeof version !== "string" || !VERSION.test(version)) return { error: "version is not x.y.z" };
  if (!oneOf(os, OSES)) return { error: "os is not darwin or linux" };
  if (!oneOf(arch, ARCHES)) return { error: "arch is not amd64 or arm64" };
  if (!oneOf(method, METHODS)) return { error: "method is not script, homebrew, go, checkout, or unknown" };
  const rawEvents = raw.events ?? [];
  if (!Array.isArray(rawEvents)) return { error: "events is not a list" };
  const events: Event[] = [];
  for (const e of rawEvents) {
    if (!isObject(e) || typeof e.name !== "string" || !Object.hasOwn(EVENTS, e.name) || !isObject(e.props)) continue;
    if (typeof e.id !== "string" || !UUID_V4.test(e.id)) continue;
    if (!Number.isInteger(e.hours_ago) || (e.hours_ago as number) < 0 || (e.hours_ago as number) > MAX_HOURS_AGO) continue;
    const props = EVENTS[e.name](e.props);
    if (props) events.push({ name: e.name, id: e.id, hours_ago: e.hours_ago as number, props });
  }
  return { checkin: { schema: 1, id, version, os, arch, method, events } };
}

const RETRY_MIN_S = 3600;
const RETRY_MAX_S = 604800;

export function reply(latest: string | null, retryAfter: string | undefined): Reply {
  const r: Reply = { v: 1, latest };
  const s = Number.parseInt(retryAfter ?? "", 10);
  if (Number.isFinite(s)) r.retry_after_s = Math.min(RETRY_MAX_S, Math.max(RETRY_MIN_S, s));
  return r;
}

async function uuidFrom(text: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  const b = new Uint8Array(digest, 0, 16);
  b[6] = (b[6] & 0x0f) | 0x80;
  b[8] = (b[8] & 0x3f) | 0x80;
  const hex = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

const PERSONLESS = { $process_person_profile: false, $geoip_disable: true, $ip: null };

export async function posthogBatch(c: Checkin, apiKey: string, now: Date): Promise<string> {
  const batch = [
    {
      event: "checkin",
      distinct_id: c.id,
      uuid: await uuidFrom(`${c.id} ${now.toISOString().slice(0, 10)}`),
      timestamp: now.toISOString(),
      properties: { version: c.version, os: c.os, arch: c.arch, method: c.method, ...PERSONLESS },
    },
  ];
  for (const e of c.events) {
    batch.push({
      event: e.name,
      distinct_id: c.id,
      uuid: await uuidFrom(`${c.id} ${e.id}`),
      timestamp: new Date(now.getTime() - e.hours_ago * 3600 * 1000).toISOString(),
      properties: { version: c.version, os: c.os, arch: c.arch, ...e.props, ...PERSONLESS },
    });
  }
  return JSON.stringify({ api_key: apiKey, batch });
}
