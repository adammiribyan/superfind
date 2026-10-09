import importlib.machinery
import importlib.util
import json
import os
import re
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

HOP = Path(__file__).resolve().parent.parent / "hop"
ANSI = re.compile(r"\x1b\[[0-9;]*m")


def load_hop():
    loader = importlib.machinery.SourceFileLoader("hop", str(HOP))
    module = importlib.util.module_from_spec(importlib.util.spec_from_loader("hop", loader))
    loader.exec_module(module)
    return module


def line(**entry):
    return json.dumps(entry, separators=(",", ":"))


def user(text, cwd="/tmp", branch="main"):
    return line(type="user", cwd=cwd, gitBranch=branch, message={"role": "user", "content": text})


def claude(text):
    return line(type="assistant", message={"role": "assistant", "content": [{"type": "text", "text": text}]})


def tool(name):
    return line(type="assistant", message={"role": "assistant", "content": [
        {"type": "tool_use", "id": "t1", "name": name, "input": {}}]})


class HopBehavior(unittest.TestCase):
    def setUp(self):
        self.home = Path(tempfile.mkdtemp())
        self.projects = self.home / ".claude" / "projects" / "-work-billing"
        self.projects.mkdir(parents=True)
        self.env = {**os.environ, "HOME": str(self.home), "XDG_CACHE_HOME": str(self.home / ".cache"),
                    "PWD": "/elsewhere", "TYPESAFE_API_KEY": ""}
        self.env.pop("CLAUDE_CONFIG_DIR", None)
        self.clock = time.time() - 2 * 86400

    def session(self, session_id, *lines, title=None):
        path = self.projects / f"{session_id}.jsonl"
        body = list(lines) + ([line(type="ai-title", aiTitle=title)] if title else [])
        path.write_text("\n".join(body) + "\n")
        self.clock += 60
        os.utime(path, (self.clock, self.clock))
        return path.resolve()

    def hop(self, *args):
        result = subprocess.run([sys.executable, str(HOP), *args], env=self.env, capture_output=True, text=True,
                                check=True)
        return ANSI.sub("", result.stdout)

    def run_hop(self, *args):
        return subprocess.run([sys.executable, str(HOP), *args], env=self.env, capture_output=True, text=True)

    def titles(self, *search):
        self.hop("index")
        return [row.split("\t")[4] for row in self.hop("search", *search).splitlines() if row]

    def test_typing_finds_a_session_by_what_was_said_in_it(self):
        self.session("a1", user("make payment retries safe to repeat"),
                     claude("Each request now carries an idempotency key, so a retried charge is applied once."),
                     title="Payment retries")
        self.session("b2", user("rename the invoice PDF files"), claude("Renamed them."), title="Invoice file names")
        self.assertEqual(self.titles("idempotency"), ["Payment retries"])

    def test_a_search_with_no_matches_returns_no_rows(self):
        self.session("a1", user("rename the invoice PDF files"), claude("Renamed them."), title="Invoice file names")
        self.hop("index")
        self.assertEqual(self.hop("search", "kubernetes"), "")

    def test_a_title_match_ranks_above_a_conversation_match(self):
        self.session("a1", user("idempotency idempotency idempotency, fix the retries"),
                     claude("Idempotency keys everywhere now, idempotency done."), title="Payment retries")
        self.session("b2", user("check what our endpoints do on repeated requests"),
                     claude("Listed the endpoints."), title="Idempotency audit")
        self.assertEqual(self.titles("idempotency"), ["Idempotency audit", "Payment retries"])

    def test_a_slash_command_names_an_untitled_session(self):
        self.session("a1", user("<command-message>review</command-message>\n<command-name>/review</command-name>\n"
                                "<command-args>the auth module</command-args>"), claude("Reviewed the auth module."))
        self.assertEqual(self.titles(""), ["/review the auth module"])

    def test_throwaway_sessions_sort_after_real_work_even_when_newer(self):
        self.session("a1", user("migrate the ledger tables to the new schema"),
                     claude(" ".join(["The migration moved every ledger row and kept the history."] * 20)),
                     title="Ledger migration")
        self.session("b2", user("hi"), claude("Hello."), title="Say hi")
        self.assertEqual(self.titles(""), ["Ledger migration", "Say hi"])

    def test_a_short_session_that_ran_tools_is_not_treated_as_throwaway(self):
        self.session("a1", user("bump the button padding"), tool("Edit"), claude("Done."), title="Button padding")
        self.session("b2", user("hi"), claude("Hello."), title="Say hi")
        self.assertEqual(self.titles(""), ["Button padding", "Say hi"])

    def judged(self, path, status, p=0.97):
        st = os.stat(path)
        cache = self.home / ".cache" / "hop"
        cache.mkdir(parents=True, exist_ok=True)
        (cache / "status.json").write_text(json.dumps(
            {str(path): {"mtime": st.st_mtime, "size": st.st_size, "status": status, "p": p}}))

    def rows(self, *search):
        self.hop("index")
        return [row.split("\t")[5].strip() for row in self.hop("search", *search).splitlines() if row]

    def test_a_session_waiting_on_you_says_so_and_can_be_filtered(self):
        waiting = self.session("a1", user("deploy the ledger migration"), tool("Bash"),
                               claude("Staging is ready. Should I run it on production now?"), title="Ledger migration")
        self.session("b2", user("rename the export job"), tool("Edit"), claude("Renamed."), title="Export job name")
        self.judged(waiting, "waiting")
        self.assertEqual(self.rows("is:waiting"), ["Ledger migration  waiting on you · 1d ago · tmp"])

    def test_a_judgment_is_dropped_once_the_session_changes(self):
        waiting = self.session("a1", user("deploy the ledger migration"), tool("Bash"),
                               claude("Should I run it on production now?"), title="Ledger migration")
        self.judged(waiting, "waiting")
        with open(waiting, "a") as transcript:
            transcript.write(user("yes, go ahead") + "\n")
        self.assertEqual(self.rows("is:waiting"), [])

    def test_an_unconfident_judgment_is_not_shown(self):
        waiting = self.session("a1", user("deploy the ledger migration"), tool("Bash"),
                               claude("Should I run it on production now?"), title="Ledger migration")
        self.judged(waiting, "waiting", p=0.6)
        self.assertEqual(self.rows(""), ["Ledger migration  1d ago · tmp"])

    def test_a_running_session_shows_active_now_instead_of_a_judgment(self):
        running = self.session("a1", user("deploy the ledger migration"), tool("Bash"),
                               claude("Should I run it on production now?"), title="Ledger migration")
        now = time.time()
        os.utime(running, (now, now))
        self.judged(running, "waiting")
        self.assertEqual(self.rows(""), ["Ledger migration  active now · tmp"])

    def test_bold_text_that_wraps_never_shows_its_markers(self):
        path = self.session("a1", user("summarize the incident for the team"), tool("Read"),
                            claude("**The outage came from a certificate that expired on the payments gateway "
                                   "during the overnight batch window** and was renewed by hand."),
                            title="Incident summary")
        self.hop("index")
        self.env["FZF_PREVIEW_COLUMNS"] = "40"
        self.assertNotIn("**", self.hop("preview", str(path)))

    def test_a_label_expires_once_you_have_left_a_session_for_two_weeks(self):
        waiting = self.session("a1", user("deploy the ledger migration"), tool("Bash"),
                               claude("Should I run it on production now?"), title="Ledger migration")
        old = time.time() - 15 * 86400
        os.utime(waiting, (old, old))
        self.judged(waiting, "waiting")
        self.assertEqual(self.rows("is:waiting"), [])

    def test_ask_says_why_it_failed_when_no_ranker_can_answer(self):
        self.session("a1", user("deploy the ledger migration"), tool("Bash"), claude("Done."), title="Ledger migration")
        self.hop("index")
        self.env["PATH"] = "/usr/bin:/bin"
        result = self.run_hop("ask", "where did we migrate the ledger")
        self.assertEqual((result.returncode, result.stdout, result.stderr.strip()),
                         (1, "", "Couldn't ask: Claude Code didn't answer."))

    def test_messages_from_other_agents_are_not_counted_as_your_prompts(self):
        path = self.session("a1", user("migrate the ledger tables to the new schema"),
                            user("Another Claude session sent a message:\n<agent-message>report</agent-message>"),
                            claude("Migrated."), title="Ledger migration")
        self.hop("index")
        header = self.hop("preview", str(path)).splitlines()
        self.assertEqual(header[1].split(" · ")[-1], "1 prompt")

    def test_the_preview_opens_on_your_last_real_request_not_an_acknowledgement(self):
        path = self.session("a1", user("refactor the billing module so retries share one code path"),
                            claude("Refactored; retries now go through a single path."),
                            user("done"), claude("Great, it is merged."), title="Billing refactor")
        self.hop("index")
        preview = self.hop("preview", str(path))
        self.assertEqual(preview.splitlines()[0], "Billing refactor")
        self.assertIn("refactor the billing module so retries share one code path", preview)
        self.assertNotIn("\n  done\n", preview)

    def test_resuming_a_deleted_worktree_runs_from_its_nearest_surviving_parent(self):
        parent = self.home / "repo"
        parent.mkdir()
        hop = load_hop()
        config = hop.HOME / ".claude"
        self.assertEqual(hop.resume_command(str(parent / ".claude" / "worktrees" / "gone"), "abc-123", config),
                         f"cd {parent} && claude --resume abc-123")


if __name__ == "__main__":
    unittest.main()
