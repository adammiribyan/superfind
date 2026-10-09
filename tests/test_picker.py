import os
import shutil
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

from test_hop import HOP, claude, line, tool, user


def tmux(*args):
    return subprocess.run(["tmux", *args], capture_output=True, text=True)


@unittest.skipUnless(shutil.which("tmux"), "the picker tests drive a real terminal through tmux")
class PickerBehavior(unittest.TestCase):
    def setUp(self):
        self.home = Path(tempfile.mkdtemp()).resolve()
        self.projects = self.home / ".claude" / "projects" / "-tmp"
        self.projects.mkdir(parents=True)
        self.out = self.home / "out"
        self.session_name = f"hop-test-{os.getpid()}-{id(self)}"
        self.clock = time.time() - 3 * 86400
        self.env = {"HOME": str(self.home), "XDG_CACHE_HOME": str(self.home / ".cache"), "PWD": "/elsewhere",
                    "TYPESAFE_API_KEY": "", "HOP_PLAIN_COLORS": "1", "PATH": "/usr/bin:/bin", "TERM": "xterm-256color"}

    def tearDown(self):
        tmux("kill-session", "-t", self.session_name)
        shutil.rmtree(self.home, ignore_errors=True)

    def session(self, session_id, title, *lines):
        path = self.projects / f"{session_id}.jsonl"
        path.write_text("\n".join(list(lines) + [line(type="ai-title", aiTitle=title)]) + "\n")
        self.clock += 3600
        os.utime(path, (self.clock, self.clock))

    def open_picker(self, width=140, height=32):
        subprocess.run([str(HOP), "index"], env=self.env, capture_output=True, check=True)
        env = " ".join(f"{k}='{v}'" for k, v in self.env.items())
        command = f"env -i {env} {HOP} pick > {self.out}.stdout 2> {self.out}.stderr; echo $? > {self.out}.code"
        tmux("new-session", "-d", "-s", self.session_name, "-x", str(width), "-y", str(height), command)
        self.wait_for("Claude sessions")

    def screen(self):
        return tmux("capture-pane", "-p", "-t", self.session_name).stdout

    def wait_for(self, text, timeout=5):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if text in self.screen():
                return
            time.sleep(0.1)
        self.fail(f"never saw {text!r} on screen:\n{self.screen()}")

    def keys(self, *keys):
        tmux("send-keys", "-t", self.session_name, *keys)
        time.sleep(0.4)

    def result(self, timeout=5):
        deadline = time.time() + timeout
        while time.time() < deadline and not Path(f"{self.out}.code").exists():
            time.sleep(0.1)
        code = int(Path(f"{self.out}.code").read_text())
        return code, Path(f"{self.out}.stdout").read_text()

    def three_sessions(self):
        self.session("old", "Ledger migration", user("migrate the ledger tables to the new schema"), tool("Bash"),
                     claude("Migrated every ledger row and kept the history."))
        self.session("mid", "Invoice layout", user("the invoice PDF cuts off long company names"), tool("Edit"),
                     claude("Long names now wrap onto two lines."))
        self.session("new", "Checkout double charge", user("customers get charged twice on slow networks"),
                     tool("Edit"), claude("The Pay button now disables itself until the charge finishes."),
                     user("add a test that taps twice"), tool("Edit"), claude("Added a test that taps Pay twice."))

    def rows(self):
        return [l.strip() for l in self.screen().splitlines() if " ago · tmp" in l or " · tmp" in l]

    def test_sessions_are_listed_newest_first(self):
        self.three_sessions()
        self.open_picker()
        titles = [r.split("  ")[0] for r in self.rows()]
        self.assertEqual(titles[:3], ["Checkout double charge", "Invoice layout", "Ledger migration"])

    def test_typing_narrows_the_list_and_shows_the_matching_passage(self):
        self.three_sessions()
        self.open_picker()
        self.keys("charged")
        self.wait_for("1/3")
        self.assertEqual([r.split("  ")[0] for r in self.rows()], ["Checkout double charge"])
        self.assertIn("customers get charged twice", self.screen())

    def test_a_search_with_no_matches_says_so_and_offers_a_way_back(self):
        self.three_sessions()
        self.open_picker()
        self.keys("kubernetes")
        self.wait_for("No sessions match “kubernetes”")
        self.assertIn("ctrl-u clear search", self.screen())
        self.keys("C-u")
        self.wait_for("3/3")

    def test_enter_prints_the_title_and_the_command_that_resumes_it(self):
        self.three_sessions()
        self.open_picker()
        self.keys("Down", "Enter")
        self.assertEqual(self.result(), (0, "Invoice layout\ncd /tmp && claude --resume mid\n"))

    def test_escape_cancels_without_printing_anything(self):
        self.three_sessions()
        self.open_picker()
        self.keys("Escape")
        self.assertEqual(self.result(), (1, ""))

    def test_tab_switches_the_preview_to_the_recent_conversation(self):
        self.three_sessions()
        self.open_picker()
        self.assertNotIn("customers get charged twice", self.screen())
        self.keys("Tab")
        self.wait_for("customers get charged twice")

    def test_ctrl_slash_hides_the_preview(self):
        self.three_sessions()
        self.open_picker()
        self.wait_for("Added a test that taps Pay twice")
        self.keys("C-_")
        time.sleep(0.3)
        self.assertNotIn("Added a test that taps Pay twice", self.screen())

    def test_a_narrow_window_stacks_the_preview_under_the_list(self):
        self.three_sessions()
        self.open_picker(width=70)
        self.wait_for("Added a test that taps Pay twice")
        lines = self.screen().splitlines()
        list_row = next(i for i, l in enumerate(lines) if "Ledger migration" in l)
        reply_row = next(i for i, l in enumerate(lines) if "Added a test that taps Pay twice" in l)
        self.assertLess(list_row, reply_row)
        self.assertNotIn("│", lines[list_row])

    def test_ask_says_why_it_failed_when_no_ranker_can_answer(self):
        self.three_sessions()
        self.open_picker()
        self.keys("where did we stop double charges", "M-Enter")
        self.wait_for("Couldn't ask: Claude Code didn't answer.", timeout=10)


if __name__ == "__main__":
    unittest.main()
