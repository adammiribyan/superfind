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


def user(text, cwd="/work/billing", branch="main"):
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
        self.clock = time.time() - 86400

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
