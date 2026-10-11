import argparse
import concurrent.futures
import json
import math
import os
import random
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
import urllib.parse
from pathlib import Path

STOPWORDS = ("the a to of and in is it that for on with this be we you are as not can will".split())
SYLLABLES = ("ka lo mi ne ru sa ti vo be da fe gi ho ju ki la mo nu pi qu ra se to vu wa xi yo ze "
             "an el in or us ar en ir ol um").split()
PROBES = {"zebrafix": 0.5, "quokkaroute": 0.01, "marmotcache": 0.1}
BYTES_PER_WORD = {"claude": 720, "codex": 2000, "grok": 670}
DAY = 86400


def dumps(entry):
    return json.dumps(entry, separators=(",", ":"))


class Corpus:
    def __init__(self, seed):
        self.rng = random.Random(seed)
        rng = random.Random(seed + 1)
        self.vocab = sorted({"".join(rng.choice(SYLLABLES) for _ in range(rng.randint(2, 4))) for _ in range(6000)})
        rng.shuffle(self.vocab)
        self.weights = [1 / (rank + 1) for rank in range(len(self.vocab))]
        filler = random.Random(seed + 2)
        self.filler = "".join(filler.choice("abcdef0123456789 \n_./=") for _ in range(1 << 16))

    def words(self, n, probes=()):
        picked = self.rng.choices(self.vocab, self.weights, k=n)
        for i in range(0, n, 7):
            picked[i] = self.rng.choice(STOPWORDS)
        for probe in probes:
            if n:
                picked[self.rng.randrange(n)] = probe
        return " ".join(picked)

    def tool_output(self, n):
        start = self.rng.randrange(len(self.filler) - min(n, len(self.filler) - 1))
        out = self.filler[start:start + n]
        return (out * (n // len(out) + 1))[:n]

    def uid(self):
        return "%08x-%04x-%04x-%04x-%012x" % tuple(self.rng.getrandbits(b) for b in (32, 16, 16, 16, 48))

    def plan(self, agent):
        size = min(int(math.exp(self.rng.gauss(math.log(700_000), 1.5))), 64 << 20)
        size = max(size, 4096)
        words = max(size // BYTES_PER_WORD[agent], 20)
        prompts = max(1, min(int(math.exp(self.rng.gauss(math.log(3), 1.1))), 200))
        tools = max(0, int(math.exp(self.rng.gauss(math.log(40), 1.3))))
        probes = [p for p, share in PROBES.items() if self.rng.random() < share]
        cwd = "/work/" + self.rng.choice(self.vocab[:200]) + "-" + self.rng.choice(self.vocab[:50])
        return size, words, prompts, tools, probes, cwd

    def turns(self, words, prompts, tools, probes):
        replies = prompts * 2
        per = max(words // (prompts + replies), 3)
        out = []
        for p in range(prompts):
            out.append(("user", self.words(min(per, 80), probes if p == 0 else ())))
            for _ in range(2):
                out.append(("assistant", self.words(per)))
                for _ in range(tools // replies):
                    out.append(("tool", None))
        return out

    def claude(self, path, size, words, prompts, tools, probes, cwd):
        lines = []
        for role, text in self.turns(words, prompts, tools, probes):
            if role == "user":
                lines.append(dumps({"type": "user", "cwd": cwd, "gitBranch": "main", "entrypoint": "cli",
                                    "message": {"role": "user", "content": text}}))
            elif role == "assistant":
                lines.append(dumps({"type": "assistant", "message": {"role": "assistant",
                                    "content": [{"type": "text", "text": text}]}}))
            else:
                lines.append(dumps({"type": "assistant", "message": {"role": "assistant",
                                    "content": [{"type": "tool_use", "id": "t", "name": "Bash", "input": {}}]}}))
                lines.append(None)
        self.write(path, lines, size, lambda n: dumps({"type": "user", "message": {"role": "user", "content": [
            {"type": "tool_result", "tool_use_id": "t", "content": self.tool_output(n)}]}}))

    def codex(self, path, size, words, prompts, tools, probes, cwd, hidden):
        meta = {"id": "meta", "cwd": cwd, "originator": "codex_cli_rs", "source": "cli", "git": {"branch": "main"}}
        if hidden:
            meta.update(thread_source="subagent", parent_thread_id=self.uid())
        lines = [dumps({"type": "session_meta", "payload": meta}),
                 dumps({"type": "turn_context", "payload": {"cwd": cwd}})]
        for role, text in self.turns(words, prompts, tools, probes):
            if role == "tool":
                lines.append(dumps({"type": "response_item", "payload": {"type": "function_call", "name": "shell"}}))
                lines.append(None)
            else:
                kind = "input_text" if role == "user" else "output_text"
                lines.append(dumps({"type": "response_item", "payload": {"type": "message", "role": role,
                                    "content": [{"type": kind, "text": text}]}}))
        self.write(path, lines, size, lambda n: dumps({"type": "response_item", "payload": {
            "type": "function_call_output", "output": self.tool_output(n)}}))

    def grok(self, path, size, words, prompts, tools, probes, cwd):
        lines = []
        for role, text in self.turns(words, prompts, tools, probes):
            if role == "user":
                lines.append(dumps({"type": "user", "content": [{"type": "text",
                                    "text": "<user_query>" + text + "</user_query>"}]}))
            elif role == "assistant":
                lines.append(dumps({"type": "assistant", "content": text, "tool_calls": [{"id": "t"}]}))
            else:
                lines.append(None)
        self.write(path, lines, size, lambda n: dumps({"type": "tool_result", "content": self.tool_output(n)}))
        title = self.words(5)
        path.with_name("summary.json").write_text(dumps({"info": {"cwd": cwd}, "generated_title": title,
                                                         "session_kind": "interactive", "head_branch": "main"}))

    def write(self, path, lines, size, tool_line):
        text_bytes = sum(len(line) + 1 for line in lines if line)
        slots = lines.count(None)
        each = max((size - text_bytes) // slots, 64) if slots else 0
        path.parent.mkdir(parents=True, exist_ok=True)
        with open(path, "w") as f:
            for line in lines:
                f.write((line if line is not None else tool_line(each)) + "\n")


def session_path(root, agent, sid, day, cwd):
    if agent == "claude":
        return root / ".claude/projects" / ("-" + cwd.strip("/").replace("/", "-")) / (sid + ".jsonl")
    if agent == "codex":
        return root / ".codex/sessions" / day.replace("-", "/") / ("rollout-" + day + "T10-00-00-" + sid + ".jsonl")
    return root / ".grok/sessions" / urllib.parse.quote(cwd, safe="") / sid / "chat_history.jsonl"


def generate(root, count, copies, seed):
    corpus = Corpus(seed)
    now = 1_790_000_000
    made = []
    for n in range(count):
        roll = corpus.rng.random()
        agent = "claude" if roll < 0.40 else "codex" if roll < 0.98 else "grok"
        hidden = agent == "codex" and corpus.rng.random() < 0.40
        size, words, prompts, tools, probes, cwd = corpus.plan(agent)
        mtime = now - corpus.rng.uniform(0, 90 * DAY)
        day = "2026-%02d-%02d" % (corpus.rng.randint(7, 9), corpus.rng.randint(1, 28))
        path = session_path(root, agent, corpus.uid(), day, cwd)
        if agent == "claude":
            corpus.claude(path, size, words, prompts, tools, probes, cwd)
        elif agent == "codex":
            corpus.codex(path, size, words, prompts, tools, probes, cwd, hidden)
        else:
            corpus.grok(path, size, words, prompts, tools, probes, cwd)
        for side in (path, path.with_name("summary.json")):
            if side.exists():
                os.utime(side, (mtime, mtime))
        made.append((agent, path, cwd, day))
    for copy in range(1, copies):
        for agent, path, cwd, day in made:
            twin = session_path(root, agent, corpus.uid(), day, cwd + "-" + str(copy))
            twin.parent.mkdir(parents=True, exist_ok=True)
            os.link(path, twin)
            if agent == "grok":
                os.link(path.with_name("summary.json"), twin.with_name("summary.json"))
    (root / ".claude").mkdir(exist_ok=True)
    (root / ".codex").mkdir(exist_ok=True)
    (root / ".grok").mkdir(exist_ok=True)


def append(root, count, seed):
    rng = random.Random(seed)
    files = sorted(root.glob(".claude/projects/*/*.jsonl"))
    before = []
    for path in rng.sample(files, min(count, len(files))):
        info = path.stat()
        before.append((path, info.st_size, info.st_atime_ns, info.st_mtime_ns))
        if info.st_nlink > 1:
            alone = path.with_suffix(".alone")
            shutil.copy2(path, alone)
            os.replace(alone, path)
        with open(path, "a") as f:
            f.write(dumps({"type": "user", "message": {"role": "user", "content": "appended " + str(rng.random())}}) + "\n")
            f.write(dumps({"type": "assistant", "message": {"role": "assistant",
                    "content": [{"type": "text", "text": "appended reply"}]}}) + "\n")
    return before


def restore(before):
    for path, size, atime, mtime in before:
        os.truncate(path, size)
        os.utime(path, ns=(atime, mtime))


def measure(binary, root, cache, *args):
    env = dict(os.environ, HOME=str(root), XDG_CACHE_HOME=str(cache), SUPERFIND_TELEMETRY="off",
               SUPERFIND_CHECKIN_URL="http://127.0.0.1:1/v1/checkin")
    for name in ("CLAUDE_CONFIG_DIR", "CODEX_HOME", "GROK_HOME", "SUPERFIND_CLAUDE_DIRS", "SUPERFIND_CODEX_DIRS",
                 "SUPERFIND_GROK_DIRS"):
        env.pop(name, None)
    start = time.perf_counter()
    child = subprocess.Popen([binary, *args], env=env, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    out = child.stdout.read()
    _, status, usage = os.wait4(child.pid, 0)
    elapsed = time.perf_counter() - start
    if os.waitstatus_to_exitcode(status) != 0:
        sys.exit(f"{binary} {' '.join(args)} exited with {os.waitstatus_to_exitcode(status)}")
    rss = usage.ru_maxrss if sys.platform == "darwin" else usage.ru_maxrss * 1024
    return elapsed, rss, out


def together(binary, root, cache, *args):
    with concurrent.futures.ThreadPoolExecutor(2) as pool:
        runs = list(pool.map(lambda _: measure(binary, root, cache, *args), range(2)))
    return max(r[0] for r in runs), max(r[1] for r in runs), b""


def bench(root, binaries, runs, scratch, seed):
    scratch = Path(tempfile.mkdtemp(prefix="superfind-bench-", dir=scratch))
    steps = [
        ("index, empty superfind cache", lambda b, c: measure(b, root, c, "index")),
        ("warm index", lambda b, c: measure(b, root, c, "index")),
        ("search, 50% of sessions", lambda b, c: measure(b, root, c, "search", "zebrafix")),
        ("search, 10%", lambda b, c: measure(b, root, c, "search", "marmotcache")),
        ("search, 1%", lambda b, c: measure(b, root, c, "search", "quokkaroute")),
        ("refresh after 10 appends", None),
        ("two indexes at once, empty cache", None),
    ]
    searches = [("zebrafix",), ("marmotcache",), ("quokkaroute",), ("zebrafix", "marmotcache"), ("agent:codex", "zebrafix")]
    outputs = {}
    for b in binaries:
        cache = scratch / ("same-%d" % binaries.index(b))
        print("%s: %s" % (Path(b).name, measure(b, root, cache, "index")[2].decode().strip()))
        for words in searches:
            outputs[b, words] = measure(b, root, cache, "search", *words, "--json")[2]
        shutil.rmtree(cache)
    times = {(b, name): [] for b in binaries for name, _ in steps}
    peaks = {(b, name): [] for b in binaries for name, _ in steps}
    for run in range(runs):
        for b in binaries:
            cache = scratch / ("run-%d-%d" % (run, binaries.index(b)))
            for name, step in steps:
                if name == "refresh after 10 appends":
                    appended = append(root, 10, seed * 1000 + run)
                    try:
                        elapsed, rss, _ = measure(b, root, cache, "index")
                    finally:
                        restore(appended)
                elif name == "two indexes at once, empty cache":
                    shutil.rmtree(cache)
                    elapsed, rss, _ = together(b, root, cache, "index")
                else:
                    elapsed, rss, _ = step(b, cache)
                times[b, name].append(elapsed)
                peaks[b, name].append(rss)
            shutil.rmtree(cache)
    scratch.rmdir()
    print("| step | " + " | ".join("%s median (max) | peak RSS" % Path(b).name for b in binaries) + " |")
    print("|---|" + "---|---|" * len(binaries))
    for name, _ in steps:
        cells = []
        for b in binaries:
            t = times[b, name]
            cells.append("%.2f s (%.2f s) | %d MB" % (statistics.median(t), max(t), max(peaks[b, name]) >> 20))
        print("| %s | %s |" % (name, " | ".join(cells)))
    for words in searches:
        same = len({outputs[b, words] for b in binaries}) == 1
        print("search %s --json: %d bytes, %s" % (" ".join(words), len(outputs[binaries[0], words]),
                                                  "identical across binaries" if same else "DIFFERENT across binaries"))

def main():
    parser = argparse.ArgumentParser(description="Write a synthetic Claude Code, Codex and Grok corpus for scale runs, "
                                                 "or time superfind binaries against one.")
    parser.add_argument("root", type=Path)
    parser.add_argument("--sessions", type=int, default=1000)
    parser.add_argument("--copies", type=int, default=1)
    parser.add_argument("--seed", type=int, default=7)
    parser.add_argument("--append", type=int, default=0)
    parser.add_argument("--bench", action="append", metavar="BINARY", help="time this binary; repeat to compare")
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument("--scratch", type=Path, default=Path(tempfile.gettempdir()))
    args = parser.parse_args()
    if args.bench:
        bench(args.root, [str(Path(b).resolve()) for b in args.bench], args.runs, args.scratch, args.seed)
        return
    if args.append:
        append(args.root, args.append, args.seed)
        return
    if args.root.exists():
        sys.exit(f"{args.root} already exists")
    generate(args.root, args.sessions, args.copies, args.seed)


if __name__ == "__main__":
    main()
