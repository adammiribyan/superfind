import calendar
import json
import os
import subprocess
import time
import unittest

from test_superfind import SUPERFIND, SuperfindBehavior, claude, line, tool, user

WHEN = "takes a day like 2026-10-09, or today, yesterday, or a time ago like 12h, 3d, or 2w."
MALFORMED = {
    "after:3days": "after: " + WHEN,
    "before:2026-1": "before: " + WHEN,
    "after:2026-02-30": "after: " + WHEN,
    "after:": "after: " + WHEN,
    "in:there": "in: takes here, as in in:here.",
    "branch:": "branch: takes a branch name, like branch:main.",
}


class Recall(SuperfindBehavior):
    def worked(self, session_id, cwd, text="fix the ledger", branch="main", at=None):
        path = self.session(session_id, user(text, cwd=cwd, branch=branch), tool("Bash"), claude("Done."))
        if at is not None:
            os.utime(path, (at, at))
        return path

    def by_agent(self, session_id, cwd):
        return self.session(session_id, line(type="user", cwd=cwd, entrypoint="sdk-cli",
                                             message={"role": "user", "content": "score the ledger answers"}),
                            tool("Bash"), claude("Scored."))

    def run_in(self, *args, pwd=None, tz=None, cwd=None):
        env = {**self.env, "TZ": tz or "UTC"}
        if pwd is None:
            env.pop("PWD", None)
        else:
            env["PWD"] = pwd
        return subprocess.run([str(SUPERFIND), *args], env=env, capture_output=True, text=True, cwd=cwd)

    def ids(self, *search, pwd="/elsewhere", tz=None, cwd=None):
        result = self.run_in("search", *search, pwd=pwd, tz=tz, cwd=cwd)
        self.assertEqual(result.returncode, 0, result.stderr)
        return sorted(row.split("\t")[2] for row in result.stdout.splitlines())

    def json_ids(self, *search, pwd="/elsewhere"):
        result = self.run_in("search", "--json", *search, pwd=pwd)
        self.assertEqual(result.returncode, 0, result.stderr)
        return sorted(json.loads(row)["id"] for row in result.stdout.splitlines())

    def folders(self):
        self.worked("app", "/work/app")
        self.worked("sub", "/work/app/sub", text="wrap the invoice names")
        self.worked("work", "/work")
        self.worked("application", "/work/application")
        self.worked("other", "/elsewhere/other")
        self.worked("home", str(self.home))

    def test_in_here_keeps_this_folder_the_folders_inside_it_and_the_folders_above_it(self):
        self.folders()
        self.assertEqual(self.ids("in:here", pwd="/work/app"), ["app", "sub", "work"])
        self.assertEqual(self.ids("IN:HERE", pwd="/work/app/sub/"), ["app", "sub", "work"])
        self.assertEqual(self.ids("in:here", pwd="/work"), ["app", "application", "sub", "work"])
        self.assertEqual(self.ids("in:here", pwd="/"), ["app", "application", "home", "other", "sub", "work"])
        self.assertEqual(self.ids("in:here", pwd=str(self.home)), ["home"])
        self.assertEqual(self.ids("in:here", pwd=str(self.home / "shop")), [])
        self.assertEqual(self.ids("in:here", "ledger", pwd="/work/app"), ["app", "work"])
        self.assertEqual(self.ids("ledger", "in:here", pwd="/work/app/sub"), ["app", "work"])
        self.assertEqual(self.json_ids("in:here", pwd="/work/app"), self.ids("in:here", pwd="/work/app"))

    def test_without_pwd_in_here_uses_the_folder_superfind_runs_in(self):
        project = self.home / "shop"
        (project / "web").mkdir(parents=True)
        self.worked("shop", str(project))
        self.worked("other", "/elsewhere/other")
        self.assertEqual(self.ids("in:here", pwd=None, cwd=project / "web"), ["shop"])
        self.assertEqual(self.ids("in:here", pwd="shop/web", cwd=project / "web"), ["shop"])

    def test_in_here_sees_through_a_symlinked_folder(self):
        real = self.home / "src" / "app"
        (real / "web").mkdir(parents=True)
        (self.home / "link").symlink_to(self.home / "src")
        self.worked("real", str(real.resolve()))
        self.worked("linked", str(self.home / "link" / "app" / "web"))
        self.assertEqual(self.ids("in:here", pwd=str(self.home / "link" / "app")), ["linked", "real"])
        self.assertEqual(self.ids("in:here", pwd=str(real)), ["linked", "real"])

    def test_branch_keeps_sessions_that_ended_on_any_named_branch(self):
        self.worked("main", "/work/app", branch="main")
        self.worked("auth", "/work/app", branch="feat/auth")
        self.worked("none", "/work/app", branch="")
        self.assertEqual(self.ids("branch:MAIN"), ["main"])
        self.assertEqual(self.ids("branch:main", "branch:feat/auth"), ["auth", "main"])
        self.assertEqual(self.ids("branch:mai"), [])
        self.assertEqual(self.ids("branch:feat/auth", "ledger"), ["auth"])

    def test_after_and_before_count_days_in_your_time_zone(self):
        late = calendar.timegm((2026, 10, 9, 22, 30, 0))
        self.worked("late", "/work/app", at=late)
        self.worked("early", "/work/app", at=late - 12 * 3600)
        for tz in ("UTC", "Europe/Berlin"):
            self.assertEqual(self.ids("after:2026-10-09", tz=tz), ["early", "late"], tz)
        self.assertEqual(self.ids("after:2026-10-09", "before:2026-10-10", tz="UTC"), ["early", "late"])
        self.assertEqual(self.ids("after:2026-10-09", "before:2026-10-10", tz="Europe/Berlin"), ["early"])
        self.assertEqual(self.ids("after:2026-10-10", tz="UTC"), [])
        self.assertEqual(self.ids("after:2026-10-10", tz="Europe/Berlin"), ["late"])
        self.assertEqual(self.ids("before:2026-10-10", tz="UTC"), ["early", "late"])
        self.assertEqual(self.ids("before:2026-10-10", tz="Europe/Berlin"), ["early"])
        self.assertEqual(self.ids("after:2026-10-10", "before:2026-10-09"), [])

    def test_after_and_before_take_today_yesterday_and_a_time_ago(self):
        now = time.time()
        self.worked("hour", "/work/app", at=now - 3600, branch="fix")
        self.worked("days", "/work/app", at=now - 3 * 86400 - 600, branch="fix")
        self.worked("weeks", "/elsewhere", at=now - 15 * 86400, branch="fix")
        self.assertEqual(self.ids("after:2h"), ["hour"])
        self.assertEqual(self.ids("before:2h"), ["days", "weeks"])
        self.assertEqual(self.ids("after:3d"), ["hour"])
        self.assertEqual(self.ids("after:4d"), ["days", "hour"])
        self.assertEqual(self.ids("before:2w"), ["weeks"])
        self.assertEqual(self.ids("after:yesterday"), ["hour"])
        self.assertEqual(self.ids("after:today", "before:yesterday"), [])
        self.assertEqual(self.ids("in:here", "branch:FIX", "after:1w", pwd="/work/app"), ["days", "hour"])
        self.assertEqual(self.ids("in:here", "branch:main", "after:1w", pwd="/work/app"), [])

    def test_json_search_includes_sessions_another_program_started_here(self):
        self.worked("typed", "/work/app")
        self.by_agent("sdk", "/work/app")
        self.by_agent("away", "/elsewhere")
        self.assertEqual(self.ids("in:here", pwd="/work/app"), ["typed"])
        self.assertEqual(self.json_ids("in:here", pwd="/work/app"), ["sdk", "typed"])
        self.assertEqual(self.ids("in:here", "is:agent", pwd="/work/app"), ["sdk"])

    def test_a_filter_that_cannot_be_read_is_a_usage_mistake_before_any_work(self):
        log = self.logging_claude()
        for command in ("search", "ask"):
            for token, hint in MALFORMED.items():
                for args in ((command, "ledger", token), (command, "--json", token)):
                    result = self.run_in(*args)
                    self.assertEqual((result.returncode, result.stdout, result.stderr),
                                     (2, "", f"superfind {command}: can't read {token}. {hint}\n"), args)
        self.assertFalse(log.exists())
        self.assertFalse((self.home / ".cache" / "superfind" / "index.json").exists())

    def test_ask_with_a_filter_that_cannot_be_read_never_asks_a_model(self):
        log = self.logging_claude()
        self.worked("app", "/work/app")
        self.superfind("index")
        result = self.run_in("ask", "where", "we", "stopped", "after:2026-1", pwd="/work/app")
        self.assertEqual((result.returncode, result.stdout), (2, ""))
        self.assertFalse(log.exists())
        self.run_in("ask", "where", "we", "stopped", "after:2000-01-01", pwd="/work/app")
        self.assertTrue(log.exists())

    def test_is_and_agent_values_it_does_not_know_are_still_ignored(self):
        self.worked("app", "/work/app")
        self.assertEqual(self.ids("is:bogus", "agent:bogus", "ledger"), ["app"])
        self.assertEqual(self.ids("foo:bar"), [])


def load_tests(loader, tests, pattern):
    own = sorted(name for name in vars(Recall) if name.startswith("test_"))
    return unittest.TestSuite(Recall(name) for name in own)


if __name__ == "__main__":
    unittest.main()
