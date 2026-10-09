import http.server
import json
import os
import fcntl
import pty
import select
import shutil
import struct
import subprocess
import tempfile
import termios
import threading
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

    def settled_screen(self):
        previous, current = None, self.screen()
        while current != previous:
            time.sleep(0.3)
            previous, current = current, self.screen()
        return current

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

    def test_a_tiny_window_still_draws_and_picks(self):
        self.three_sessions()
        self.open_picker(width=60, height=8)
        self.assertIn("Checkout double charge", self.screen())
        self.keys("Down", "Enter")
        self.assertEqual(self.result(), (0, "Invoice layout\ncd /tmp && claude --resume mid\n"))

    def test_pasting_a_search_filters_the_list(self):
        self.three_sessions()
        self.open_picker()
        tmux("set-buffer", "-b", self.session_name, "charged")
        tmux("paste-buffer", "-p", "-d", "-b", self.session_name, "-t", self.session_name)
        self.wait_for("1/3")
        self.assertEqual([r.split("  ")[0] for r in self.rows()], ["Checkout double charge"])

    def test_a_scrolled_preview_keeps_its_place_when_labels_refresh(self):
        turns = []
        for n in range(1, 41):
            turns += [user(f"step {n} of the rollout"), tool("Bash"), claude(f"Finished rollout step {n}.")]
        self.session("long", "Rollout", *turns)
        self.open_picker(height=24)
        self.keys("Tab")
        self.wait_for("step 39 of the rollout")
        self.keys(*["M-Up"] * 12)
        scrolled = self.settled_screen()
        self.assertNotIn("Finished rollout step 40.", scrolled)
        cache = self.home / ".cache" / "hop"
        (cache / "status.json").write_text("{}")
        os.utime(cache / "status.json", (time.time() + 5, time.time() + 5))
        time.sleep(2.5)
        self.assertEqual(self.screen(), scrolled)

    def test_a_preview_showing_the_latest_turn_still_shows_it_after_the_window_shrinks(self):
        turns = []
        for n in range(1, 41):
            turns += [user(f"step {n} of the rollout"), tool("Bash"), claude(f"Finished rollout step {n}.")]
        self.session("long", "Rollout", *turns)
        self.open_picker(height=24)
        self.keys("Tab")
        self.wait_for("step 39 of the rollout")
        tmux("resize-window", "-t", self.session_name, "-y", "16")
        time.sleep(0.6)
        self.assertIn("Finished rollout step 40.", self.settled_screen())

    def test_a_broken_search_index_says_so_instead_of_finding_nothing(self):
        import sqlite3
        self.three_sessions()
        subprocess.run([str(HOP), "index"], env=self.env, capture_output=True, check=True)
        db = sqlite3.connect(self.home / ".cache" / "hop" / "search.db")
        db.executescript("DROP TABLE docs; CREATE TABLE docs (path, title, body);")
        db.close()
        self.open_picker()
        self.keys("charged")
        self.wait_for("Search failed: the search index couldn't be read")
        self.assertNotIn("No sessions match", self.screen())

    def test_a_preview_that_could_not_be_read_is_read_again_once_it_can_be(self):
        self.three_sessions()
        subprocess.run([str(HOP), "index"], env=self.env, capture_output=True, check=True)
        newest = self.projects / "new.jsonl"
        newest.chmod(0)
        self.open_picker()
        self.wait_for("Couldn't read this session")
        time.sleep(1)
        newest.chmod(0o644)
        self.keys("Down")
        self.keys("Up")
        self.wait_for("Added a test that taps Pay twice")

    def test_ctrl_r_searches_deeper_and_says_why_it_failed_when_no_ranker_can_answer(self):
        self.three_sessions()
        self.open_picker()
        self.keys("where did we stop double charges", "C-r")
        self.wait_for("Deep search failed: Claude Code didn't answer.", timeout=10)
        self.assertFalse(Path(f"{self.out}.code").exists())

    def test_alt_enter_also_searches_deeper_in_terminals_that_send_it(self):
        self.three_sessions()
        self.open_picker()
        self.keys("where did we stop double charges", "M-Enter")
        self.wait_for("Deep search failed: Claude Code didn't answer.", timeout=10)


    def test_explanation_passages_are_packed_by_characters_and_never_cut_short(self):
        received = []

        class TypeSafe(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                questions = json.loads(self.rfile.read(int(self.headers["Content-Length"])))["questions"]
                answers = {}
                for name, question in questions.items():
                    options = [k for k in question.get("criteria", {}) if k != "none"]
                    if question["type"] == "noul":
                        answers[name] = {"noul": 0.9}
                    elif options and options[0].startswith("p"):
                        received.append(question["criteria"])
                        answers[name] = {"choice": "none"}
                    elif options and options[0].startswith("s"):
                        answers[name] = {"probabilities": {k: 0.5 for k in options}}
                    else:
                        answers[name] = {"choice": "unclear", "probabilities": {"unclear": 1.0}}
                body = json.dumps({"answers": answers}).encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), TypeSafe)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        paragraphs = [("Миграция реестра " * 9)[:150]] * 2 + ["р" * 199 + "a", "р" * 199 + "b"]
        self.session("ledger", "Ledger migration", user("migrate the ledger"), tool("Bash"),
                     claude("\n\n".join(paragraphs)))
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=f"http://127.0.0.1:{server.server_port}")
        self.open_picker()
        self.keys("where did we migrate the ledger", "C-r")
        deadline = time.time() + 10
        while not received and time.time() < deadline:
            time.sleep(0.1)
        passages = [received[0][k] for k in received[0] if k != "none"]
        self.assertEqual(passages, ["migrate the ledger " + paragraphs[0] + " " + paragraphs[1], paragraphs[2], paragraphs[3]])
        self.assertEqual(list(received[0]), [f"p{i}" for i in range(len(received[0]) - 1)] + ["none"])


class PickerColors(unittest.TestCase):
    def stop(self, pid, terminal):
        os.close(terminal)
        if os.waitpid(pid, os.WNOHANG) == (0, 0):
            os.kill(pid, 9)
            os.waitpid(pid, 0)

    def test_colors_that_arrive_after_the_first_paint_still_theme_the_picker(self):
        home = Path(tempfile.mkdtemp()).resolve()
        self.addCleanup(shutil.rmtree, home, ignore_errors=True)
        projects = home / ".claude" / "projects" / "-tmp"
        projects.mkdir(parents=True)
        (projects / "a1.jsonl").write_text(user("migrate the ledger") + "\n" + tool("Bash") + "\n" +
                                          claude("Migrated.") + "\n" + line(type="ai-title", aiTitle="Ledger") + "\n")
        env = {"HOME": str(home), "XDG_CACHE_HOME": str(home / ".cache"), "PATH": "/usr/bin:/bin",
               "TERM": "xterm-256color", "COLORTERM": "truecolor", "TYPESAFE_API_KEY": ""}
        subprocess.run([str(HOP), "index"], env=env, capture_output=True, check=True)
        pid, terminal = pty.fork()
        if pid == 0:
            fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
            os.execve(str(HOP), [str(HOP), "pick"], env)
        self.addCleanup(self.stop, pid, terminal)

        def read_until(expected, seconds=10):
            seen, deadline = b"", time.time() + seconds
            while expected not in seen and time.time() < deadline:
                if select.select([terminal], [], [], 0.05)[0]:
                    try:
                        seen += os.read(terminal, 65536)
                    except OSError:
                        break
            return seen

        before = read_until(b"\x1b[7m")
        self.assertIn(b"\x1b]11;?", before)
        time.sleep(0.6)
        os.write(terminal, b"\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\\x1b]10;rgb:dddd/dddd/dddd\x1b\\")
        after = read_until(b"48;2;41;52;64")
        os.write(terminal, b"\x03")
        self.assertIn(b"48;2;41;52;64", after)


if __name__ == "__main__":
    unittest.main()
