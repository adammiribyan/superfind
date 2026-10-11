#!/usr/bin/env python3
"""Write llms-full.txt, the whole superfind reference in one file for AI assistants.

  scripts/llms.py            write llms-full.txt at the repo root
  scripts/llms.py PATH       write it to PATH instead

The release workflow runs it and attaches the file to each release.

It builds the file from llms.txt, the help that the built binary prints, and the
JSON that the binary prints for a made-up session in a throwaway home folder. Set
SUPERFIND_BIN to use another build than bin/superfind.
"""
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SUPERFIND = Path(os.environ.get("SUPERFIND_BIN", ROOT / "bin" / "superfind"))
OUTPUT = ROOT / "llms-full.txt"
RAW = "https://raw.githubusercontent.com/adammiribyan/superfind/main/"
EXAMPLE_HOME = "/Users/you"
EXAMPLE_TIME = 1788256800

sys.path.insert(0, str(ROOT / "tests"))
from test_superfind import claude, line, tool, uid, user

FIELDS = {
    "schema": "The version of this record format, `1` today.",
    "agent": "`claude`, `codex`, or `grok`.",
    "id": "The agent's own session id, the one its resume command takes.",
    "path": "The transcript file. The one field that always names exactly one session.",
    "cwd": "The folder the session ran in.",
    "home": "The agent's home folder that holds the transcript, such as `~/.claude`.",
    "title": "The session's title: the one you or the agent gave it, or else its first request. "
             "Common keys and passwords in it are masked.",
    "project": "The last part of the session's folder, for display. For a Claude Code worktree under `.claude/worktrees`, the main checkout's folder.",
    "branch": "The git branch the transcript last recorded, or empty.",
    "updated": "When the transcript last changed, in UTC.",
    "prompts": "How many requests the session has, at least 1. In a session another program drove, its requests count.",
    "status": "`waiting` or `unfinished` when a TypeSafe label says so, and empty otherwise.",
    "active": "True when the transcript changed in the last two minutes, so the session may still be open "
              "somewhere. It doesn't mean the agent is running.",
    "open": "`null` unless a running agent has the session open. Then `pid` is that agent's process id, `status` is "
            "`busy` or `idle` for Claude Code and empty when the agent doesn't say, and `tmux` is the pane it runs in, "
            "as `{\"pane\": \"%3\", \"session\": \"work\", \"window\": 1}`, or `null` when superfind can't see the pane. "
            "Codex counts as open only when it was started with `codex resume <id>`.",
    "folder_gone": "True when the session's folder no longer exists. `resume` then moves to the nearest parent that does.",
    "resume": "The shell command that resumes the session, with every value quoted for the shell.",
    "kind": "`agent` when another program started the session, such as `codex exec` or `claude -p`, and "
            "`interactive` otherwise.",
    "match": "On search and ask results, the passage that holds your words, or empty. Always empty in `show --json`.",
    "outcomes": "In `show --json`: the commits the session made and the pull requests it opened, in the order it made "
                "them, judged from local git as of your last fetch. Commits the repository no longer has are left out. "
                "A merge means the change was accepted, not that it was right.",
    "total_turns": "In `show --json`: how many turns the session has.",
    "omitted_turns": "In `show --json`: how many earlier turns were left out.",
    "truncated": "In `show --json`: true when `--max-bytes` cut anything. Leaving turns out with `--tail` doesn't count.",
    "turns": "In `show --json`: the conversation, oldest first.",
}

TURN_FIELDS = {
    "role": "`user` or `assistant`.",
    "text": "What was said, with tool calls and system text left out. Common keys and passwords are masked.",
    "cut": "Present and true only on a turn whose start was cut to fit `--max-bytes`.",
}


OUTCOME_FIELDS = {
    "kind": "`commit` for a commit the session made, `pull_request` for a pull request it opened that none of its "
            "commits already stand for.",
    "commit": "The full hash of the commit the session made. Empty for a pull request, and for a commit that a squash "
              "merge carried after its branch was deleted.",
    "pr": "The pull request that carried the commit, or that the session opened, or `0` when none is known.",
    "subject": "The commit's subject from git. For a pull request, the subject it landed with, or empty.",
    "repo": "The repository's top folder.",
    "stage": "How far it got. A commit is `committed`, `pushed` (on a remote branch), `merged` (on the default branch, "
             "directly or in a squash), or `reverted` (merged, then a later commit on the default branch reverts it). "
             "A pull request is `opened`, `merged`, or `reverted`.",
    "branch": "The default branch it is measured against, such as `main`, or empty when none was found.",
    "landed": "The hash of the default branch's commit that carries it: the commit itself, its amended or rebased copy, "
              "or its squash. Empty when not merged.",
    "landed_at": "When that commit was committed, in UTC. Empty when not merged.",
    "reverted_by": "The hash of the default branch's commit that reverts it, or empty.",
}


def git(folder, *args):
    when = f"@{EXAMPLE_TIME - 600} +0000"
    env = {"HOME": str(folder), "PATH": os.environ.get("PATH", ""), "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "You",
           "GIT_AUTHOR_EMAIL": "you@example.com", "GIT_COMMITTER_NAME": "You", "GIT_COMMITTER_EMAIL": "you@example.com",
           "GIT_AUTHOR_DATE": when, "GIT_COMMITTER_DATE": when}
    subprocess.run(["git", *args], cwd=folder, env=env, capture_output=True, check=True)


def run(home, *args):
    env = {"HOME": str(home), "XDG_CACHE_HOME": str(home / ".cache"), "PATH": os.environ.get("PATH", ""),
           "NO_COLOR": "1", "TYPESAFE_API_KEY": "", "COLUMNS": "100", "SUPERFIND_TELEMETRY": "off", "SUPERFIND_CHECKIN_URL": "http://127.0.0.1:1/v1/checkin"}
    result = subprocess.run([str(SUPERFIND), *args], env=env, capture_output=True, encoding="utf-8")
    if result.returncode != 0 or result.stderr:
        sys.exit(f"scripts/llms.py: superfind {' '.join(args)} exited {result.returncode}: {result.stderr}")
    return result.stdout


def example_home(home):
    folder = home / "code" / "storefront"
    folder.mkdir(parents=True)
    project = home / ".claude" / "projects" / ("-" + str(folder).strip("/").replace("/", "-"))
    project.mkdir(parents=True)
    git(folder, "init", "-q", "-b", "main")
    (folder / "pay.go").write_text("package pay\n")
    git(folder, "add", "-A")
    git(folder, "commit", "-q", "-m", "fix: send one idempotency key per cart")
    transcript = project / f"{uid(1)}.jsonl"
    transcript.write_text(encoding="utf-8", data="\n".join([
        user("the checkout button double-charges customers on slow networks", cwd=str(folder)),
        tool("Grep"),
        claude("The retry in pay() resends the charge after a timeout. Each request now carries an idempotency "
               "key derived from the cart id, so a retry can't charge twice."),
        user("add a test for the slow network", cwd=str(folder)),
        tool("Edit"),
        claude("Added TestRetryUsesSameKey with a transport that times out once."),
        line(type="assistant", timestamp="2026-09-01T09:50:00.000Z", message={"role": "assistant", "content": [
            {"type": "tool_use", "id": "t2", "name": "Bash",
             "input": {"command": 'git commit -qam "fix: send one idempotency key per cart"'}}]}),
        line(type="ai-title", aiTitle="Checkout double-charges on slow networks"),
    ]) + "\n")
    os.utime(transcript, (EXAMPLE_TIME, EXAMPLE_TIME))


def anonymize(text, home):
    encoded = "-" + str(home).strip("/").replace("/", "-")
    return text.replace(str(home), EXAMPLE_HOME).replace(encoded, "-" + EXAMPLE_HOME.strip("/").replace("/", "-"))


def overview():
    text = (ROOT / "llms.txt").read_text(encoding="utf-8")
    return text[:text.index("\n## ")].strip()


def commands(home):
    result = subprocess.run([str(SUPERFIND), "no-such-command"], capture_output=True, encoding="utf-8",
                            env={"HOME": str(home), "PATH": os.environ.get("PATH", ""), "SUPERFIND_TELEMETRY": "off", "SUPERFIND_CHECKIN_URL": "http://127.0.0.1:1/v1/checkin"})
    if result.returncode != 2:
        sys.exit(f"scripts/llms.py: an unknown command exited {result.returncode}, not 2: {result.stderr}")
    listed = re.search(r"The commands are (.+)\.\n", result.stderr).group(1)
    return [c.strip() for c in re.split(r", and |, ", listed)] + ["version"]


def fence(text):
    return "```text\n" + text.rstrip("\n") + "\n```"


def fields(found, glossary):
    missing = [k for k in found if k not in glossary]
    if missing:
        sys.exit("scripts/llms.py: describe these JSON fields in FIELDS or TURN_FIELDS: " + ", ".join(missing))
    return "\n".join(f"- `{k}`: {glossary[k]}" for k in found)


def build():
    with tempfile.TemporaryDirectory(prefix="superfind-llms-", dir="/tmp") as tmp:
        home = Path(os.path.realpath(tmp))
        if not re.fullmatch(r"[A-Za-z0-9/._-]+", str(home)):
            sys.exit(f"scripts/llms.py: the example home {home} needs a plain path")
        example_home(home)
        found = json.loads(run(home, "search", "--json", "idempotency").splitlines()[0])
        shown = json.loads(run(home, "show", uid(1), "--json"))
        record_keys = list(found) + [k for k in shown if k not in found and k != "turns"] + ["turns"]
        cut = json.loads(run(home, "show", uid(1), "--json", "--max-bytes", "20"))
        turn_keys = list(dict.fromkeys(k for t in shown["turns"] + cut["turns"] for k in t))
        if sorted(turn_keys) != sorted(TURN_FIELDS):
            sys.exit("scripts/llms.py: TURN_FIELDS must match the turn fields superfind prints: " + ", ".join(turn_keys))
        outcome_keys = list(dict.fromkeys(k for o in shown["outcomes"] for k in o))
        if sorted(outcome_keys) != sorted(OUTCOME_FIELDS):
            sys.exit("scripts/llms.py: OUTCOME_FIELDS must match the outcome fields superfind prints: " + ", ".join(outcome_keys))
        unused = [k for k in FIELDS if k not in record_keys]
        if unused:
            sys.exit("scripts/llms.py: these described fields no longer appear in the JSON: " + ", ".join(unused))
        names = commands(home)
        parts = [
            overview(),
            "This file is the full reference in one place, for AI assistants and agents. scripts/llms.py writes it "
            "from llms.txt, the help that superfind prints, and the JSON it prints for an example session. "
            "Paths in the examples are shown under /Users/you.",
            "## Commands\n\n`superfind --help`:\n\n" + fence(run(home, "--help")),
        ]
        for name in names:
            parts.append(f"### superfind help {name}\n\n" + fence(run(home, "help", name)))
        search_line = anonymize(run(home, "search", "--json", "idempotency").splitlines()[0], home)
        show_json = anonymize(json.dumps(shown, ensure_ascii=False, indent=1), home)
        parts += [
            "## JSON output\n\n`search --json` and `ask --json` print JSON Lines: one object per session, one per "
            "line, best match first, or newest first when there are no words. Use `jq -s` to get an array. `show --json` prints one object. Every "
            "session object has these fields:\n\n" + fields(record_keys, FIELDS),
            "Each item in `outcomes` has:\n\n" + fields(outcome_keys, OUTCOME_FIELDS),
            "Each item in `turns` has:\n\n" + fields(turn_keys, TURN_FIELDS),
            "### Example: `superfind search --json idempotency`\n\n```json\n" + search_line + "\n```",
            f"### Example: `superfind show {uid(1)} --json`, indented here\n\n```json\n" + show_json + "\n```",
            "## Links\n\n"
            "- Repository: https://github.com/adammiribyan/superfind\n"
            "- Releases: https://github.com/adammiribyan/superfind/releases\n"
            f"- Changelog: {RAW}CHANGELOG.md\n"
            f"- README: {RAW}README.md\n"
            f"- Short index for AI assistants: {RAW}llms.txt",
        ]
        return anonymize("\n\n".join(parts) + "\n", home)


def main():
    output = Path(sys.argv[1]) if len(sys.argv) > 1 else OUTPUT
    output.write_text(build(), encoding="utf-8")


if __name__ == "__main__":
    main()
