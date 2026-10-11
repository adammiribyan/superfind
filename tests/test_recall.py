import calendar
import json
import os
import shutil
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

from test_blame import claude_result, claude_tool, codex_at, edited, grok_call
from test_superfind import (ANSI, GROK_SYSTEM, SUPERFIND, SuperfindBehavior, claude, codex_call, codex_meta, codex_output, codex_reply,
                            codex_user, grok_user, line, tool, uid, user)

WHEN = "takes a date like 2026-10-09, yesterday, or 3d."
MALFORMED = {
    "after:3days": "after: " + WHEN,
    "before:2026-1": "before: " + WHEN,
    "after:2026-02-30": "after: " + WHEN,
    "after:": "after: " + WHEN,
    "in:there": "in: takes here, as in in:here.",
    "branch:": "branch: takes a branch name, like branch:main.",
    "file:": "file: takes a file or folder, like file:main.go or file:internal/.",
    "has:commits": "has: takes commit, as in has:commit.",
}


KEEP = object()


class Recall(SuperfindBehavior):
    def setUp(self):
        super().setUp()
        self.root = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.app = self.place("work/app")

    def place(self, folder):
        path = self.root / folder
        path.mkdir(parents=True, exist_ok=True)
        return path

    def worked(self, session_id, cwd, text="fix the ledger", branch="main", at=None):
        path = self.session(session_id, user(text, cwd=str(cwd), branch=branch), tool("Bash"), claude("Done."))
        if at is not None:
            os.utime(path, (at, at))
        return path

    def by_agent(self, session_id, cwd):
        return self.session(session_id, line(type="user", cwd=str(cwd), entrypoint="sdk-cli",
                                             message={"role": "user", "content": "score the ledger answers"}),
                            tool("Bash"), claude("Scored."))

    def run_in(self, *args, here=None, pwd=KEEP, tz=None):
        here = self.place("elsewhere") if here is None else here
        env = {**self.env, "TZ": tz or "UTC", "PWD": str(here) if pwd is KEEP else pwd}
        if env["PWD"] is None:
            del env["PWD"]
        return subprocess.run([str(SUPERFIND), *args], env=env, capture_output=True, text=True, cwd=here, stdin=subprocess.DEVNULL)

    def ids(self, *search, here=None, pwd=KEEP, tz=None):
        result = self.run_in("search", *search, here=here, pwd=pwd, tz=tz)
        self.assertEqual(result.returncode, 0, result.stderr)
        return sorted(row.split("\t")[2] for row in result.stdout.splitlines())

    def json_ids(self, *search, here=None):
        result = self.run_in("search", "--json", *search, here=here)
        self.assertEqual(result.returncode, 0, result.stderr)
        return sorted(json.loads(row)["id"] for row in result.stdout.splitlines())

    def folders(self):
        self.worked("app", self.place("work/app"))
        self.worked("sub", self.place("work/app/sub"), text="wrap the invoice names")
        self.worked("work", self.place("work"))
        self.worked("application", self.place("work/application"))
        self.worked("other", self.place("elsewhere/other"))
        self.worked("home", self.home)

    def test_in_here_keeps_this_folder_and_the_folders_above_it_but_not_the_ones_inside_it(self):
        self.folders()
        app, sub = self.place("work/app"), self.place("work/app/sub")
        self.assertEqual(self.ids("in:here", here=app), ["app", "work"])
        self.assertEqual(self.ids("IN:HERE", here=sub, pwd=f"{sub}/"), ["app", "sub", "work"])
        self.assertEqual(self.ids("in:here", here=self.place("work")), ["work"])
        self.assertEqual(self.ids("in:here", here=Path("/")), [])
        self.assertEqual(self.ids("in:here", here=self.home), ["home"])
        (self.home / "shop").mkdir()
        self.assertEqual(self.ids("in:here", here=self.home / "shop"), [])
        self.assertEqual(self.ids("in:here", "ledger", here=app), ["app", "work"])
        self.assertEqual(self.ids("ledger", "in:here", here=sub), ["app", "work"])
        self.assertEqual(self.json_ids("in:here", here=app), self.ids("in:here", here=app))

    def test_without_pwd_in_here_uses_the_folder_superfind_runs_in(self):
        project = self.home / "shop"
        (project / "web").mkdir(parents=True)
        self.worked("shop", project)
        self.worked("other", self.place("elsewhere/other"))
        self.assertEqual(self.ids("in:here", here=project / "web", pwd=None), ["shop"])
        self.assertEqual(self.ids("in:here", here=project / "web", pwd="shop/web"), ["shop"])

    def test_a_pwd_left_over_from_another_folder_does_not_say_where_here_is(self):
        self.worked("app", self.place("work/app"))
        self.worked("shop", self.place("work/shop"))
        here, stale = self.place("work/app"), self.place("work/shop")
        for pwd in (str(stale), str(self.root / "gone")):
            self.assertEqual(self.ids("in:here", here=here, pwd=pwd), ["app"], pwd)
            result = self.run_in("last", "--json", here=here, pwd=pwd)
            self.assertEqual([json.loads(row)["id"] for row in result.stdout.splitlines()], ["app"], pwd)

    def test_a_pwd_typed_in_another_letter_case_still_finds_this_folder(self):
        here = self.place("work/app")
        self.worked("app", here.resolve())
        self.assertEqual(self.ids("in:here", here=here, pwd=str(here).upper()), ["app"])
        self.assertEqual(self.ids("in:here", here=here, pwd=str(here.resolve()).upper()), ["app"])

    def test_in_here_sees_through_a_symlinked_folder(self):
        real = self.home / "src" / "app"
        (real / "web").mkdir(parents=True)
        (self.home / "link").symlink_to(self.home / "src")
        self.worked("real", real.resolve())
        self.worked("linked", self.home / "link" / "app")
        self.worked("web", self.home / "link" / "app" / "web")
        self.assertEqual(self.ids("in:here", here=self.home / "link" / "app"), ["linked", "real"])
        self.assertEqual(self.ids("in:here", here=real), ["linked", "real"])
        self.assertEqual(self.ids("in:here", here=real / "web"), ["linked", "real", "web"])

    def test_branch_keeps_sessions_that_ended_on_any_named_branch(self):
        self.worked("main", self.app, branch="main")
        self.worked("auth", self.app, branch="feat/auth")
        self.worked("none", self.app, branch="")
        self.assertEqual(self.ids("branch:MAIN"), ["main"])
        self.assertEqual(self.ids("branch:main", "branch:feat/auth"), ["auth", "main"])
        self.assertEqual(self.ids("branch:mai"), [])
        self.assertEqual(self.ids("branch:feat/auth", "ledger"), ["auth"])

    def test_after_and_before_count_days_in_your_time_zone(self):
        late = calendar.timegm((2026, 10, 9, 22, 30, 0))
        self.worked("late", self.app, at=late)
        self.worked("early", self.app, at=late - 12 * 3600)
        for tz in ("UTC", "Europe/Berlin"):
            self.assertEqual(self.ids("after:2026-10-09", tz=tz), ["early", "late"], tz)
        self.assertEqual(self.ids("after:2026-10-09", "before:2026-10-10", tz="UTC"), ["early", "late"])
        self.assertEqual(self.ids("after:2026-10-09", "before:2026-10-10", tz="Europe/Berlin"), ["early"])
        self.assertEqual(self.ids("after:2026-10-10", tz="UTC"), [])
        self.assertEqual(self.ids("after:2026-10-10", tz="Europe/Berlin"), ["late"])
        self.assertEqual(self.ids("before:2026-10-10", tz="UTC"), ["early", "late"])
        self.assertEqual(self.ids("before:2026-10-10", tz="Europe/Berlin"), ["early"])
        self.assertEqual(self.ids("after:2026-10-10", "before:2026-10-09"), [])
        self.assertEqual(self.ids("after:2300-01-01"), [])
        self.assertEqual(self.ids("before:2300-01-01"), ["early", "late"])

    def test_after_and_before_take_today_yesterday_and_a_time_ago(self):
        now = time.time()
        self.worked("hour", self.app, at=now - 3600, branch="fix")
        self.worked("days", self.app, at=now - 3 * 86400 - 600, branch="fix")
        self.worked("weeks", self.place("elsewhere"), at=now - 15 * 86400, branch="fix")
        self.assertEqual(self.ids("after:2h"), ["hour"])
        self.assertEqual(self.ids("before:2h"), ["days", "weeks"])
        self.assertEqual(self.ids("after:3d"), ["hour"])
        self.assertEqual(self.ids("after:4d"), ["days", "hour"])
        self.assertEqual(self.ids("before:2w"), ["weeks"])
        self.assertEqual(self.ids("after:yesterday"), ["hour"])
        self.assertEqual(self.ids("after:today", "before:yesterday"), [])
        self.assertEqual(self.ids("in:here", "branch:FIX", "after:1w", here=self.app), ["days", "hour"])
        self.assertEqual(self.ids("in:here", "branch:main", "after:1w", here=self.app), [])

    def test_json_search_includes_sessions_another_program_started_here(self):
        self.worked("typed", self.app)
        self.by_agent("sdk", self.app)
        self.by_agent("away", self.place("elsewhere"))
        self.assertEqual(self.ids("in:here", here=self.app), ["typed"])
        self.assertEqual(self.json_ids("in:here", here=self.app), ["sdk", "typed"])
        self.assertEqual(self.ids("in:here", "is:agent", here=self.app), ["sdk"])

    def edited_in(self, n, folder, path, failed=False):
        return self.session(uid(n), user("fix the ledger", cwd=str(folder)), *edited(time.time() - 600, "e1", str(path), failed=failed),
                            claude("Done."))

    def test_file_keeps_sessions_that_edited_a_file_or_a_folder_with_any_agent(self):
        subprocess.run(["git", "init", "-q", str(self.app)], check=True)
        ledger = self.app / "src" / "ledger.go"
        self.edited_in(1, self.app, ledger)
        self.edited_in(2, self.app, ledger, failed=True)
        self.session(uid(3), user("read the ledger", cwd=str(self.app)), claude_tool(time.time() - 600, "r1", "Read", file_path=str(ledger)),
                     claude_result(time.time() - 599, "r1", "package ledger"), claude("Read it."))
        self.codex_session(uid(4), codex_meta(cwd=str(self.app)), codex_user("fix the ledger"),
                           codex_at(time.time() - 600, "response_item", type="custom_tool_call", name="apply_patch", call_id="p1",
                                    input="*** Begin Patch\n*** Update File: src/ledger.go\n@@\n-a\n+b\n*** End Patch"),
                           codex_at(time.time() - 599, "response_item", type="custom_tool_call_output", call_id="p1", output="Success."),
                           codex_reply("Done."))
        self.grok_session(uid(5), str(self.app), GROK_SYSTEM, grok_user("fix the ledger"),
                          grok_call("search_replace", "t1", file_path=str(ledger), old_string="a", new_string="b"), title="Ledger")
        self.edited_in(6, self.app, self.app / "srcx" / "other.go")
        self.edited_in(7, self.app, self.app / ".claude" / "worktrees" / "gone" / "src" / "ledger.go")
        self.edited_in(8, self.place("elsewhere/repo"), self.root / "elsewhere" / "repo" / "src" / "ledger.go")
        edited_ledger = sorted(uid(n) for n in (1, 4, 5, 7))
        self.assertEqual(self.json_ids("file:src/ledger.go", here=self.app), edited_ledger)
        self.assertEqual(self.json_ids(f"file:{ledger}"), edited_ledger)
        self.assertEqual(self.json_ids("file:src", here=self.app), edited_ledger)
        self.assertEqual(self.json_ids("file:src/", here=self.app), edited_ledger)
        self.assertEqual(self.json_ids("file:srcx", here=self.app), [uid(6)])
        self.assertEqual(self.json_ids("file:src/ledger.go", here=self.place("elsewhere")), [])
        self.assertEqual(self.json_ids("file:src/ledger.go", "file:srcx/other.go", here=self.app), sorted(edited_ledger + [uid(6)]))
        self.assertEqual(self.json_ids("file:src/ledger.go", "agent:codex", here=self.app), [uid(4)])

    def test_has_commit_keeps_sessions_that_ran_git_commit(self):
        now = time.time()
        self.session(uid(1), user("commit the ledger", cwd=str(self.app)),
                     claude_tool(now - 600, "b1", "Bash", command='git commit -qam "fix: the ledger"'),
                     claude_result(now - 599, "b1", "[main 1a2b3c4] fix: the ledger"), claude("Committed."))
        self.session(uid(2), user("commit the ledger", cwd=str(self.app)),
                     claude_tool(now - 600, "b1", "Bash", command='git commit -qam "fix: the ledger"'),
                     claude_result(now - 599, "b1", "nothing to commit", failed=True), claude("Nothing to commit."))
        self.worked(uid(3), self.app)
        self.assertEqual(self.json_ids("has:commit"), [uid(1)])
        self.assertEqual(self.json_ids("HAS:Commit", "ledger"), [uid(1)])

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
        self.worked("app", self.app)
        self.superfind("index")
        result = self.run_in("ask", "where", "we", "stopped", "after:2026-1", here=self.app)
        self.assertEqual((result.returncode, result.stdout), (2, ""))
        self.assertFalse(log.exists())
        self.run_in("ask", "where", "we", "stopped", "after:2000-01-01", here=self.app)
        self.assertTrue(log.exists())

    def test_is_and_agent_values_it_does_not_know_are_still_ignored(self):
        self.worked("app", self.app)
        self.assertEqual(self.ids("is:bogus", "agent:bogus", "ledger"), ["app"])
        self.assertEqual(self.ids("foo:bar"), [])

    def last(self, *args, here=None):
        result = self.run_in("last", *args, here=here or self.app)
        return result.returncode, ANSI.sub("", result.stdout), result.stderr

    def handoff(self, path, title):
        return f"{title}\n{self.superfind('command', str(path))}"

    def test_last_prints_the_newest_real_session_here_and_the_command_that_resumes_it(self):
        self.worked("app", self.app)
        sub = self.session("sub", user("wrap the invoice names", cwd=str(self.app / "sub")), tool("Edit"), claude("Wrapped."),
                           title="Invoice names")
        self.session("hi", user("hi", cwd=str(self.app)), claude("Hello."))
        self.worked("other", self.place("elsewhere/other"))
        self.by_agent("sdk", self.app)
        app = self.projects / "app.jsonl"
        self.assertEqual(self.last(), (0, self.handoff(app.resolve(), "fix the ledger"), ""))
        [record] = [json.loads(row) for row in self.last("--json")[1].splitlines()]
        self.assertEqual((record["id"], record["kind"], record["match"], record["resume"]),
                         ("app", "interactive", "", self.superfind("command", str(app)).strip()))
        self.assertEqual([json.loads(row)["id"] for row in self.last("--json", "is:agent")[1].splitlines()], ["sdk"])
        self.assertEqual(self.last("branch:nope")[:2], (1, ""))
        hi = self.projects / "hi.jsonl"
        self.assertEqual(self.last(here=self.place("work/app/sub/deeper"))[1], self.handoff(sub, "Invoice names"))
        app.unlink()
        self.assertEqual(self.last()[1], self.handoff(hi.resolve(), "hi"))

    def test_last_never_resumes_a_second_copy_of_a_session_an_agent_has_open(self):
        self.worked("app", self.app)
        sessions = self.home / ".claude" / "sessions"
        sessions.mkdir()
        pid_file = sessions / f"{os.getpid()}.json"
        pid_file.write_text(json.dumps({"pid": os.getpid(), "sessionId": "app", "cwd": str(self.app),
                                        "startedAt": int(time.time() * 1000), "status": "idle"}))
        self.assertEqual(self.last(), (1, "", "superfind last: fix the ledger is already open in another window. "
                                              "superfind last doesn't start a second copy.\n"))
        [record] = [json.loads(row) for row in self.last("--json")[1].splitlines()]
        self.assertEqual((record["id"], record["open"]["pid"]), ("app", os.getpid()))
        pid_file.unlink()
        self.assertEqual(self.last(), (0, self.handoff((self.projects / "app.jsonl").resolve(), "fix the ledger"), ""))

    def test_last_takes_the_agent_and_every_other_filter(self):
        self.worked("app", self.app)
        codex = self.codex_session(uid(1), codex_meta(cwd=str(self.app)), codex_user("migrate the ledger index"), codex_call(),
                                   codex_output(), codex_reply("Migrated."))
        self.worked("newest", self.app, branch="fix")
        code, out, _ = self.last("agent:codex")
        self.assertEqual((code, out), (0, self.handoff(codex, "migrate the ledger index")))
        self.assertIn(f"codex resume {uid(1)}", out)
        self.assertEqual(self.last("branch:main", "agent:claude")[1].splitlines()[1], self.superfind("command", "app").strip())

    def test_a_session_still_running_says_it_may_be_open(self):
        path = self.worked("app", self.app, at=time.time())
        self.assertEqual(self.last()[1].splitlines()[0], "fix the ledger · may still be open in another window")
        self.assertIn(f"--resume {path.stem}", self.last()[1])

    def test_last_with_nothing_here_or_words_or_an_unknown_option_says_so(self):
        self.worked("other", self.place("elsewhere/other"))
        self.assertEqual(self.last(), (1, "", "superfind last: no session started in this folder or a folder that holds it.\n"))
        self.assertEqual(self.last("agent:codex"),
                         (1, "", "superfind last: no session that fits these filters started in this folder or a folder that holds it.\n"))
        for args in (("ledger",), ("agent:codex", "ledger"), ("--", "--json")):
            self.assertEqual(self.last(*args), (2, "", "superfind last: takes filters, such as agent:codex or branch:main, not words. "
                                                       "To search this folder, use superfind search in:here WORDS.\n"), args)
        self.assertEqual(self.last("--frobnicate"), (2, "", "superfind last: there is no --frobnicate option. See superfind help last.\n"))

    def test_last_refuses_an_agent_or_is_value_it_does_not_know_instead_of_dropping_it(self):
        self.worked("app", self.app)
        cases = {("agent:codx",): "can't read agent:codx. agent: takes claude, codex, or grok.",
                 ("is:bogus", "--json"): "can't read is:bogus. is: takes waiting, unfinished, open, or agent.",
                 ("agent:claude", "is:finished"): "can't read is:finished. is: takes waiting, unfinished, open, or agent."}
        for args, sentence in cases.items():
            self.assertEqual(self.last(*args), (2, "", f"superfind last: {sentence}\n"), args)
        self.assertEqual(self.ids("agent:bogus", "ledger"), ["app"])

    def test_last_explains_itself(self):
        for args in (("help", "last"), ("last", "--help"), ("last", "-h")):
            result = self.run_in(*args)
            self.assertEqual((result.returncode, result.stderr), (0, ""), args)
            self.assertTrue(result.stdout.startswith("superfind last [FILTERS] [--json]\n"), args)
        self.assertIn("superfind last [FILTERS] [--json]\n"
                      "                          resume the newest session from this folder (after shell setup);\n"
                      "                          without the shell setup, or in a script, print its title and the\n"
                      "                          command that resumes it\n", self.superfind("--help"))


def load_tests(loader, tests, pattern):
    own = sorted(name for name in vars(Recall) if name.startswith("test_"))
    return unittest.TestSuite(Recall(name) for name in own)


if __name__ == "__main__":
    unittest.main()
