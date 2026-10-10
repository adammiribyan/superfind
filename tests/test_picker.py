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

from test_superfind import (SUPERFIND, claude, codex_call, codex_meta, codex_output, codex_reply, codex_user, colors_in,
                            grok_reply, grok_user, line, tool, uid, user)


def tmux(*args):
    return subprocess.run(["tmux", *args], capture_output=True, text=True)


@unittest.skipUnless(shutil.which("tmux"), "the picker tests drive a real terminal through tmux")
class PickerBehavior(unittest.TestCase):
    def setUp(self):
        self.home = Path(tempfile.mkdtemp()).resolve()
        self.projects = self.home / ".claude" / "projects" / "-tmp"
        self.projects.mkdir(parents=True)
        self.out = self.home / "out"
        self.session_name = f"superfind-test-{os.getpid()}-{id(self)}"
        self.clock = time.time() - 3 * 86400
        self.env = {"HOME": str(self.home), "XDG_CACHE_HOME": str(self.home / ".cache"), "PWD": "/elsewhere",
                    "TYPESAFE_API_KEY": "", "NO_COLOR": "1", "PATH": "/usr/bin:/bin", "TERM": "xterm-256color"}

    def tearDown(self):
        tmux("kill-session", "-t", self.session_name)
        shutil.rmtree(self.home, ignore_errors=True)

    def session(self, session_id, title, *lines):
        path = self.projects / f"{session_id}.jsonl"
        path.write_text("\n".join(list(lines) + [line(type="ai-title", aiTitle=title)]) + "\n")
        self.clock += 3600
        os.utime(path, (self.clock, self.clock))

    def codex_session(self, session_id, *lines):
        folder = self.home / ".codex" / "sessions" / "2026" / "10" / "09"
        folder.mkdir(parents=True, exist_ok=True)
        path = folder / f"rollout-2026-10-09T10-00-00-{session_id}.jsonl"
        path.write_text("\n".join(lines) + "\n")
        self.clock += 3600
        os.utime(path, (self.clock, self.clock))

    def open_picker(self, width=140, height=32, index_first=True, header="Claude sessions", linger=False):
        if index_first:
            subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        env = " ".join(f"{k}='{v}'" for k, v in self.env.items())
        command = f"env -i {env} {SUPERFIND} pick > {self.out}.stdout 2> {self.out}.stderr; echo $? > {self.out}.code"
        if linger:
            command += "; sleep 60"
        tmux("new-session", "-d", "-s", self.session_name, "-x", str(width), "-y", str(height), command)
        self.wait_for(header)

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

    def test_a_damaged_search_index_heals_while_the_picker_is_open(self):
        self.three_sessions()
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        db = self.home / ".cache" / "superfind" / "search.db"
        db.write_bytes(b"this is not a database " * 200)
        for leftover in db.parent.glob("search.db-*"):
            leftover.unlink()
        self.open_picker(index_first=False)
        self.keys("charged")
        self.wait_for("1/3")
        self.assertEqual([r.split("  ")[0] for r in self.rows()], ["Checkout double charge"])

    def test_the_picker_searches_the_rebuilt_index_not_the_damaged_one_it_opened(self):
        import sqlite3
        self.three_sessions()
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        db = sqlite3.connect(self.home / ".cache" / "superfind" / "search.db")
        db.execute("DROP TABLE docs_content")
        db.commit()
        db.close()
        self.open_picker(index_first=False)
        self.keys("charged")
        self.wait_for("1/3")
        self.assertEqual([r.split("  ")[0] for r in self.rows()], ["Checkout double charge"])

    def test_damage_found_while_typing_rebuilds_the_index_and_finds_the_session(self):
        import sqlite3
        self.three_sessions()
        for n in range(30):
            self.session(f"pad{n}", f"Padding {n}", user(f"pad session {n} " + "filler " * 300), tool("Bash"), claude("Done."))
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        db = sqlite3.connect(self.home / ".cache" / "superfind" / "search.db")
        db.execute("UPDATE docs_data SET block = x'00' WHERE id > 10")
        db.commit()
        db.close()
        self.open_picker(index_first=False)
        self.keys("charged")
        self.wait_for("1/33")
        self.assertEqual([r.split("  ")[0] for r in self.rows()], ["Checkout double charge"])

    @unittest.skipIf(os.geteuid() == 0, "root reads folders whatever their mode")
    def test_a_folder_that_cannot_be_read_is_named_in_the_picker(self):
        self.three_sessions()
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        self.projects.parent.chmod(0)
        self.addCleanup(self.projects.parent.chmod, 0o755)
        self.open_picker(index_first=False)
        self.wait_for("1 session file or folder couldn't be read")
        self.assertEqual(len(self.rows()), 3)

    def test_labels_that_land_while_a_status_filter_is_typed_show_up(self):
        self.three_sessions()
        self.open_picker()
        self.keys("is:waiting")
        self.wait_for("No sessions match")
        path = self.projects / "mid.jsonl"
        st = os.stat(path)
        status = self.home / ".cache" / "superfind" / "status.json"
        status.write_text(json.dumps({str(path): {"mtime_ns": st.st_mtime_ns, "size": st.st_size, "status": "waiting", "p": 0.97}}))
        self.wait_for("1/3")
        self.assertEqual([r.split("  ")[0] for r in self.rows()], ["Invoice layout"])

    def test_a_session_that_loses_its_label_leaves_status_filtered_ask_results(self):
        stub = self.home / "stub"
        stub.mkdir()
        (stub / "claude").write_text("#!/bin/sh\ncat > /dev/null\necho '{\"result\": \"[0]\"}'\n")
        (stub / "claude").chmod(0o755)
        self.three_sessions()
        path = self.projects / "mid.jsonl"
        st = os.stat(path)
        status = self.home / ".cache" / "superfind" / "status.json"
        status.parent.mkdir(parents=True, exist_ok=True)
        status.write_text(json.dumps({str(path): {"mtime_ns": st.st_mtime_ns, "size": st.st_size, "status": "waiting", "p": 0.97}}))
        self.env["PATH"] = f"{stub}:/usr/bin:/bin"
        self.open_picker()
        self.keys("is:waiting invoice names", "C-r")
        self.wait_for("1/3", timeout=10)
        status.write_text("{}")
        os.utime(status, (time.time() + 5, time.time() + 5))
        self.wait_for("0/3")

    def test_a_lost_search_index_is_rebuilt_even_when_the_picker_opens_first(self):
        self.three_sessions()
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        (self.home / ".cache" / "superfind" / "search.db").unlink()
        self.open_picker(index_first=False)
        self.wait_for("3/3")
        self.keys("Escape")
        self.result()
        found = subprocess.run([str(SUPERFIND), "search", "charged"], env=self.env, capture_output=True, text=True)
        self.assertEqual([r.split("\t")[4] for r in found.stdout.splitlines()], ["Checkout double charge"])

    def test_a_machine_with_no_sessions_says_where_it_looked(self):
        shutil.rmtree(self.home / ".claude")
        self.open_picker(index_first=False, header="Sessions")
        self.wait_for("No sessions yet in ~/.claude, ~/.codex, or ~/.grok.")
        self.keys("abc")
        screen = self.settled_screen()
        self.assertIn("No sessions yet in ~/.claude, ~/.codex, or ~/.grok.", screen)
        self.assertIn("esc quit", screen)
        self.assertNotIn("deep search", screen)
        self.keys("Escape")
        self.assertEqual(self.result(), (1, ""))

    def test_shrinking_the_terminal_to_one_cell_and_back_redraws_the_picker(self):
        self.three_sessions()
        self.open_picker()
        tmux("resize-window", "-t", self.session_name, "-x", "1", "-y", "1")
        time.sleep(0.8)
        self.assertEqual(tmux("display", "-p", "-t", self.session_name, "#{window_width}x#{window_height}").stdout, "1x1\n")
        tmux("resize-window", "-t", self.session_name, "-x", "140", "-y", "32")
        time.sleep(0.8)
        self.assertEqual([r.split("  ")[0] for r in self.rows()], ["Checkout double charge", "Invoice layout", "Ledger migration"])

    def test_the_title_handed_to_the_shell_carries_no_terminal_commands(self):
        self.session("esc", "make the \x1b[31mred\x1b[0m title \x1b]0;renamed\x07go \x1b[2Jaway",
                     user("make the title red"), tool("Edit"), claude("Done."))
        self.open_picker()
        self.keys("Enter")
        code, out = self.result()
        self.assertEqual((code, out.splitlines()[0]), (0, "make the red title go away"))

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
        cache = self.home / ".cache" / "superfind"
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
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        db = sqlite3.connect(self.home / ".cache" / "superfind" / "search.db")
        db.executescript("DROP TABLE docs; CREATE TABLE docs (path, title, body);")
        db.close()
        self.open_picker()
        self.keys("charged")
        self.wait_for("Search failed: the search index couldn't be read")
        self.assertNotIn("No sessions match", self.screen())

    def test_a_preview_that_could_not_be_read_is_read_again_once_it_can_be(self):
        self.three_sessions()
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        newest = self.projects / "new.jsonl"
        newest.chmod(0)
        self.open_picker()
        self.wait_for("Couldn't read this session")
        time.sleep(1)
        newest.chmod(0o644)
        self.keys("Down")
        self.keys("Up")
        self.wait_for("Added a test that taps Pay twice")

    def resume_through_shell(self, shell, open_keys):
        stub = self.home / "stub"
        stub.mkdir()
        (stub / "claude").write_text(f'#!/bin/sh\necho "$(pwd) $*" > {self.out}.resumed\n')
        (stub / "claude").chmod(0o755)
        self.three_sessions()
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        env = " ".join(f"{k}='{v}'" for k, v in {**self.env, "PATH": f"{stub}:/usr/bin:/bin"}.items())
        start = {"zsh": "zsh -f -i", "bash": "bash --norc --noprofile -i"}[shell]
        tmux("new-session", "-d", "-s", self.session_name, "-x", "140", "-y", "32", f"env -i {env} {start}")
        time.sleep(0.5)
        self.keys(f'eval "$({SUPERFIND} init {shell})"', "Enter")
        self.keys(*open_keys)
        self.wait_for("Claude sessions")
        self.keys("Down", "Enter")
        deadline = time.time() + 5
        while not Path(f"{self.out}.resumed").exists() and time.time() < deadline:
            time.sleep(0.1)
        self.assertEqual(Path(f"{self.out}.resumed").read_text(), "/tmp --resume mid\n")
        self.keys("pwd", "Enter")
        self.wait_for("Resuming")
        self.assertIn("\n/tmp", self.screen())

    def test_typing_superfind_in_zsh_resumes_the_chosen_session_in_that_shell(self):
        self.resume_through_shell("zsh", ["superfind", "Enter"])

    def test_typing_sfd_in_zsh_resumes_the_chosen_session_in_that_shell(self):
        self.resume_through_shell("zsh", ["sfd", "Enter"])

    def test_ctrl_x_c_in_zsh_resumes_the_chosen_session_in_that_shell(self):
        self.resume_through_shell("zsh", ["C-x", "c"])

    def test_typing_superfind_in_bash_resumes_the_chosen_session_in_that_shell(self):
        self.resume_through_shell("bash", ["superfind", "Enter"])

    def test_typing_sfd_in_bash_resumes_the_chosen_session_in_that_shell(self):
        self.resume_through_shell("bash", ["sfd", "Enter"])

    def test_ctrl_x_c_in_bash_resumes_the_chosen_session_in_that_shell(self):
        self.resume_through_shell("bash", ["C-x", "c"])

    def test_the_list_appears_while_indexing_and_the_selection_survives_the_refresh(self):
        self.three_sessions()
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        self.session("newest", "Search ranking tweaks", user("tune the ranking"), tool("Edit"), claude("Tuned."))
        lock = open(self.home / ".cache" / "superfind" / "index.lock", "w")
        fcntl.flock(lock, fcntl.LOCK_EX)
        self.open_picker(index_first=False)
        self.keys("Down")
        self.assertEqual([r.split("  ")[0] for r in self.rows()][:3],
                         ["Checkout double charge", "Invoice layout", "Ledger migration"])
        fcntl.flock(lock, fcntl.LOCK_UN)
        lock.close()
        self.wait_for("Search ranking tweaks")
        self.keys("Enter")
        self.assertEqual(self.result(), (0, "Invoice layout\ncd /tmp && claude --resume mid\n"))

    def test_the_picker_says_sessions_when_more_than_one_agent_is_present(self):
        self.three_sessions()
        self.codex_session(uid(1), codex_meta(), codex_user("fix the build"), codex_call(), codex_output(),
                           codex_reply("Fixed."))
        self.open_picker(header="─ Sessions ")
        self.assertEqual([r.split("│")[0].strip() for r in self.rows()[:2]],
                         ["fix the build  2d ago · tmp · codex", "Checkout double charge  2d ago · tmp · claude"])
        self.assertNotIn("Claude sessions", self.screen())

    def test_choosing_a_codex_session_prints_its_resume_command(self):
        self.three_sessions()
        self.codex_session(uid(1), codex_meta(), codex_user("fix the build"), codex_call(), codex_output(),
                           codex_reply("Fixed."))
        self.open_picker(header="─ Sessions ")
        self.keys("Enter")
        self.assertEqual(self.result(), (0, f"fix the build\ncd /tmp && codex resume {uid(1)}\n"))

    def test_two_agents_sharing_one_home_keep_their_own_resume_commands(self):
        shared = self.home / "shared"
        rollout = shared / "sessions" / "2026" / "10" / "09" / f"rollout-2026-10-09T10-00-00-{uid(1)}.jsonl"
        rollout.parent.mkdir(parents=True)
        rollout.write_text("\n".join([codex_meta(), codex_user("fix the build"), codex_call(), codex_output(),
                                      codex_reply("Fixed.")]) + "\n")
        chat = shared / "sessions" / "%2Ftmp" / uid(2) / "chat_history.jsonl"
        chat.parent.mkdir(parents=True)
        chat.write_text("\n".join([grok_user("rate the code"), grok_reply("It reads well.", tools=1)]) + "\n")
        (chat.parent / "summary.json").write_text(json.dumps({"info": {"cwd": "/tmp"}, "session_summary": "Code rating"}))
        for path in (rollout, chat, chat.parent / "summary.json"):
            self.clock += 3600
            os.utime(path, (self.clock, self.clock))
        self.env.update(CODEX_HOME=str(shared), GROK_HOME=str(shared))
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        lock = open(self.home / ".cache" / "superfind" / "index.lock", "w")
        self.addCleanup(lock.close)
        fcntl.flock(lock, fcntl.LOCK_EX)
        self.open_picker(index_first=False, header="─ Sessions ")
        self.wait_for("2/2")
        self.keys("Enter")
        self.assertEqual(self.result(), (0, f"Code rating\ncd /tmp && grok --resume {uid(2)}\n"))

    def test_the_first_frame_says_claude_sessions_when_another_agents_home_is_empty(self):
        self.three_sessions()
        flat = self.home / ".codex" / "sessions" / "rollout-2025-04-01-0199.json"
        flat.parent.mkdir(parents=True)
        flat.write_text("{}")
        (self.home / ".cache" / "superfind").mkdir(parents=True)
        lock = open(self.home / ".cache" / "superfind" / "index.lock", "w")
        self.addCleanup(lock.close)
        fcntl.flock(lock, fcntl.LOCK_EX)
        self.open_picker(index_first=False)
        self.assertNotIn("─ Sessions ", self.screen())

    def test_ask_in_the_picker_keeps_the_agent_filter(self):
        received = []

        class Recorder(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                sent = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                if "which" in sent["questions"]:
                    received.append(sent)
                self.send_response(500)
                self.send_header("Content-Length", "0")
                self.end_headers()

            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Recorder)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        self.three_sessions()
        self.codex_session(uid(1), codex_meta(), codex_user("migrate the ledger index"), codex_call(), codex_output(),
                           codex_reply("Migrated."))
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=f"http://127.0.0.1:{server.server_port}")
        self.open_picker(header="─ Sessions ")
        self.keys("agent:codex ledger", "C-r")
        deadline = time.time() + 10
        while not received and time.time() < deadline:
            time.sleep(0.1)
        cards = [c.split(" | ")[0] for k, c in received[0]["questions"]["which"]["criteria"].items() if k != "none"]
        self.assertEqual((received[0]["state"]["search"], cards), ("ledger", ["migrate the ledger index"]))

    def test_clearing_an_ask_stops_it_before_more_session_cards_are_sent(self):
        received = []

        class Slow(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                sent = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                received.append(sent)
                time.sleep(2)
                answers = {q: {"choice": list(spec["criteria"])[0],
                               "probabilities": {k: 0.5 for k in spec["criteria"]}, "noul": 0.5}
                           for q, spec in sent["questions"].items()}
                body = json.dumps({"answers": answers}).encode()
                try:
                    self.send_response(200)
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                except OSError:
                    pass

            def log_message(self, *args):
                pass

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Slow)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        self.three_sessions()
        status = self.home / ".cache" / "superfind" / "status.json"
        status.parent.mkdir(parents=True, exist_ok=True)
        status.write_text("{}")
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=f"http://127.0.0.1:{server.server_port}")
        self.open_picker()
        self.keys("ledger", "C-r")
        self.keys("C-u")
        time.sleep(5)
        self.assertEqual([list(r["questions"]) for r in received if "status" not in r["questions"]], [["which"]])

    def test_quitting_during_an_ask_stops_claude_and_what_it_started(self):
        stub = self.home / "stub"
        stub.mkdir()
        pids = self.home / "pids"
        (stub / "claude").write_text(f"#!/bin/sh\necho $$ >> {pids}\nsleep 30 &\necho $! >> {pids}\nwait\n")
        (stub / "claude").chmod(0o755)
        self.three_sessions()
        self.env["PATH"] = f"{stub}:/usr/bin:/bin"
        self.addCleanup(lambda: [subprocess.run(["kill", "-9", pid], capture_output=True)
                                 for pid in (pids.read_text().split() if pids.exists() else [])])
        self.open_picker(linger=True)
        self.keys("ledger", "C-r")
        deadline = time.time() + 10
        while len(pids.read_text().split() if pids.exists() else []) < 2 and time.time() < deadline:
            time.sleep(0.1)
        self.keys("Escape")
        self.assertEqual(self.result()[0], 1)
        time.sleep(0.5)
        alive = []
        for pid in map(int, pids.read_text().split()):
            try:
                os.kill(pid, 0)
                alive.append(pid)
            except ProcessLookupError:
                pass
        self.assertEqual(alive, [])

    def test_interrupting_the_picker_during_an_ask_stops_claude_too(self):
        stub = self.home / "stub"
        stub.mkdir()
        pids = self.home / "pids"
        (stub / "claude").write_text(f"#!/bin/sh\necho $PPID > {pids}.parent\necho $$ >> {pids}\nsleep 30 &\necho $! >> {pids}\nwait\n")
        (stub / "claude").chmod(0o755)
        self.three_sessions()
        self.env["PATH"] = f"{stub}:/usr/bin:/bin"
        self.addCleanup(lambda: [subprocess.run(["kill", "-9", pid], capture_output=True)
                                 for pid in (pids.read_text().split() if pids.exists() else [])])
        self.open_picker(linger=True)
        self.keys("ledger", "C-r")
        deadline = time.time() + 10
        while len(pids.read_text().split() if pids.exists() else []) < 2 and time.time() < deadline:
            time.sleep(0.1)
        os.kill(int(Path(f"{pids}.parent").read_text()), 2)
        self.result()
        time.sleep(0.5)
        alive = []
        for pid in map(int, pids.read_text().split()):
            try:
                os.kill(pid, 0)
                alive.append(pid)
            except ProcessLookupError:
                pass
        self.assertEqual(alive, [])

    def test_a_damaged_index_found_while_typing_is_removed_only_under_the_index_lock(self):
        import sqlite3
        self.three_sessions()
        for n in range(30):
            self.session(f"pad{n}", f"Padding {n}", user(f"pad session {n} " + "filler " * 300), tool("Bash"), claude("Done."))
        self.open_picker()
        self.wait_for("33/33")
        db_path = self.home / ".cache" / "superfind" / "search.db"
        db = sqlite3.connect(db_path)
        db.execute("UPDATE docs_data SET block = x'00' WHERE id > 10")
        db.commit()
        db.close()
        lock = open(self.home / ".cache" / "superfind" / "index.lock", "w")
        fcntl.flock(lock, fcntl.LOCK_EX)
        self.keys("charged")
        time.sleep(1.5)
        self.assertTrue(db_path.exists())
        lock.close()
        self.wait_for("1/33")
        self.assertEqual([r.split("  ")[0] for r in self.rows()], ["Checkout double charge"])

    def test_a_cached_session_outside_every_home_is_not_listed(self):
        self.three_sessions()
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        index = self.home / ".cache" / "superfind" / "index.json"
        cache = json.loads(index.read_text())
        entry = next(iter(cache["files"].values()))
        cache["files"]["/nowhere/stray.jsonl"] = {**entry, "session": {**entry["session"], "title": "Stray session"}}
        index.write_text(json.dumps(cache))
        lock = open(self.home / ".cache" / "superfind" / "index.lock", "w")
        self.addCleanup(lock.close)
        fcntl.flock(lock, fcntl.LOCK_EX)
        self.open_picker(index_first=False)
        self.wait_for("3/3")
        self.assertNotIn("Stray session", self.screen())

    def test_ctrl_r_asks_and_says_why_it_failed_when_no_ranker_can_answer(self):
        self.three_sessions()
        self.open_picker()
        self.keys("where did we stop double charges", "C-r")
        self.wait_for("Ask failed: Claude Code didn't answer.", timeout=10)
        self.assertFalse(Path(f"{self.out}.code").exists())

    def test_alt_enter_also_asks_in_terminals_that_send_it(self):
        self.three_sessions()
        self.open_picker()
        self.keys("where did we stop double charges", "M-Enter")
        self.wait_for("Ask failed: Claude Code didn't answer.", timeout=10)


    def explaining_typesafe(self):
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
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=f"http://127.0.0.1:{server.server_port}")
        return received

    def test_explanation_passages_cover_every_message_and_all_of_a_long_one(self):
        received = self.explaining_typesafe()
        long_reply = " ".join(f"word{n:03d}" for n in range(125))
        self.session("ledger", "Ledger migration", user("migrate the ledger"), tool("Bash"),
                     claude("I changed ledger.go to batch the writes."), user("now add a test for the ledger"),
                     claude(long_reply))
        self.open_picker()
        self.keys("where did we migrate the ledger", "C-r")
        deadline = time.time() + 10
        while not received and time.time() < deadline:
            time.sleep(0.1)
        passages = " ".join(received[0][k] for k in received[0] if k != "none")
        for said in ("I changed ledger.go", "now add a test for the ledger", "word000", "word124"):
            self.assertIn(said, passages)
        self.assertTrue(all(len(received[0][k]) <= 400 for k in received[0]))

    def test_explanation_passages_are_packed_by_characters_and_never_cut_short(self):
        received = self.explaining_typesafe()
        paragraphs = [("Миграция реестра " * 9)[:150]] * 2 + ["р" * 199 + "a", "р" * 199 + "b"]
        self.session("ledger", "Ledger migration", user("migrate the ledger"), tool("Bash"),
                     claude("\n\n".join(paragraphs)))
        self.open_picker()
        self.keys("where did we migrate the ledger", "C-r")
        deadline = time.time() + 10
        while not received and time.time() < deadline:
            time.sleep(0.1)
        passages = [received[0][k] for k in received[0] if k != "none"]
        self.assertEqual(passages, ["migrate the ledger " + paragraphs[0] + " " + paragraphs[1], paragraphs[2], paragraphs[3]])
        self.assertEqual(list(received[0]), [f"p{i}" for i in range(len(received[0]) - 1)] + ["none"])


class PtyPicker(unittest.TestCase):
    EXTRA_ENV = {}

    def setUp(self):
        home = Path(tempfile.mkdtemp()).resolve()
        self.addCleanup(shutil.rmtree, home, ignore_errors=True)
        projects = home / ".claude" / "projects" / "-tmp"
        projects.mkdir(parents=True)
        (projects / "a1.jsonl").write_text(user("migrate the ledger") + "\n" + tool("Bash") + "\n" +
                                          claude("Migrated.") + "\n" + line(type="ai-title", aiTitle="Ledger") + "\n")
        env = {"HOME": str(home), "XDG_CACHE_HOME": str(home / ".cache"), "PATH": "/usr/bin:/bin",
               "TERM": "xterm-256color", "COLORTERM": "truecolor", "TYPESAFE_API_KEY": "", **self.EXTRA_ENV}
        subprocess.run([str(SUPERFIND), "index"], env=env, capture_output=True, check=True)
        pid, self.terminal = pty.fork()
        if pid == 0:
            fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
            os.execve(str(SUPERFIND), [str(SUPERFIND), "pick"], env)
        self.addCleanup(self.stop, pid, self.terminal)

    def stop(self, pid, terminal):
        os.write(terminal, b"\x03")
        os.close(terminal)
        if os.waitpid(pid, os.WNOHANG) == (0, 0):
            os.kill(pid, 9)
            os.waitpid(pid, 0)

    def read(self, until=None, seconds=10):
        seen, deadline = b"", time.time() + seconds
        while (until is None or until not in seen) and time.time() < deadline:
            if select.select([self.terminal], [], [], 0.05)[0]:
                try:
                    seen += os.read(self.terminal, 65536)
                except OSError:
                    break
        return seen


class PickerColors(PtyPicker):
    COLORS = b"\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\\x1b]10;rgb:dddd/dddd/dddd\x1b\\"
    TINT = b"48;2;41;52;64"

    def test_colors_that_arrive_after_the_first_paint_still_theme_the_picker(self):
        before = self.read(until=b"\x1b[7m")
        self.assertIn(b"\x1b]11;?", before)
        self.assertIn(b"\x1b[7m Ledger", before)
        time.sleep(0.6)
        os.write(self.terminal, self.COLORS)
        self.assertIn(self.TINT, self.read(until=self.TINT))

    def test_the_picker_appears_in_one_finished_frame_once_colors_are_known(self):
        asked = self.read(until=b"\x1b]11;?")
        os.write(self.terminal, self.COLORS)
        shown = asked + self.read(until=b"Migrated.")
        self.assertIn(self.TINT, shown)
        self.assertNotIn(b"Ledger", asked)
        self.assertNotIn(b"\x1b[7m", shown[:shown.index(self.TINT)])

    def test_the_picker_leaves_the_cursor_color_to_the_terminal(self):
        os.write(self.terminal, self.COLORS)
        self.assertNotIn(b"\x1b]12;", self.read(until=b"Migrated."))


class PickerWithoutColor(PtyPicker):
    EXTRA_ENV = {"NO_COLOR": "1"}

    def test_no_color_shows_the_selection_in_reverse_video_and_sends_no_colors(self):
        first = self.read(until=b"Migrated.")
        self.assertIn(b"Ledger", first)
        self.assertIn(b"\x1b[7m", first)
        self.assertNotIn(b"\x1b]11;?", first)
        self.assertEqual(colors_in(first), [])

if __name__ == "__main__":
    unittest.main()
