const REPO = "https://github.com/adammiribyan/superfind";
const CACHE_KEY = "https://superfind.dev/v1/_cache/latest";
const FRESH_MS = 10 * 60 * 1000;
const KEEP_S = 7 * 24 * 3600;
const TIMEOUT_MS = 5000;
const FAILED = Symbol("failed");

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

interface Entry {
  latest: string | null;
  at: number;
}

export async function latest(fetch: Fetch, cache: Cache, now: Date): Promise<string | null> {
  const entry = await cached(cache);
  if (entry && now.getTime() - entry.at < FRESH_MS) return entry.latest;
  const found = await fromGitHub(fetch);
  if (found === FAILED) return entry?.latest ?? null;
  const body = JSON.stringify({ latest: found, at: now.getTime() } satisfies Entry);
  await cache
    .put(CACHE_KEY, new Response(body, { headers: { "cache-control": `max-age=${KEEP_S}` } }))
    .catch(() => {});
  return found;
}

async function cached(cache: Cache): Promise<Entry | undefined> {
  try {
    const res = await cache.match(CACHE_KEY);
    return res ? ((await res.json()) as Entry) : undefined;
  } catch {
    return undefined;
  }
}

async function fromGitHub(fetch: Fetch): Promise<string | null | typeof FAILED> {
  const head = { method: "HEAD", redirect: "manual", signal: AbortSignal.timeout(TIMEOUT_MS) } as const;
  try {
    const release = await fetch(`${REPO}/releases/latest`, head);
    const tag = /\/releases\/tag\/(v\d+\.\d+\.\d+)$/.exec(release.headers.get("location") ?? "")?.[1];
    if (!tag) return FAILED;
    const sig = await fetch(`${REPO}/releases/download/${tag}/checksums.txt.minisig`, head);
    if (sig.status < 400) return tag.slice(1);
    if (sig.status === 404) return null;
    return FAILED;
  } catch {
    return FAILED;
  }
}
