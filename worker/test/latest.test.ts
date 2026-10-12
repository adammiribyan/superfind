import { test } from "node:test";
import assert from "node:assert/strict";
import { GOOD, world } from "./fakes.ts";

const latestOf = async (w: ReturnType<typeof world>) => (await w.call(GOOD)).body.latest;

test("latest is the newest release when it carries a signature", async () => {
  const w = world({ tag: "v0.13.1", sig: 302 });
  assert.equal(await latestOf(w), "0.13.1");
  assert.deepEqual(
    w.sent.filter((s) => s.url.startsWith("https://github.com/")).map((s) => `${s.method} ${s.url}`),
    [
      "HEAD https://github.com/adammiribyan/superfind/releases/latest",
      "HEAD https://github.com/adammiribyan/superfind/releases/download/v0.13.1/checksums.txt.minisig",
    ],
  );
});

test("latest is null when the newest release has no signature", async () => {
  assert.equal(await latestOf(world({ tag: "v0.11.0", sig: 404 })), null);
});

test("latest is null when GitHub is down and nothing is cached", async () => {
  assert.equal(await latestOf(world({ github: "down" })), null);
  assert.equal(await latestOf(world({ sig: 503 })), null);
});

test("GitHub is asked at most once per ten minutes", async () => {
  const w = world();
  w.at("2026-10-11T12:00:00Z");
  assert.equal(await latestOf(w), "0.12.0");
  w.opts.tag = "v0.12.1";
  w.at("2026-10-11T12:09:59Z");
  assert.deepEqual([await latestOf(w), w.github()], ["0.12.0", 2]);
  w.at("2026-10-11T12:10:00Z");
  assert.deepEqual([await latestOf(w), w.github()], ["0.12.1", 4]);
});

test("a GitHub outage serves the last answer however old", async () => {
  const w = world();
  assert.equal(await latestOf(w), "0.12.0");
  w.opts.github = "down";
  w.at("2026-10-14T12:00:00Z");
  assert.equal(await latestOf(w), "0.12.0");
  w.opts.github = undefined;
  w.opts.tag = "nightly";
  assert.equal(await latestOf(w), "0.12.0");
});
