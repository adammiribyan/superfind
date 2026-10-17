import { test } from "node:test";
import assert from "node:assert/strict";
import { parseCheckin } from "../src/checkin.ts";
import { GOOD, ID, UPGRADED, world } from "./fakes.ts";

test("a good check-in is rebuilt from the allowlist, dropping unknown fields and events", () => {
  const body = { ...GOOD, home: "/Users/someone", events: [{ name: "search", query: "secret" }, "junk"] };
  assert.deepEqual(parseCheckin(JSON.stringify(body)), {
    checkin: { schema: 1, id: ID, version: "0.12.0", os: "darwin", arch: "arm64", method: "script", events: [] },
  });
});

test("an upgraded event keeps only its allowed props, and a malformed one is dropped", () => {
  const events = [
    { ...UPGRADED, path: "/Users/someone/.local/bin/superfind", props: { ...UPGRADED.props, from: "0.11.0" } },
    { ...UPGRADED, props: { method: "script", outcome: "exploded" } },
    { ...UPGRADED, props: { method: "npm", outcome: "installed" } },
    { ...UPGRADED, props: { outcome: "installed" } },
    { ...UPGRADED, props: "installed" },
    { ...UPGRADED, id: "not-a-uuid" },
    { ...UPGRADED, id: undefined },
    { ...UPGRADED, hours_ago: -1 },
    { ...UPGRADED, hours_ago: 3.5 },
    { ...UPGRADED, hours_ago: "3" },
    { ...UPGRADED, hours_ago: 24 * 366 + 1 },
    { ...UPGRADED, name: "search" },
    { ...UPGRADED, hours_ago: 0, props: { method: "homebrew", outcome: "unchanged" } },
  ];
  const parsed = parseCheckin(JSON.stringify({ ...GOOD, events }));
  assert.deepEqual("checkin" in parsed ? parsed.checkin.events : parsed, [
    { name: "upgraded", id: UPGRADED.id, hours_ago: 3, props: { method: "script", outcome: "installed" } },
    { name: "upgraded", id: UPGRADED.id, hours_ago: 0, props: { method: "homebrew", outcome: "unchanged" } },
  ]);
});

test("share events keep no props", () => {
  const events = ["share_written", "share_received", "share_started", "share_opened"].map((name) => ({
    ...UPGRADED,
    name,
    props: { bundle_id: "9f2c0e1a3b4f4b8e9a520c7d6e2b9f11", repo: "https://github.com/o/r" },
  }));
  const parsed = parseCheckin(JSON.stringify({ ...GOOD, events }));
  assert.deepEqual("checkin" in parsed ? parsed.checkin.events : parsed, [
    { name: "share_written", id: UPGRADED.id, hours_ago: 3, props: {} },
    { name: "share_received", id: UPGRADED.id, hours_ago: 3, props: {} },
    { name: "share_started", id: UPGRADED.id, hours_ago: 3, props: {} },
  ]);
});

test("a check-in without events is accepted with none", () => {
  const { events, ...rest } = GOOD;
  assert.deepEqual(parseCheckin(JSON.stringify(rest)), {
    checkin: { schema: 1, id: ID, version: "0.12.0", os: "darwin", arch: "arm64", method: "script", events: [] },
  });
});

test("the reply carries the latest signed version and is never cached", async () => {
  const w = world();
  assert.deepEqual(await w.call(GOOD), {
    status: 200,
    body: { v: 1, latest: "0.12.0" },
    headers: { "content-type": "application/json", "cache-control": "no-store" },
  });
});

test("each bad field is a 400 that names it", async () => {
  const cases: [unknown, string][] = [
    ["{not json", "body is not JSON"],
    [[GOOD], "body is not a JSON object"],
    [null, "body is not a JSON object"],
    [{ ...GOOD, id: "not-a-uuid" }, "id is not a version 4 UUID"],
    [{ ...GOOD, id: "0f8e2c1a-5b7d-1e3f-9a21-6c4d8b0e7f12" }, "id is not a version 4 UUID"],
    [{ ...GOOD, id: ID.toUpperCase() }, "id is not a version 4 UUID"],
    [{ ...GOOD, id: undefined }, "id is not a version 4 UUID"],
    [{ ...GOOD, version: "0.12" }, "version is not x.y.z"],
    [{ ...GOOD, version: "v0.12.0" }, "version is not x.y.z"],
    [{ ...GOOD, version: "0.12.0-dirty" }, "version is not x.y.z"],
    [{ ...GOOD, os: "windows" }, "os is not darwin or linux"],
    [{ ...GOOD, arch: "386" }, "arch is not amd64 or arm64"],
    [{ ...GOOD, method: "npm" }, "method is not script, homebrew, go, checkout, or unknown"],
    [{ ...GOOD, events: {} }, "events is not a list"],
  ];
  for (const [body, error] of cases) {
    const w = world();
    const raw = typeof body === "string" ? body : JSON.stringify(body);
    const res = await w.call(raw);
    assert.deepEqual([res.status, res.body], [400, { error }], raw);
    assert.equal(w.waits.length, 0, `nothing is forwarded for ${raw}`);
  }
});

test("a schema other than 1 is told to upgrade", async () => {
  for (const schema of [2, 0, "1", undefined]) {
    const res = await world().call({ ...GOOD, schema });
    assert.deepEqual([res.status, res.body], [400, { error: "upgrade" }], String(schema));
  }
});

test("a body over 16 KiB is a 413, one at 16 KiB is accepted", async () => {
  const json = JSON.stringify(GOOD);
  const exact = json + " ".repeat(16 * 1024 - json.length);
  const ok = await world().call(exact);
  assert.equal(ok.status, 200);
  const over = await world().call(exact + " ");
  assert.deepEqual([over.status, over.body], [413, { error: "body is over 16 KiB" }]);
});

test("other methods are 405 and other paths are 404", async () => {
  const w = world();
  const get = await w.call(undefined, { method: "GET" });
  assert.deepEqual([get.status, get.body, get.headers.allow], [405, { error: "method not allowed" }, "POST"]);
  const put = await w.call(GOOD, { method: "PUT" });
  assert.deepEqual([put.status, put.headers.allow], [405, "POST"]);
  const other = await w.call(GOOD, { path: "/v1/checkins" });
  assert.deepEqual([other.status, other.body], [404, { error: "not found" }]);
  const root = await w.call(undefined, { method: "GET", path: "/v1/" });
  assert.deepEqual([root.status, root.body], [404, { error: "not found" }]);
});

test("health answers GET only", async () => {
  const w = world();
  const ok = await w.call(undefined, { method: "GET", path: "/v1/health" });
  assert.deepEqual([ok.status, ok.body], [200, { ok: true }]);
  const post = await w.call({}, { path: "/v1/health" });
  assert.deepEqual([post.status, post.headers.allow], [405, "GET"]);
});

test("RETRY_AFTER_S is clamped to between an hour and a week", async () => {
  const cases: [string | undefined, number | undefined][] = [
    [undefined, undefined],
    ["", undefined],
    ["soon", undefined],
    ["60", 3600],
    ["86400", 86400],
    ["99999999", 604800],
  ];
  for (const [value, expected] of cases) {
    const w = world();
    if (value !== undefined) w.env.RETRY_AFTER_S = value;
    const res = await w.call(GOOD);
    const want = expected === undefined ? { v: 1, latest: "0.12.0" } : { v: 1, latest: "0.12.0", retry_after_s: expected };
    assert.deepEqual(res.body, want, String(value));
  }
});
