#!/usr/bin/env python3
"""Run one shard of the behavior tests, in parallel processes.

  behavior.py list
      print the id of every behavior test, one per line
  behavior.py run SHARD SHARDS PROCESSES COUNTS_FILE
      run the tests of shard SHARD (1-based) of SHARDS, spread over PROCESSES
      processes, and write how many ran, failed, and were skipped to COUNTS_FILE
  behavior.py total COUNTS_DIR
      check that the shards' counts files cover every behavior test exactly once

Tests are dealt out one at a time over the sorted test ids, so every shard and
every process gets a share of each test file. Each process gets its own
TMUX_TMPDIR, so no two processes share a tmux server.
"""
import json
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

TESTS = Path(__file__).resolve().parent.parent.parent / "tests"


def test_ids():
    def walk(suite):
        for item in suite:
            if isinstance(item, unittest.TestSuite):
                yield from walk(item)
            else:
                yield item.id()
    sys.path.insert(0, str(TESTS))
    ids = sorted(walk(unittest.TestLoader().discover(str(TESTS))))
    broken = [i for i in ids if i.startswith("unittest.loader.")]
    if broken:
        sys.exit(f"tests failed to import: {broken}")
    return ids


def deal(items, hands):
    return [items[i::hands] for i in range(hands)]


RUNNER = """
import json, sys, unittest
ids, out = sys.argv[1:-1], sys.argv[-1]
suite = unittest.TestLoader().loadTestsFromNames(ids)
result = unittest.TextTestRunner(verbosity=2, stream=sys.stdout).run(suite)
json.dump({"ids": ids, "ran": result.testsRun, "failed": [t.id() for t, _ in result.failures + result.errors],
           "skipped": [t.id() for t, _ in result.skipped],
           "unexpected_successes": [t.id() for t in result.unexpectedSuccesses]}, open(out, "w"))
"""


def run(shard, shards, processes, counts_file):
    ids = deal(test_ids(), shards)[shard - 1]
    work = Path(tempfile.mkdtemp(prefix="behavior-"))
    jobs = []
    for n, chunk in enumerate(deal(ids, processes)):
        if not chunk:
            continue
        tmux = work / f"tmux{n}"
        tmux.mkdir()
        log = open(work / f"log{n}", "w")
        env = {**os.environ, "TMUX_TMPDIR": str(tmux)}
        proc = subprocess.Popen([sys.executable, "-c", RUNNER, *chunk, str(work / f"result{n}.json")],
                                cwd=TESTS, env=env, stdout=log, stderr=subprocess.STDOUT)
        jobs.append((n, chunk, proc, log, time.time()))
    results = []
    for n, chunk, proc, log, start in jobs:
        proc.wait()
        log.close()
        took = time.time() - start
        print(f"::group::process {n + 1}/{len(jobs)}: {len(chunk)} tests, exit {proc.returncode}, {took:.0f}s")
        print((work / f"log{n}").read_text(), end="")
        print("::endgroup::")
        result_file = work / f"result{n}.json"
        if not result_file.exists():
            results.append({"ids": chunk, "ran": 0, "failed": chunk, "skipped": [], "unexpected_successes": []})
            print(f"process {n + 1} wrote no result")
            continue
        results.append(json.loads(result_file.read_text()))
    counts = {
        "shard": shard, "shards": shards,
        "ids": sorted(i for r in results for i in r["ids"]),
        "ran": sum(r["ran"] for r in results),
        "failed": sorted(i for r in results for i in r["failed"] + r["unexpected_successes"]),
        "skipped": sorted(i for r in results for i in r["skipped"]),
    }
    Path(counts_file).write_text(json.dumps(counts, indent=1))
    print(f"shard {shard}/{shards}: {len(ids)} tests assigned, {counts['ran']} ran, "
          f"{len(counts['failed'])} failed, {len(counts['skipped'])} skipped")
    for t in counts["skipped"]:
        print(f"  skipped: {t}")
    for t in counts["failed"]:
        print(f"::error::failed: {t}")
    ok = counts["ran"] == len(ids) and counts["ids"] == sorted(ids) and not counts["failed"]
    sys.exit(0 if ok else 1)


def total(counts_dir):
    want = test_ids()
    files = sorted(Path(counts_dir).rglob("*.json"))
    ran = [i for f in files for i in json.loads(f.read_text())["ids"]]
    skipped = [i for f in files for i in json.loads(f.read_text())["skipped"]]
    print(f"{len(files)} shards, {len(ran)} tests ran, {len(skipped)} skipped, {len(want)} tests exist")
    for t in skipped:
        print(f"  skipped: {t}")
    missing, extra = sorted(set(want) - set(ran)), sorted(i for i in set(ran) if ran.count(i) > 1 or i not in want)
    for t in missing:
        print(f"::error::never ran: {t}")
    for t in extra:
        print(f"::error::ran twice or unknown: {t}")
    sys.exit(0 if not missing and not extra and len(ran) == len(want) else 1)


if __name__ == "__main__":
    command = sys.argv[1] if len(sys.argv) > 1 else ""
    if command == "list":
        print("\n".join(test_ids()))
    elif command == "run" and len(sys.argv) == 6:
        run(int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]), sys.argv[5])
    elif command == "total" and len(sys.argv) == 3:
        total(sys.argv[2])
    else:
        sys.exit(__doc__)
