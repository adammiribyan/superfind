import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { GOOD, ID, UPGRADED, world } from "./fakes.ts";

test("PostHog gets one personless check-in with no address and only a content type", async () => {
  const w = world();
  await w.call(GOOD, { headers: { "x-forwarded-for": "203.0.113.7", "user-agent": "superfind/0.12.0" } });
  await Promise.all(w.waits);
  assert.deepEqual(w.posthog(), [
    {
      url: "https://us.i.posthog.com/batch/",
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        api_key: "phc_test",
        batch: [
          {
            event: "checkin",
            distinct_id: ID,
            uuid: "e46e30f8-b298-8ba9-a37a-bb967cfc8d70",
            timestamp: "2026-10-11T12:00:00.000Z",
            properties: {
              version: "0.12.0",
              os: "darwin",
              arch: "arm64",
              method: "script",
              $process_person_profile: false,
              $geoip_disable: true,
              $ip: null,
            },
          },
        ],
      }),
    },
  ]);
});

test("each queued event is forwarded as its own event, timed by hours_ago, with a uuid a resend repeats", async () => {
  const w = world();
  await w.call({ ...GOOD, events: [UPGRADED] });
  await Promise.all(w.waits);
  const batch = JSON.parse(w.posthog()[0].body!).batch;
  assert.deepEqual(batch[1], {
    event: "upgraded",
    distinct_id: ID,
    uuid: "8c0e7ae7-37a6-857f-b681-d06938a5af96",
    timestamp: "2026-10-11T09:00:00.000Z",
    properties: {
      version: "0.12.0",
      os: "darwin",
      arch: "arm64",
      method: "script",
      outcome: "installed",
      $process_person_profile: false,
      $geoip_disable: true,
      $ip: null,
    },
  });
  assert.equal(batch.length, 2);
  w.at("2026-10-12T12:00:00Z");
  await w.call({ ...GOOD, events: [UPGRADED] });
  await Promise.all(w.waits);
  const again = JSON.parse(w.posthog()[1].body!).batch;
  assert.deepEqual([again[1].uuid, again[1].timestamp], [batch[1].uuid, "2026-10-12T09:00:00.000Z"]);
  assert.notEqual(again[0].uuid, batch[0].uuid);
  const other = world();
  await other.call({ ...GOOD, id: "1f8e2c1a-5b7d-4e3f-9a21-6c4d8b0e7f12", events: [UPGRADED] });
  await Promise.all(other.waits);
  assert.notEqual(JSON.parse(other.posthog()[0].body!).batch[1].uuid, batch[1].uuid);
});

test("a share event is forwarded with only the install's version, OS, and CPU type", async () => {
  const w = world();
  await w.call({ ...GOOD, events: [{ ...UPGRADED, name: "share_started", props: {} }] });
  await Promise.all(w.waits);
  assert.deepEqual(JSON.parse(w.posthog()[0].body!).batch[1], {
    event: "share_started",
    distinct_id: ID,
    uuid: "8c0e7ae7-37a6-857f-b681-d06938a5af96",
    timestamp: "2026-10-11T09:00:00.000Z",
    properties: { version: "0.12.0", os: "darwin", arch: "arm64", $process_person_profile: false, $geoip_disable: true, $ip: null },
  });
});

test("an install keeps one event uuid all day and gets a new one the next UTC day", async () => {
  const w = world();
  const uuids: string[] = [];
  for (const at of ["2026-10-11T00:00:01Z", "2026-10-11T23:59:59Z", "2026-10-12T00:00:00Z"]) {
    w.at(at);
    await w.call(GOOD);
    await Promise.all(w.waits);
    uuids.push(JSON.parse(w.posthog().at(-1)!.body!).batch[0].uuid);
  }
  assert.deepEqual(uuids, [
    "e46e30f8-b298-8ba9-a37a-bb967cfc8d70",
    "e46e30f8-b298-8ba9-a37a-bb967cfc8d70",
    "7f653d54-8819-8a11-b7ea-dd41d5aa746c",
  ]);
  const other = world();
  await other.call({ ...GOOD, id: "1f8e2c1a-5b7d-4e3f-9a21-6c4d8b0e7f12" });
  await Promise.all(other.waits);
  assert.equal(JSON.parse(other.posthog()[0].body!).batch[0].uuid, "62818472-7939-822e-9dfc-a33c72ec0cf7");
});

test("the reply never waits on PostHog", { timeout: 2000 }, async () => {
  const w = world({ posthog: "hang" });
  const res = await w.call(GOOD);
  assert.deepEqual([res.status, res.body], [200, { v: 1, latest: "0.12.0" }]);
  const settled = await Promise.race([w.waits[0].then(() => "settled"), new Promise((r) => setTimeout(r, 50, "pending"))]);
  assert.equal(settled, "pending");
});

test("a PostHog that is down does not fail the check-in", async () => {
  const w = world({ posthog: "down" });
  const res = await w.call(GOOD);
  assert.deepEqual([res.status, res.body], [200, { v: 1, latest: "0.12.0" }]);
  assert.deepEqual(await Promise.allSettled(w.waits), [{ status: "fulfilled", value: undefined }]);
});

test("without a PostHog key nothing is forwarded", async () => {
  const w = world();
  w.env.POSTHOG_KEY = "";
  const res = await w.call(GOOD);
  assert.deepEqual([res.status, w.waits.length, w.posthog().length], [200, 0, 0]);
});

test("the Worker's source never names a client address header or request.cf", () => {
  const dir = new URL("../src/", import.meta.url);
  const forbidden = /cf-connecting-ip|x-forwarded-for|x-real-ip|true-client-ip|\.cf\b/i;
  const hits = readdirSync(dir)
    .filter((f) => forbidden.test(readFileSync(new URL(f, dir), "utf8")))
    .sort();
  assert.deepEqual(hits, []);
  assert.equal(forbidden.test("const ip = request.headers.get('CF-Connecting-IP')"), true);
  assert.equal(forbidden.test("request.cf.country"), true);
});
