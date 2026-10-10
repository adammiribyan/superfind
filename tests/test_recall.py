import calendar
import json
import os
import subprocess
import time
import unittest

from test_superfind import (ANSI, SUPERFIND, SuperfindBehavior, claude, codex_call, codex_meta, codex_output, codex_reply,
                            codex_user, line, tool, uid, user)

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
        for command in ("search", "ask", "last"):
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

    def last(self, *args, pwd="/work/app"):
        result = self.run_in("last", *args, pwd=pwd)
        return result.returncode, ANSI.sub("", result.stdout), result.stderr

    def handoff(self, path, title):
        return f"{title}\n{self.superfind('command', str(path))}"

    def test_last_prints_the_newest_real_session_here_and_the_command_that_resumes_it(self):
        self.worked("app", "/work/app")
        sub = self.session("sub", user("wrap the invoice names", cwd="/work/app/sub"), tool("Edit"), claude("Wrapped."),
                           title="Invoice names")
        self.session("hi", user("hi", cwd="/work/app"), claude("Hello."))
        self.worked("other", "/elsewhere/other")
        self.by_agent("sdk", "/work/app")
        self.assertEqual(self.last(), (0, self.handoff(sub, "Invoice names"), ""))
        [record] = [json.loads(row) for row in self.last("--json")[1].splitlines()]
        self.assertEqual((record["id"], record["kind"], record["match"], record["resume"]),
                         ("sub", "interactive", "", self.superfind("command", str(sub)).strip()))
        self.assertEqual([json.loads(row)["id"] for row in self.last("--json", "is:agent")[1].splitlines()], ["sdk"])
        self.assertEqual(self.last("branch:nope")[:2], (1, ""))
        hi = self.projects / "hi.jsonl"
        self.assertEqual(self.last(pwd="/work/app/sub/deeper")[1], self.handoff(sub, "Invoice names"))
        for name in ("app", "sub"):
            (self.projects / f"{name}.jsonl").unlink()
        self.assertEqual(self.last()[1], self.handoff(hi.resolve(), "hi"))

    def test_last_takes_the_agent_and_every_other_filter(self):
        self.worked("app", "/work/app")
        codex = self.codex_session(uid(1), codex_meta(cwd="/work/app"), codex_user("migrate the ledger index"), codex_call(),
                                   codex_output(), codex_reply("Migrated."))
        self.worked("newest", "/work/app", branch="fix")
        code, out, _ = self.last("agent:codex")
        self.assertEqual((code, out), (0, self.handoff(codex, "migrate the ledger index")))
        self.assertIn(f"codex resume {uid(1)}", out)
        self.assertEqual(self.last("branch:main", "agent:claude")[1].splitlines()[1], self.superfind("command", "app").strip())

    def test_a_session_still_running_says_it_may_be_open(self):
        path = self.worked("app", "/work/app", at=time.time())
        self.assertEqual(self.last()[1].splitlines()[0], "fix the ledger · may still be open in another window")
        self.assertIn(f"--resume {path.stem}", self.last()[1])

    def test_last_with_nothing_here_or_words_or_an_unknown_option_says_so(self):
        self.worked("other", "/elsewhere/other")
        self.assertEqual(self.last(), (1, "", "superfind last: no session started in this folder or a folder inside it.\n"))
        self.assertEqual(self.last("agent:codex"),
                         (1, "", "superfind last: no session that fits these filters started in this folder or a folder inside it.\n"))
        for args in (("ledger",), ("agent:codex", "ledger"), ("--", "--json")):
            self.assertEqual(self.last(*args), (2, "", "superfind last: takes filters, such as agent:codex or branch:main, not words. "
                                                       "To search this folder, use superfind search in:here WORDS.\n"), args)
        self.assertEqual(self.last("--frobnicate"), (2, "", "superfind last: there is no --frobnicate option. See superfind help last.\n"))

    def test_last_explains_itself(self):
        for args in (("help", "last"), ("last", "--help"), ("last", "-h")):
            result = self.run_in(*args)
            self.assertEqual((result.returncode, result.stderr), (0, ""), args)
            self.assertTrue(result.stdout.startswith("superfind last [FILTERS] [--json]\n"), args)
        self.assertIn("superfind last [FILTERS] [--json]", self.superfind("--help"))


def load_tests(loader, tests, pattern):
    own = sorted(name for name in vars(Recall) if name.startswith("test_"))
    return unittest.TestSuite(Recall(name) for name in own)


if __name__ == "__main__":
    unittest.main()
