import json
import os
import shlex
import shutil
import sys
import time
import urllib.parse
import uuid as uuid_mod
from datetime import datetime, timezone
from pathlib import Path

AGENTS = {
    "Dedupe Stripe refund webhooks": "codex",
    "Search results ignore synonyms": "grok",
    "Cut staging costs": "codex",
    "Onboarding copy review": "grok",
}

SESSIONS = [
    ("store", "Double charge on slow checkouts", "feat/checkout-guard", [
        ("user", "customers on slow connections sometimes get charged twice when they tap Pay again"),
        ("claude", "The Pay button stays enabled while the request is in flight, so a second tap sends a second "
                   "charge. I'll disable it on submit and send an idempotency key with each charge, so a retried "
                   "request is applied once."),
        ("user", "add a test that taps twice"),
        ("claude", "Added a Playwright test that taps Pay twice on a throttled network. It now passes: one charge, "
                   "one receipt. The fix is on feat/checkout-guard."),
        ("user", "refund the customers who were already charged twice"),
        ("claude", "Refunded the 37 duplicate charges from the past two weeks and emailed each customer a receipt."),
    ]),
    ("billing", "Dedupe Stripe refund webhooks", "main", [
        ("user", "our refund webhook sometimes runs twice when Stripe retries it"),
        ("claude", "Stripe retries any webhook that doesn't answer within 10 seconds. The handler issues the refund "
                   "before recording the event id, so a retry refunds again. I'll record the event id first and "
                   "skip ids we've already seen."),
        ("user", "run the webhook tests"),
        ("claude", "All 41 pass. One was flaky on the second run; I made its wait for the retry explicit."),
        ("user", "good, merge it and replay last week's webhook log"),
        ("claude", "Merged. Replaying last week's webhook log produced zero duplicate refunds."),
    ]),
    ("store", "Image CDN migration", "main", [
        ("user", "move product images from S3 to the image CDN and keep old URLs working"),
        ("claude", "Product images now load from the CDN with automatic WebP. Old S3 URLs redirect with a 301, so "
                   "existing links and caches keep working. Largest image on the home page dropped from 410 KB to "
                   "96 KB."),
    ]),
    ("docs", "Search results ignore synonyms", "main", [
        ("user", "searching the docs for 'sign in' doesn't find the login guide"),
        ("claude", "The docs search only matches exact words. I added a synonym list (sign in, log in, login, "
                   "authenticate) at index time, so all four find the login guide."),
    ]),
    ("billing", "Monthly invoice PDF layout", "feat/invoice-pdf", [
        ("user", "the invoice PDF cuts off long company names"),
        ("claude", "Long names now wrap onto two lines and the totals table moved down to make room. I checked it "
                   "against the ten longest customer names in production."),
    ]),
    ("mobile", "Launch crash on iOS 27", "fix/launch-crash", [
        ("user", "Crashlytics shows a spike of launch crashes on iOS 27"),
        ("claude", "The crash comes from reading a keychain item before the device is unlocked. I deferred the read "
                   "until the app is active. The symbolicated stack trace matches all 212 reports."),
        ("user", "can you also add a regression test"),
        ("claude", "Added a UI test that launches with the keychain locked. Ready for review."),
    ]),
    ("infra", "Cut staging costs", "main", [
        ("user", "staging costs almost as much as production, why"),
        ("claude", "Staging runs the same instance sizes as production around the clock. Scheduling it to stop "
                   "outside working hours and halving the database size would save about $1,900 a month. The "
                   "schedule is ready, but it would stop staging during Monday's release rehearsal. Should I apply "
                   "it now, or after Monday?"),
    ]),
    ("docs", "Dark mode code blocks", "main", [
        ("user", "code blocks are unreadable in dark mode"),
        ("claude", "The code theme was hard-coded to the light palette. Code blocks now follow the site theme, and "
                   "contrast passes WCAG AA in both."),
    ]),
    ("mobile", "Onboarding copy review", "main", [
        ("user", "make the onboarding screens shorter and friendlier"),
        ("claude", "Cut each screen to one sentence and one button. Reading time went from about 40 to 15 seconds."),
    ]),
    ("infra", "hi", "main", [("user", "hi")]),
]


def line(**entry):
    return json.dumps(entry, separators=(",", ":"))


def claude_lines(cwd, branch, title, turns):
    lines = []
    for role, text in turns:
        if role == "user":
            lines.append(line(type="user", cwd=str(cwd), gitBranch=branch, message={"role": "user", "content": text}))
        else:
            lines.append(line(type="assistant", message={"role": "assistant", "content": [
                {"type": "tool_use", "id": f"tool-{len(lines)}", "name": "Edit", "input": {}}]}))
            lines.append(line(type="assistant", message={"role": "assistant",
                                                         "content": [{"type": "text", "text": text}]}))
    lines.append(line(type="ai-title", aiTitle=title))
    return lines


def codex_lines(cwd, branch, uuid, turns):
    lines = [line(type="session_meta", payload={"id": uuid, "cwd": str(cwd), "git": {"branch": branch}})]
    for role, text in turns:
        if role == "user":
            lines.append(line(type="response_item", payload={"type": "message", "role": "user",
                                                             "content": [{"type": "input_text", "text": text}]}))
        else:
            lines.append(line(type="response_item", payload={"type": "function_call", "name": "shell"}))
            lines.append(line(type="response_item", payload={"type": "function_call_output"}))
            lines.append(line(type="response_item", payload={"type": "message", "role": "assistant",
                                                             "content": [{"type": "output_text", "text": text}]}))
    return lines


def grok_lines(turns):
    lines = []
    for n, (role, text) in enumerate(turns):
        if role == "user":
            lines.append(line(type="user", content=[{"type": "text", "text": f"<user_query>\n{text}\n</user_query>"}],
                              prompt_index=n))
        else:
            lines.append(line(type="assistant", content="", tool_calls=[
                {"id": f"call-{n}", "name": "run_terminal_command", "arguments": "{}"}]))
            lines.append(line(type="tool_result", tool_call_id=f"call-{n}", content="ok"))
            lines.append(line(type="assistant", content=text))
    return lines


def main(home):
    home = Path(home)
    shutil.rmtree(home, ignore_errors=True)
    real_claude, real_home = os.environ.get("DEMO_CLAUDE", ""), os.environ.get("DEMO_HOME", "")
    for name in ("claude", "codex", "grok"):
        stand_in = home / "bin" / name
        stand_in.parent.mkdir(parents=True, exist_ok=True)
        script = "#!/bin/sh\n"
        if name == "claude" and real_claude and real_home:
            script += f'[ "$1" = -p ] && HOME={shlex.quote(real_home)} exec {shlex.quote(real_claude)} "$@"\n'
        stand_in.write_text(script + "exit 0\n")
        stand_in.chmod(0o755)
    now = time.time()
    for age, (project, title, branch, turns) in enumerate(SESSIONS):
        cwd = home / "code" / project
        cwd.mkdir(parents=True, exist_ok=True)
        stamp = now - 600 - age * age * 5400
        uuid = str(uuid_mod.uuid5(uuid_mod.NAMESPACE_URL, "superfind-demo/" + title))
        when = datetime.fromtimestamp(stamp, timezone.utc)
        agent = AGENTS.get(title, "claude")
        if agent == "claude":
            folder = home / ".claude" / "projects" / ("-" + str(cwd).strip("/").replace("/", "-").replace(".", "-"))
            paths = [(folder / f"{uuid}.jsonl", claude_lines(cwd, branch, title, turns))]
        elif agent == "codex":
            folder = home / ".codex" / "sessions" / when.strftime("%Y") / when.strftime("%m") / when.strftime("%d")
            paths = [(folder / f"rollout-{when.strftime('%Y-%m-%dT%H-%M-%S')}-{uuid}.jsonl", codex_lines(cwd, branch, uuid, turns))]
            (home / ".codex").mkdir(parents=True, exist_ok=True)
            with open(home / ".codex" / "session_index.jsonl", "a") as index:
                index.write(line(id=uuid, thread_name=title, updated_at=when.isoformat().replace("+00:00", "Z")) + "\n")
        else:
            folder = home / ".grok" / "sessions" / urllib.parse.quote(str(cwd), safe="") / uuid
            paths = [(folder / "chat_history.jsonl", grok_lines(turns)),
                     (folder / "summary.json", [json.dumps({"info": {"id": uuid, "cwd": str(cwd)},
                                                            "session_summary": title, "head_branch": branch})])]
        folder.mkdir(parents=True, exist_ok=True)
        for path, lines in paths:
            path.write_text("\n".join(lines) + "\n")
            os.utime(path, (stamp, stamp))


if __name__ == "__main__":
    main(sys.argv[1])
