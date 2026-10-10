#!/usr/bin/env python3
"""Run one shard of the behavior tests, in parallel processes.

  behavior.py list
      print the id of every behavior test, one per line
  behavior.py run SHARD SHARDS WORKERS COUNTS_FILE
      run the tests of shard SHARD (1-based) of SHARDS, WORKERS at a time, each
      test in its own process, and write what ran, failed, and was skipped,
      with each test's duration, to COUNTS_FILE
  behavior.py total COUNTS_DIR
      check that the counts files under COUNTS_DIR cover every behavior test
      exactly once

Shards are balanced on the durations in behavior-durations.json (a test
missing from it counts as the median), longest first. Within a shard, each
worker takes the next longest test as it frees up. Each worker has its own
TMUX_TMPDIR, so no two tests running at once share a tmux server.
"""
import json
import os
import queue
import statistics
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
TESTS = HERE.parent.parent / "tests"
DURATIONS = HERE / "behavior-durations.json"


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


def expected_durations(ids):
    known = json.loads(DURATIONS.read_text()) if DURATIONS.exists() else {}
    median = statistics.median(known.values()) if known else 1.0
    return {i: known.get(i, median) for i in ids}


def shard_of(ids, shard, shards):
    cost = expected_durations(ids)
    loads, hands = [0.0] * shards, [[] for _ in range(shards)]
    for i in sorted(ids, key=lambda i: (-cost[i], i)):
        lightest = loads.index(min(loads))
        hands[lightest].append(i)
        loads[lightest] += cost[i]
    return sorted(hands[shard - 1], key=lambda i: (-cost[i], i)), loads[shard - 1]


RUNNER = """
import json, sys, time, unittest
test_id, out = sys.argv[1], sys.argv[2]
suite = unittest.TestLoader().loadTestsFromName(test_id)
start = time.time()
result = unittest.TextTestRunner(verbosity=2, stream=sys.stdout).run(suite)
json.dump({"ran": result.testsRun, "seconds": round(time.time() - start, 2),
           "failed": bool(result.failures or result.errors or result.unexpectedSuccesses),
           "skipped": bool(result.skipped)}, open(out, "w"))
"""


def run(shard, shards, workers, counts_file):
    ids, planned = shard_of(test_ids(), shard, shards)
    print(f"shard {shard}/{shards}: {len(ids)} tests, about {planned:.0f}s of work by past durations, {workers} at a time")
    work = Path(tempfile.mkdtemp(prefix="behavior-"))
    todo = queue.Queue()
    for i in ids:
        todo.put(i)
    results, lock = {}, threading.Lock()

    def worker(n):
        tmux = work / f"tmux{n}"
        tmux.mkdir()
        env = {**os.environ, "TMUX_TMPDIR": str(tmux)}
        while True:
            try:
                test_id = todo.get_nowait()
            except queue.Empty:
                return
            out = work / f"{test_id}.json"
            proc = subprocess.run([sys.executable, "-c", RUNNER, test_id, str(out)], cwd=TESTS, env=env,
                                  stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            result = json.loads(out.read_text()) if out.exists() else {"ran": 0, "seconds": 0, "failed": True, "skipped": False}
            result["failed"] = result["failed"] or proc.returncode != 0 or result["ran"] != 1
            with lock:
                results[test_id] = result
                mark = "FAIL" if result["failed"] else "skip" if result["skipped"] else "ok"
                print(f"{mark:4} {result['seconds']:6.1f}s  {test_id}", flush=True)
                if result["failed"]:
                    print(f"::group::output of {test_id}\n{proc.stdout}::endgroup::", flush=True)

    start = time.time()
    threads = [threading.Thread(target=worker, args=(n,)) for n in range(workers)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    counts = {
        "shard": shard, "shards": shards,
        "ids": sorted(results),
        "ran": sum(r["ran"] for r in results.values()),
        "failed": sorted(i for i, r in results.items() if r["failed"]),
        "skipped": sorted(i for i, r in results.items() if r["skipped"]),
        "seconds": {i: r["seconds"] for i, r in sorted(results.items())},
    }
    Path(counts_file).write_text(json.dumps(counts, indent=1))
    print(f"shard {shard}/{shards}: {len(ids)} tests assigned, {counts['ran']} ran, {len(counts['failed'])} failed, "
          f"{len(counts['skipped'])} skipped, {time.time() - start:.0f}s")
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
    missing = sorted(set(want) - set(ran))
    extra = sorted({i for i in ran if ran.count(i) > 1 or i not in want})
    for t in missing:
        print(f"::error::never ran: {t}")
    for t in extra:
        print(f"::error::ran twice or unknown: {t}")
    sys.exit(0 if files and not missing and not extra and len(ran) == len(want) else 1)


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
