import json
import os
import shutil
import sys
import time
from pathlib import Path

SESSIONS = [
    ("storefront", "Checkout button double-charges on slow networks", "feat/checkout-guard", [
        ("user", "customers on slow connections sometimes get charged twice when they tap Pay again"),
        ("claude", "The Pay button stays enabled while the request is in flight, so a second tap sends a second "
                   "charge. I'll disable it on submit and send an idempotency key with each charge, so a retried "
                   "request is applied once."),
        ("user", "add a test that taps twice"),
        ("claude", "Added a Playwright test that taps Pay twice on a throttled network. It now passes: one charge, "
                   "one receipt. The fix is on feat/checkout-guard."),
    ]),
    ("payments-api", "Retry webhooks without duplicate refunds", "main", [
        ("user", "our refund webhook sometimes runs twice when Stripe retries it"),
        ("claude", "Stripe retries any webhook that doesn't answer within 10 seconds. The handler issues the refund "
                   "before recording the event id, so a retry refunds again. I'll record the event id first and "
                   "skip ids we've already seen."),
        ("user", "ship it"),
        ("claude", "Merged. Replaying last week's webhook log produced zero duplicate refunds."),
    ]),
    ("storefront", "Image CDN migration", "main", [
        ("user", "move product images from S3 to the image CDN and keep old URLs working"),
        ("claude", "Product images now load from the CDN with automatic WebP. Old S3 URLs redirect with a 301, so "
                   "existing links and caches keep working. Largest image on the home page dropped from 410 KB to "
                   "96 KB."),
    ]),
    ("docs-site", "Search results ignore synonyms", "main", [
        ("user", "searching the docs for 'sign in' doesn't find the login guide"),
        ("claude", "The docs search only matches exact words. I added a synonym list (sign in, log in, login, "
                   "authenticate) at index time, so all four find the login guide."),
    ]),
    ("payments-api", "Monthly invoice PDF layout", "feat/invoice-pdf", [
        ("user", "the invoice PDF cuts off long company names"),
        ("claude", "Long names now wrap onto two lines and the totals table moved down to make room. I checked it "
                   "against the ten longest customer names in production."),
    ]),
    ("mobile-app", "Crash on launch for iOS 18 users", "fix/launch-crash", [
        ("user", "Crashlytics shows a spike of launch crashes on iOS 18"),
        ("claude", "The crash comes from reading a keychain item before the device is unlocked. I deferred the read "
                   "until the app is active. The symbolicated stack trace matches all 212 reports."),
        ("user", "can you also add a regression test"),
        ("claude", "Added a UI test that launches with the keychain locked. Ready for review."),
    ]),
    ("infra", "Cut staging costs", "main", [
        ("user", "staging costs almost as much as production, why"),
        ("claude", "Staging runs the same instance sizes as production around the clock. Scheduling it to stop "
                   "outside working hours and halving the database size saves about $1,900 a month."),
    ]),
    ("docs-site", "Dark mode code blocks", "main", [
        ("user", "code blocks are unreadable in dark mode"),
        ("claude", "The code theme was hard-coded to the light palette. Code blocks now follow the site theme, and "
                   "contrast passes WCAG AA in both."),
    ]),
    ("mobile-app", "Onboarding copy review", "main", [
        ("user", "make the onboarding screens shorter and friendlier"),
        ("claude", "Cut each screen to one sentence and one button. Reading time went from about 40 to 15 seconds."),
    ]),
    ("infra", "hi", "main", [("user", "hi")]),
]


def line(**entry):
    return json.dumps(entry, separators=(",", ":"))


def main(home):
    home = Path(home)
    shutil.rmtree(home, ignore_errors=True)
    now = time.time()
    for age, (project, title, branch, turns) in enumerate(SESSIONS):
        cwd = home / "code" / project
        cwd.mkdir(parents=True, exist_ok=True)
        folder = home / ".claude" / "projects" / ("-" + str(cwd).strip("/").replace("/", "-").replace(".", "-"))
        folder.mkdir(parents=True, exist_ok=True)
        lines = []
        for role, text in turns:
            if role == "user":
                lines.append(line(type="user", cwd=str(cwd), gitBranch=branch,
                                  message={"role": "user", "content": text}))
            else:
                lines.append(line(type="assistant", message={"role": "assistant", "content": [
                    {"type": "tool_use", "id": f"tool-{len(lines)}", "name": "Edit", "input": {}}]}))
                lines.append(line(type="assistant", message={"role": "assistant",
                                                             "content": [{"type": "text", "text": text}]}))
        lines.append(line(type="ai-title", aiTitle=title))
        path = folder / f"demo-{age:02d}.jsonl"
        path.write_text("\n".join(lines) + "\n")
        stamp = now - 600 - age * age * 5400
        os.utime(path, (stamp, stamp))


if __name__ == "__main__":
    main(sys.argv[1])
