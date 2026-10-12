import base64
import http.server
import json
import os
import fcntl
import re
import pty
import select
import shutil
import signal
import struct
import subprocess
import tempfile
import termios
import threading
import time
import unittest
from pathlib import Path

from test_superfind import (ANSI, SUPERFIND, claude, codex_call, codex_meta, codex_output, codex_reply, codex_user, colors_in,
                            grok_reply, grok_user, line, serve, tool, uid, user)


ASK_FIRST = "Already open. Enter again resumes a copy. Esc cancels."
GHOSTTY = 'if application "Ghostty" is running then tell application "Ghostty" to '
AGE = re.compile(r"now|\d+[mhd]|[A-Z][a-z]{2} \d{1,4}")
CELL_GAP = r" {2,}"


def tmux(*args):
    return subprocess.run(["tmux", "-L", "superfind", *args], capture_output=True, text=True)


@unittest.skipUnless(shutil.which("tmux"), "the picker tests drive a real terminal through tmux")
class PickerBehavior(unittest.TestCase):
    def setUp(self):
        self.home = Path(tempfile.mkdtemp()).resolve()
        self.projects = self.home / ".claude" / "projects" / "-tmp"
        self.projects.mkdir(parents=True)
        self.out = self.home / "out"
        self.session_name = f"superfind-test-{os.getpid()}-{id(self)}"
        self.clock = time.time() - 3 * 86400
        self.here = Path("/")
        no_ghostty = self.home / "no-ghostty"
        no_ghostty.mkdir()
        (no_ghostty / "osascript").write_text("#!/bin/sh\nexit 1\n")
        (no_ghostty / "osascript").chmod(0o755)
        self.env = {"HOME": str(self.home), "XDG_CACHE_HOME": str(self.home / ".cache"), "PWD": "/elsewhere",
                    "TYPESAFE_API_KEY": "", "NO_COLOR": "1", "PATH": f"{no_ghostty}:/usr/bin:/bin", "TERM": "xterm-256color",
                    "SUPERFIND_TELEMETRY": "off", "SUPERFIND_CHECKIN_URL": "http://127.0.0.1:1/v1/checkin"}

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

    def open_picker(self, width=140, height=32, index_first=True, header="Claude sessions", linger=False, colors=False,
                    pick="pick"):
        if index_first:
            subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        env = " ".join(f"{k}='{v}'" for k, v in self.env.items())
        command = f"env -i {env} {SUPERFIND} {pick} > {self.out}.stdout 2> {self.out}.stderr; echo $? > {self.out}.code"
        if linger:
            command += "; sleep 60"
        style = [";", "set-option", "-t", self.session_name, "window-style", "fg=#dddddd,bg=#1e1e1e"] if colors else []
        tmux("new-session", "-d", "-s", self.session_name, "-c", str(self.here), "-x", str(width), "-y", str(height), command,
             *style)
        self.wait_for(header)

    def screen(self, *flags):
        return tmux("capture-pane", "-p", *flags, "-t", self.session_name).stdout

    def until(self, done, timeout=30):
        deadline = time.time() + timeout
        while not done():
            if time.time() > deadline:
                return False
            time.sleep(0.1)
        return True

    def wait_for(self, text, timeout=30):
        if not self.until(lambda: text in self.screen(), timeout):
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

    def result(self, timeout=30):
        self.assertTrue(self.until(Path(f"{self.out}.code").exists, timeout), f"superfind never exited:\n{self.screen()}")
        code = int(Path(f"{self.out}.code").read_text())
        return code, Path(f"{self.out}.stdout").read_text()

    def alive(self, pids):
        alive = []
        for pid in map(int, pids.read_text().split()):
            try:
                os.kill(pid, 0)
                alive.append(pid)
            except ProcessLookupError:
                pass
        return alive

    def fake_claude(self):
        stub = self.home / "stub"
        stub.mkdir()
        (stub / "claude").write_text(f'#!/bin/sh\nprintf "%s\\n" "$(pwd)" "$@" > {self.out}.resumed\n')
        (stub / "claude").chmod(0o755)
        self.env["PATH"] = f"{stub}:{self.env['PATH']}"

    def test_run_resumes_the_chosen_session_in_place_of_the_picker(self):
        self.fake_claude()
        self.three_sessions()
        self.open_picker(pick="pick --run")
        self.keys("Down", "Enter")
        self.assertEqual(self.result(), (0, "Resuming \x1b[1mInvoice layout\x1b[m\n"))
        self.assertTrue(self.until(Path(f"{self.out}.resumed").exists), "claude never ran")
        self.assertEqual(Path(f"{self.out}.resumed").read_text(), "/tmp\n--resume\nmid\n")

    def test_run_cancelled_with_escape_runs_nothing(self):
        self.fake_claude()
        self.three_sessions()
        self.open_picker(pick="pick --run")
        self.keys("Escape")
        self.assertEqual(self.result(), (1, ""))
        self.assertFalse(Path(f"{self.out}.resumed").exists())

    def test_run_asks_before_a_second_copy_of_a_session_an_agent_has_open(self):
        self.fake_claude()
        self.three_sessions()
        sessions = self.home / ".claude" / "sessions"
        sessions.mkdir()
        (sessions / f"{os.getpid()}.json").write_text(json.dumps({"pid": os.getpid(), "sessionId": "mid", "cwd": "/tmp",
                                                                 "startedAt": int(time.time() * 1000), "status": "idle"}))
        self.open_picker(pick="pick --run")
        self.keys("Down", "Enter")
        self.wait_for(ASK_FIRST)
        self.assertFalse(Path(f"{self.out}.code").exists(), "closed instead of asking first")
        self.assertFalse(Path(f"{self.out}.resumed").exists())
        self.keys("Escape")
        self.assertFalse(Path(f"{self.out}.code").exists(), "Esc left the picker instead of going back")
        self.keys("Enter")
        self.wait_for(ASK_FIRST)
        self.keys("Enter")
        self.assertEqual(self.result(), (0, "Resuming \x1b[1mInvoice layout\x1b[m\x1b[2m · already open in another window\x1b[m\n"))
        self.assertTrue(self.until(Path(f"{self.out}.resumed").exists), "Enter again did not resume a copy")
        self.assertEqual(Path(f"{self.out}.resumed").read_text(), "/tmp\n--resume\nmid\n")

    def test_run_checks_again_for_a_session_opened_while_the_picker_was_up(self):
        self.fake_claude()
        self.three_sessions()
        self.open_picker(pick="pick --run")
        sessions = self.home / ".claude" / "sessions"
        sessions.mkdir()
        (sessions / f"{os.getpid()}.json").write_text(json.dumps({"pid": os.getpid(), "sessionId": "mid", "cwd": "/tmp",
                                                                 "startedAt": int(time.time() * 1000), "status": "idle"}))
        self.keys("Down", "Enter")
        self.wait_for(ASK_FIRST)
        self.assertFalse(Path(f"{self.out}.resumed").exists(), "the session opened while the picker was up was resumed")

    def three_sessions(self):
        self.session("old", "Ledger migration", user("migrate the ledger tables to the new schema"), tool("Bash"),
                     claude("Migrated every ledger row and kept the history."))
        self.session("mid", "Invoice layout", user("the invoice PDF cuts off long company names"), tool("Edit"),
                     claude("Long names now wrap onto two lines."))
        self.session("new", "Checkout double charge", user("customers get charged twice on slow networks"),
                     tool("Edit"), claude("The Pay button now disables itself until the charge finishes."),
                     user("add a test that taps twice"), tool("Edit"), claude("Added a test that taps Pay twice."))

    def row_lines(self):
        return [l.split("│")[0].rstrip() for l in self.screen().expandtabs().splitlines()
                if AGE.fullmatch(re.split(CELL_GAP, l.split("│")[0].strip())[-1])]

    def cells(self):
        return [re.split(CELL_GAP, l.strip()) for l in self.row_lines()]

    def rows(self):
        return ["  ".join(c) for c in self.cells()]

    def titles(self):
        return [c[0] for c in self.cells()]

    def test_sessions_are_listed_newest_first(self):
        self.three_sessions()
        self.open_picker()
        titles = self.titles()
        self.assertEqual(titles[:3], ["Checkout double charge", "Invoice layout", "Ledger migration"])

    def test_every_age_ends_in_one_column_and_a_long_title_keeps_its_age(self):
        self.three_sessions()
        self.session("long", "Rework the invoice numbering so credit notes and refunds share one sequence per tenant",
                     user("renumber invoices"), tool("Edit"), claude("Renumbered."))
        os.utime(self.projects / "long.jsonl", (time.time() - 600, time.time() - 600))
        self.open_picker()
        lines = self.row_lines()
        self.assertEqual(len(lines), 4)
        self.assertEqual({len(l) for l in lines}, {len(lines[0])})
        self.assertEqual(self.cells()[0], ["Rework the invoice numbering so credit notes and refunds…", "tmp", "10m"])

    def label_waiting(self, path):
        st = os.stat(path)
        status = self.home / ".cache" / "superfind" / "status.json"
        status.parent.mkdir(parents=True, exist_ok=True)
        status.write_text(json.dumps({str(path): {"mtime_ns": st.st_mtime_ns, "size": st.st_size, "status": "waiting", "p": 0.97}}))

    def test_a_status_follows_the_title_only_on_the_row_that_has_one(self):
        self.three_sessions()
        self.label_waiting(self.projects / "mid.jsonl")
        self.open_picker()
        self.assertEqual(self.cells(), [["Checkout double charge", "tmp", "2d"], ["Invoice layout", "waiting on you", "tmp", "2d"],
                                        ["Ledger migration", "tmp", "2d"]])
        self.assertTrue(self.row_lines()[1].startswith("  Invoice layout  waiting on you "), self.row_lines()[1])
        self.assertEqual({len(l) for l in self.row_lines()}, {len(self.row_lines()[0])})

    def test_an_active_session_says_active_now_and_still_shows_its_age(self):
        self.three_sessions()
        os.utime(self.projects / "mid.jsonl", (time.time() - 20, time.time() - 20))
        self.open_picker()
        self.assertEqual(self.cells()[0], ["Invoice layout", "active now", "tmp", "1m"])

    def test_one_labeled_session_keeps_project_agent_and_age_on_every_row(self):
        folder = self.home / "payments-reconciliation"
        folder.mkdir()
        self.session("ledger", "Ledger migration", user("migrate the ledger", cwd=str(folder)), tool("Bash"),
                     claude("Migrated."))
        self.codex_session(uid(1), codex_meta(), codex_user("fix the build"), codex_call(), codex_output(),
                           codex_reply("Fixed."))
        self.label_waiting(self.projects / "ledger.jsonl")
        self.open_picker(header="─ Sessions ")
        self.assertEqual(self.cells(), [["fix the build", "tmp", "codex", "2d"],
                                        ["Ledger migration", "waiting on you", "payments-reco…", "claude", "2d"]])

    def test_a_labeled_row_gives_its_agent_cell_to_a_cramped_title(self):
        folder = self.home / "payments-reconciliation"
        folder.mkdir()
        self.session("ledger", "Ledger migration", user("migrate the ledger", cwd=str(folder)), tool("Bash"),
                     claude("Migrated."))
        self.codex_session(uid(1), codex_meta(), codex_user("fix the build"), codex_call(), codex_output(),
                           codex_reply("Fixed."))
        self.label_waiting(self.projects / "ledger.jsonl")
        self.open_picker(width=120, header="─ Sessions ")
        self.assertEqual(self.cells(), [["fix the build", "tmp", "codex", "2d"],
                                        ["Ledger migration", "waiting on you", "payments-reco…", "2d"]])
        self.assertEqual({len(l) for l in self.row_lines()}, {len(self.row_lines()[0])})

    def test_a_narrow_list_drops_the_agent_column_before_the_project(self):
        folder = self.home / "payments-reconciliation"
        folder.mkdir()
        self.session("ledger", "Ledger migration", user("migrate the ledger", cwd=str(folder)), tool("Bash"),
                     claude("Migrated."))
        self.codex_session(uid(1), codex_meta(), codex_user("fix the build"), codex_call(), codex_output(),
                           codex_reply("Fixed."))
        self.open_picker(header="─ Sessions ")
        self.assertEqual(self.cells(), [["fix the build", "tmp", "codex", "2d"],
                                        ["Ledger migration", "payments-reco…", "claude", "2d"]])
        tmux("resize-window", "-t", self.session_name, "-x", "60", "-y", "24")
        narrow = [["fix the build", "tmp", "2d"], ["Ledger migration", "payments-reco…", "2d"]]
        self.assertTrue(self.until(lambda: self.cells() == narrow), self.screen())

    def test_opened_from_a_subfolder_its_sessions_come_first_without_a_project(self):
        for name in ("app/web", "shop"):
            (self.home / name).mkdir(parents=True)
        self.session("here", "Ledger migration", user("migrate the ledger", cwd=str(self.home / "app")), tool("Bash"),
                     claude("Done."))
        self.session("there", "Invoice layout", user("wrap names", cwd=str(self.home / "shop")), tool("Edit"), claude("Done."))
        self.env["PWD"] = str(self.home / "app" / "web")
        self.open_picker()
        self.assertEqual(self.cells(), [["Ledger migration", "2d"], ["Invoice layout", "shop", "2d"]])

    def test_a_session_from_the_home_folder_is_not_here_in_its_subfolders(self):
        for name in ("shop", "other"):
            (self.home / name).mkdir()
        self.session("home", "Dotfiles cleanup", user("tidy the dotfiles", cwd=str(self.home)), tool("Bash"), claude("Done."))
        self.session("other", "Invoice layout", user("wrap names", cwd=str(self.home / "other")), tool("Edit"), claude("Done."))
        self.env["PWD"] = str(self.home / "shop")
        self.open_picker()
        self.assertEqual(self.cells(), [["Invoice layout", "other", "2d"], ["Dotfiles cleanup", self.home.name, "2d"]])

    def test_a_session_from_this_folder_gives_its_blank_project_cells_to_the_title(self):
        for name in ("shop", "payments-reconciliation"):
            (self.home / name).mkdir()
        self.session("there", "Ledger migration", user("migrate the ledger", cwd=str(self.home / "payments-reconciliation")),
                     tool("Bash"), claude("Done."))
        self.session("here", "Checkout button double-charge on slow networks",
                     user("customers get charged twice", cwd=str(self.home / "shop")), tool("Edit"), claude("Done."))
        self.env["PWD"] = str(self.home / "shop")
        self.open_picker(width=112, height=16)
        self.assertEqual(self.cells(), [["Checkout button double-charge on slow networks", "2d"],
                                        ["Ledger migration", "payments-reco…", "2d"]])
        self.assertEqual({len(l) for l in self.row_lines()}, {len(self.row_lines()[0])})

    def test_sessions_another_program_started_appear_only_with_is_agent(self):
        self.three_sessions()
        self.session("sdk", "Nightly eval run", line(type="user", cwd="/tmp", entrypoint="sdk-cli",
                                                     message={"role": "user", "content": "score the ledger answers"}),
                     tool("Bash"), claude("Scored 40 answers."))
        self.codex_session("exec1", line(timestamp="2026-10-09T10:00:00Z", type="session_meta",
                                          payload={"id": "meta", "cwd": "/tmp", "source": "exec"}),
                           line(timestamp="2026-10-09T10:00:00Z", type="response_item", payload={
                               "type": "message", "role": "user", "content": [{"type": "input_text", "text": "review the ledger diff"}]}))
        self.open_picker()
        self.assertEqual(sorted(self.titles()), ["Checkout double charge", "Invoice layout", "Ledger migration"])
        self.assertIn("3/3", self.screen())
        self.assertNotIn("codex", self.screen())
        self.keys("is:agent")
        self.wait_for("2/2")
        self.assertEqual(sorted(self.titles()), ["Nightly eval run", "review the ledger diff"])

    def test_a_picker_holding_only_sessions_another_program_started_says_how_to_see_them(self):
        self.session("sdk", "Nightly eval run", line(type="user", cwd="/tmp", entrypoint="sdk-cli",
                                                     message={"role": "user", "content": "score the ledger answers"}),
                     tool("Bash"), claude("Scored 40 answers."))
        self.open_picker()
        self.wait_for("Only automated sessions found. Type is:agent to see them.")
        self.assertNotIn("0/", self.screen())
        self.keys("is:agent")
        self.assertTrue(self.until(lambda: self.titles() == ["Nightly eval run"]), self.screen())
        self.assertIn("1/1", self.screen())
        self.assertNotIn("Only automated sessions found", self.screen())

    def test_typing_narrows_the_list_and_shows_the_matching_passage(self):
        self.three_sessions()
        self.open_picker()
        tmux("set-buffer", "-b", self.session_name, "charged")
        tmux("paste-buffer", "-p", "-d", "-b", self.session_name, "-t", self.session_name)
        self.wait_for("1/3")
        self.assertEqual(self.titles(), ["Checkout double charge"])
        self.assertIn("customers get charged twice", self.screen())

    def test_sessions_that_match_about_as_well_list_newest_first(self):
        self.session("old", "Ledger migration", user("move the ledger"), tool("Bash"), claude("Moved."))
        self.session("new", "Ledger migration follow-up", user("check the ledger"), tool("Bash"), claude("Checked."))
        self.session("other", "Invoice layout", user("wrap names"), tool("Edit"), claude("Wrapped."))
        self.open_picker()
        self.keys("ledger")
        self.wait_for("2/3")
        self.assertEqual(self.titles(), ["Ledger migration follow-up", "Ledger migration"])

    def list_column(self):
        return "\n".join(re.sub(r"(\x1b\[[0-9;]*m| )+$", "", l.split("│")[0]) for l in self.screen("-e").splitlines())

    def test_the_selected_row_has_a_bold_title_and_full_strength_columns(self):
        self.three_sessions()
        self.open_picker()
        rows = [l for l in self.list_column().splitlines() if "tmp" in l]
        self.assertRegex(rows[0], r"\x1b\[1mCheckout double charge")
        self.assertNotIn("\x1b[2m", rows[0])
        self.assertNotRegex(rows[1], r"\x1b\[1mInvoice layout")
        self.assertRegex(rows[1], r"\x1b\[2mtmp")

    def answering_claude(self):
        stub = self.home / "stub"
        stub.mkdir()
        (stub / "claude").write_text("#!/bin/sh\ncat > /dev/null\necho '{\"result\": \"[0]\"}'\n")
        (stub / "claude").chmod(0o755)
        self.env["PATH"] = f"{stub}:{self.env['PATH']}"

    def two_ledger_sessions(self):
        self.session("other", "Invoice layout", user("wrap names"), tool("Edit"), claude("Wrapped."))
        self.session("old", "Ledger migration", user("move the ledger"), tool("Bash"), claude("Moved."))
        self.session("new", "Ledger migration follow-up", user("check the ledger"), tool("Bash"), claude("Checked."))

    def ledger_rows(self):
        self.keys("ledger")
        self.wait_for("2/3")
        return [l for l in self.list_column().splitlines() if "migration" in l]

    def test_matched_words_are_bold_and_blue_while_searching_and_plain_in_ask_results(self):
        self.answering_claude()
        del self.env["NO_COLOR"]
        self.two_ledger_sessions()
        self.open_picker(colors=True)
        blue = r"\x1b\[1m\x1b\[[0-9;]*34mLedger\x1b\[[0-9;]*m migration"
        selected, unselected = self.ledger_rows()
        self.assertRegex(unselected, r"^  " + blue + r"( |\x1b)")
        self.keys("C-r")
        self.assertTrue(self.until(lambda: self.titles() == ["Ledger migration follow-up"]), self.screen())
        self.assertNotIn("Asking", self.screen())
        self.assertNotRegex(self.list_column(), blue)

    def test_matched_words_are_bold_under_no_color(self):
        self.two_ledger_sessions()
        self.open_picker()
        selected, unselected = self.ledger_rows()
        self.assertRegex(unselected, r"^  \x1b\[1mLedger\x1b\[0m migration ")
        self.assertNotIn("34m", selected + unselected)

    def test_a_match_on_the_selected_row_is_bold_without_a_blue_block_when_the_terminal_hides_its_colors(self):
        del self.env["NO_COLOR"]
        self.two_ledger_sessions()
        self.open_picker()
        selected, unselected = self.ledger_rows()
        self.assertRegex(selected, r"^ \x1b\[7m \x1b\[1mLedger migration follow-up")
        self.assertNotIn("34m", selected + unselected)

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
        self.assertEqual(self.titles(), ["Checkout double charge"])

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
        self.assertEqual(self.titles(), ["Checkout double charge"])

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
        self.assertEqual(self.titles(), ["Checkout double charge"])

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
        self.assertEqual(self.titles(), ["Invoice layout"])

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
        self.env["PATH"] = f"{stub}:{self.env['PATH']}"
        self.open_picker()
        self.keys("is:waiting invoice names", "C-r")
        self.wait_for("1/3")
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
        self.keys("abc", "C-r")
        screen = self.settled_screen()
        self.assertIn("No sessions yet in ~/.claude, ~/.codex, or ~/.grok.", screen)
        self.assertIn("esc quit", screen)
        self.assertNotIn("Asking", screen)
        self.assertNotIn("deep search", screen)
        self.keys("Escape")
        self.assertEqual(self.result(), (1, ""))

    def test_shrinking_the_terminal_to_one_cell_and_back_redraws_the_picker(self):
        self.three_sessions()
        self.open_picker()
        tmux("resize-window", "-t", self.session_name, "-x", "1", "-y", "1")
        self.assertEqual(tmux("display", "-p", "-t", self.session_name, "#{window_width}x#{window_height}").stdout, "1x1\n")
        tmux("resize-window", "-t", self.session_name, "-x", "140", "-y", "32")
        titles = ["Checkout double charge", "Invoice layout", "Ledger migration"]
        self.assertTrue(self.until(lambda: self.titles() == titles), self.screen())

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
        self.wait_for("No keyword matches. Ctrl-R searches by description.")
        self.assertIn("ctrl-u clear search", self.screen())
        self.keys("C-u")
        self.wait_for("3/3")

    def test_the_search_box_has_no_prompt_mark_and_the_cursor_follows_the_text(self):
        self.three_sessions()
        self.open_picker()
        self.keys("led")
        box = next(l for l in self.screen().splitlines() if "1/3" in l)
        cursor = tmux("display", "-p", "-t", self.session_name, "#{cursor_x},#{cursor_y}").stdout.strip()
        self.assertEqual((box[:7], cursor), (" │ led ", f"6,{self.screen().splitlines().index(box)}"))

    def test_enter_prints_the_title_and_the_command_that_resumes_it(self):
        self.three_sessions()
        self.open_picker()
        self.keys("Down", "Enter")
        self.assertEqual(self.result(), (0, "Invoice layout\ncd /tmp && claude --resume mid\n"))

    def test_enter_on_a_session_whose_transcript_is_gone_explains_and_runs_nothing(self):
        prompt = line(display="fix the billing retry loop", pastedContents={}, timestamp=int(self.clock * 1000), project="/tmp",
                      sessionId="aaaaaaaa-1111-4111-8111-111111111111")
        (self.home / ".claude" / "history.jsonl").write_text(prompt + "\n")
        self.open_picker()
        self.wait_for("transcript deleted")
        self.keys("Enter")
        self.wait_for("Claude Code deleted this session's transcript, so Claude can't resume it.")
        self.keys("Escape")
        self.assertEqual(self.result(), (1, ""))

    def test_the_first_picker_after_an_update_says_whats_new_once_and_prints_what_it_always_did(self):
        version = subprocess.run([str(SUPERFIND), "--version"], capture_output=True, text=True).stdout.split()[1]
        changelog = (Path(__file__).resolve().parent.parent / "CHANGELOG.md").read_text().splitlines()
        section = changelog.index(next(l for l in changelog if l.startswith(f"## [{version}]")))
        headline = next(l for l in changelog[section + 1:] if l.strip())[2:]
        notice = f"New in {version} · {headline}"
        self.three_sessions()
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        marker = self.home / ".local" / "state" / "superfind" / "shown" / "version"
        self.assertEqual(marker.read_text(), f"{version}\n")
        marker.write_text("0.10.0\n")
        last = subprocess.run([str(SUPERFIND), "last"], env=self.env, cwd="/tmp", stdin=subprocess.DEVNULL,
                              capture_output=True, text=True)
        self.assertEqual((last.returncode, last.stdout), (0, "Checkout double charge\ncd /tmp && claude --resume new\n"))
        self.open_picker()
        self.wait_for(notice)
        self.keys("Down", "Enter")
        self.assertEqual(self.result(), (0, "Invoice layout\ncd /tmp && claude --resume mid\n"))
        tmux("kill-session", "-t", self.session_name)
        Path(f"{self.out}.code").unlink()
        self.open_picker()
        self.assertNotIn("New in", self.settled_screen())
        self.assertEqual(marker.read_text(), f"{version}\n")

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
        self.assertTrue(self.until(lambda: "Added a test that taps Pay twice" not in self.screen()), self.screen())

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
        self.assertEqual(self.titles(), ["Checkout double charge"])

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
        self.wait_for("Finished rollout step 40.")
        self.assertIn("Finished rollout step 40.", self.settled_screen())

    def test_a_broken_search_index_says_so_instead_of_finding_nothing(self):
        import sqlite3
        self.three_sessions()
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        db = sqlite3.connect(self.home / ".cache" / "superfind" / "search.db")
        db.executescript("CREATE TABLE plain AS SELECT path, title, body FROM docs; DROP TABLE docs; ALTER TABLE plain RENAME TO docs;")
        db.close()
        self.open_picker()
        self.keys("charged")
        self.wait_for("Search failed: the search index couldn't be read")
        self.assertNotIn("No session", self.screen())
        self.keys(" after:4")
        time.sleep(0.5)
        self.assertIn("Search failed: the search index couldn't be read", self.screen())
        self.assertNotIn("after: takes", self.screen())

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
        start = {"zsh": "zsh -f -i", "bash": "bash --norc --noprofile -i", "fish": f"{shutil.which('fish')} --no-config -i"}[shell]
        tmux("new-session", "-d", "-s", self.session_name, "-x", "140", "-y", "32", f"env -i {env} {start}")
        time.sleep(0.5)
        setup = f"{SUPERFIND} init fish | source" if shell == "fish" else f'eval "$({SUPERFIND} init {shell})"'
        self.keys(setup, "Enter")
        self.keys(*open_keys)
        self.wait_for("Claude sessions")
        self.keys("Down", "Enter")
        self.assertTrue(self.until(Path(f"{self.out}.resumed").exists), self.screen())
        self.assertEqual(Path(f"{self.out}.resumed").read_text(), "/tmp --resume mid\n")
        self.keys("pwd", "Enter")
        self.wait_for("Resuming")
        self.assertIn("\n/tmp", self.screen())

    def last_through_shell(self, shell):
        stub = self.home / "stub"
        stub.mkdir()
        (stub / "claude").write_text(f'#!/bin/sh\necho "$(pwd) $*" > {self.out}.resumed\n')
        (stub / "claude").chmod(0o755)
        self.folder_sessions()
        self.session("shop-new", "Newest elsewhere", user("charge once", cwd=str(self.home / "shop")), tool("Edit"),
                     claude("Done."))
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        env = " ".join(f"{k}='{v}'" for k, v in {**self.env, "PATH": f"{stub}:/usr/bin:/bin"}.items())
        start = {"zsh": "zsh -f -i", "bash": "bash --norc --noprofile -i", "fish": f"{shutil.which('fish')} --no-config -i"}[shell]
        tmux("new-session", "-d", "-s", self.session_name, "-x", "160", "-y", "32", f"env -i {env} {start}")
        time.sleep(0.5)
        self.keys(f"{SUPERFIND} init fish | source" if shell == "fish" else f'eval "$({SUPERFIND} init {shell})"', "Enter")
        self.keys(f"cd {self.home / 'app'}", "Enter")
        if shell != "fish":
            self.keys("IFS=:", "Enter")
        self.keys("superfind last --json", "Enter")
        self.wait_for('"schema":1')
        time.sleep(0.5)
        self.assertNotIn("Resuming", self.screen())
        self.assertFalse(Path(f"{self.out}.resumed").exists())
        self.keys("clear", "Enter")
        self.keys("superfind last", "Enter")
        self.assertTrue(self.until(Path(f"{self.out}.resumed").exists), self.screen())
        folder = self.home / "app"
        self.assertEqual(Path(f"{self.out}.resumed").read_text(), f"{folder} --resume app\n")
        self.keys("pwd", "Enter")
        self.wait_for("Resuming")
        self.assertIn("Resuming Ledger migration", self.screen())
        self.wait_for(f"\n{folder}\n")

    def test_typing_superfind_last_in_zsh_resumes_the_newest_session_here_in_that_shell(self):
        self.last_through_shell("zsh")

    def test_typing_superfind_last_in_bash_resumes_the_newest_session_here_in_that_shell(self):
        self.last_through_shell("bash")

    @unittest.skipUnless(shutil.which("fish"), "needs fish")
    def test_typing_superfind_last_in_fish_resumes_the_newest_session_here_in_that_shell(self):
        self.last_through_shell("fish")

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

    @unittest.skipUnless(shutil.which("fish"), "needs fish")
    def test_typing_superfind_in_fish_resumes_the_chosen_session_in_that_shell(self):
        self.resume_through_shell("fish", ["superfind", "Enter"])

    @unittest.skipUnless(shutil.which("fish"), "needs fish")
    def test_typing_sfd_in_fish_resumes_the_chosen_session_in_that_shell(self):
        self.resume_through_shell("fish", ["sfd", "Enter"])

    @unittest.skipUnless(shutil.which("fish"), "needs fish")
    def test_ctrl_x_c_in_fish_resumes_the_chosen_session_in_that_shell(self):
        self.resume_through_shell("fish", ["C-x", "c"])

    def test_the_list_appears_while_indexing_and_the_selection_survives_the_refresh(self):
        self.three_sessions()
        subprocess.run([str(SUPERFIND), "index"], env=self.env, capture_output=True, check=True)
        self.session("newest", "Search ranking tweaks", user("tune the ranking"), tool("Edit"), claude("Tuned."))
        lock = open(self.home / ".cache" / "superfind" / "index.lock", "w")
        fcntl.flock(lock, fcntl.LOCK_EX)
        self.open_picker(index_first=False)
        self.keys("Down")
        self.assertEqual(self.titles()[:3],
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
        self.assertEqual(self.cells()[:2], [["fix the build", "tmp", "codex", "2d"], ["Checkout double charge", "tmp", "claude", "2d"]])
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

        url = serve(self, Recorder)
        self.three_sessions()
        self.codex_session(uid(1), codex_meta(), codex_user("migrate the ledger index"), codex_call(), codex_output(),
                           codex_reply("Migrated."))
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=url)
        self.open_picker(header="─ Sessions ")
        self.keys("agent:codex ledger", "C-r")
        self.assertTrue(self.until(lambda: received), "TypeSafe was never asked")
        cards = [c.split(" | ")[0] for k, c in received[0]["questions"]["which"]["criteria"].items() if k != "none"]
        self.assertEqual((received[0]["state"]["search"], cards), ("ledger", ["migrate the ledger index"]))

    def test_clearing_an_ask_stops_it_before_more_session_cards_are_sent(self):
        received, cleared, answered = [], threading.Event(), threading.Event()
        self.addCleanup(cleared.set)

        class Slow(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                sent = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                received.append(sent)
                if "which" in sent["questions"]:
                    cleared.wait(30)
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
                if "which" in sent["questions"]:
                    answered.set()

            def log_message(self, *args):
                pass

        url = serve(self, Slow, threaded=True)
        self.three_sessions()
        status = self.home / ".cache" / "superfind" / "status.json"
        status.parent.mkdir(parents=True, exist_ok=True)
        status.write_text("{}")
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=url)
        self.open_picker()
        self.keys("ledger", "C-r")
        self.assertTrue(self.until(lambda: any("which" in r["questions"] for r in received)), "the ask never asked TypeSafe")
        self.keys("C-u")
        self.assertTrue(self.until(lambda: "Asking" not in self.screen()), self.screen())
        cleared.set()
        self.assertTrue(self.until(answered.is_set))
        self.until(lambda: len([r for r in received if "status" not in r["questions"]]) > 1, timeout=3)
        self.assertEqual([list(r["questions"]) for r in received if "status" not in r["questions"]], [["which"]])

    def test_quitting_during_an_ask_stops_claude_and_what_it_started(self):
        stub = self.home / "stub"
        stub.mkdir()
        pids = self.home / "pids"
        (stub / "claude").write_text(f"#!/bin/sh\necho $$ >> {pids}\nsleep 30 &\necho $! >> {pids}\nwait\n")
        (stub / "claude").chmod(0o755)
        self.three_sessions()
        self.env["PATH"] = f"{stub}:{self.env['PATH']}"
        self.addCleanup(lambda: [subprocess.run(["kill", "-9", pid], capture_output=True)
                                 for pid in (pids.read_text().split() if pids.exists() else [])])
        self.open_picker(linger=True)
        self.keys("ledger", "C-r")
        self.assertTrue(self.until(lambda: len(pids.read_text().split() if pids.exists() else []) >= 2), "claude never started")
        self.assertIn("esc stop asking", self.screen())
        self.keys("Escape")
        self.until(lambda: not self.alive(pids))
        self.assertEqual(self.alive(pids), [])
        self.assertFalse(Path(f"{self.out}.code").exists())
        self.assertEqual(self.titles(), ["Ledger migration"])
        self.assertNotIn("Asking", self.screen())
        self.keys("Escape")
        self.assertEqual(self.result(), (1, ""))

    def test_escape_leaves_ask_results_for_the_keyword_search_and_then_quits(self):
        self.answering_claude()
        self.three_sessions()
        self.open_picker()
        self.keys("ledger")
        self.wait_for("1/3")
        self.keys("C-r")
        self.wait_for("Sessions by description")
        self.assertIn("enter resume · tab view · esc back to search", self.screen())
        self.assertEqual(self.titles(), ["Checkout double charge"])
        self.keys("Escape")
        self.wait_for("Claude sessions")
        self.assertNotIn("Sessions by description", self.screen())
        self.assertEqual(self.titles(), ["Ledger migration"])
        self.assertIn("enter resume · tab view · ctrl-r ask", self.screen())
        self.keys("Escape")
        self.assertEqual(self.result(), (1, ""))

    def test_interrupting_the_picker_during_an_ask_stops_claude_too(self):
        stub = self.home / "stub"
        stub.mkdir()
        pids = self.home / "pids"
        (stub / "claude").write_text(f"#!/bin/sh\necho $PPID > {pids}.parent\necho $$ >> {pids}\nsleep 30 &\necho $! >> {pids}\nwait\n")
        (stub / "claude").chmod(0o755)
        self.three_sessions()
        self.env["PATH"] = f"{stub}:{self.env['PATH']}"
        self.addCleanup(lambda: [subprocess.run(["kill", "-9", pid], capture_output=True)
                                 for pid in (pids.read_text().split() if pids.exists() else [])])
        self.open_picker(linger=True)
        self.keys("ledger", "C-r")
        self.assertTrue(self.until(lambda: len(pids.read_text().split() if pids.exists() else []) >= 2), "claude never started")
        os.kill(int(Path(f"{pids}.parent").read_text()), 2)
        self.result()
        self.until(lambda: not self.alive(pids))
        self.assertEqual(self.alive(pids), [])

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
        self.assertEqual(self.titles(), ["Checkout double charge"])

    def test_damage_found_during_an_ask_does_not_crash_the_picker(self):
        import sqlite3

        asked = threading.Event()

        class Slow(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                sent = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                if "which" in sent["questions"] and "s0" in sent["questions"]["which"]["criteria"]:
                    asked.set()
                    time.sleep(3)
                answers = {q: {"choice": list(spec.get("criteria", {"x": 1}))[0], "noul": 0.9,
                               "probabilities": {k: (0.9 if k == "s0" else 0.01) for k in spec.get("criteria", {})}}
                           for q, spec in sent["questions"].items()}
                body = json.dumps({"answers": answers}).encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args):
                pass

        url = serve(self, Slow, threaded=True)
        self.three_sessions()
        status = self.home / ".cache" / "superfind" / "status.json"
        status.parent.mkdir(parents=True, exist_ok=True)
        status.write_text("{}")
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=url)
        self.open_picker()
        tmux("set-buffer", "-b", self.session_name, "charged")
        tmux("paste-buffer", "-p", "-d", "-b", self.session_name, "-t", self.session_name)
        self.wait_for("1/3")
        db = sqlite3.connect(self.home / ".cache" / "superfind" / "search.db")
        db.execute("UPDATE docs_data SET block = zeroblob(length(block)) WHERE id = 10")
        db.commit()
        db.close()
        self.keys("C-r")
        self.assertTrue(self.until(asked.is_set), "the ask never asked TypeSafe")
        status.write_text("{}")
        os.utime(status, (time.time() + 5, time.time() + 5))
        time.sleep(5)
        self.assertFalse(Path(f"{self.out}.code").exists(), Path(f"{self.out}.stderr").read_text()[-500:])

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
        self.wait_for("Ask failed: Claude Code didn't answer.")
        self.assertFalse(Path(f"{self.out}.code").exists())

    def test_alt_enter_also_asks_in_terminals_that_send_it(self):
        self.three_sessions()
        self.open_picker()
        self.keys("where did we stop double charges", "M-Enter")
        self.wait_for("Ask failed: Claude Code didn't answer.")

    def folder_sessions(self):
        for name in ("app/sub", "shop"):
            (self.home / name).mkdir(parents=True)
        self.session("app", "Ledger migration", user("migrate the ledger", cwd=str(self.home / "app"), branch="main"),
                     tool("Bash"), claude("Done."))
        self.session("sub", "Invoice layout", user("wrap names", cwd=str(self.home / "app" / "sub"), branch="fix"),
                     tool("Edit"), claude("Done."))
        self.session("shop", "Checkout double charge", user("charge once", cwd=str(self.home / "shop"), branch="fix"),
                     tool("Edit"), claude("Done."))
        self.session("dotfiles", "Dotfiles cleanup", user("tidy", cwd=str(self.home), branch="main"), tool("Bash"),
                     claude("Done."))
        self.session("old", "Old ledger fix", user("fix the ledger", cwd=str(self.home / "app"), branch="fix"),
                     tool("Bash"), claude("Done."))
        os.utime(self.projects / "old.jsonl", (time.time() - 10 * 86400, time.time() - 10 * 86400))
        self.here = self.home / "app"
        self.env["PWD"] = str(self.here)

    def test_filters_in_the_picker_list_what_search_prints_in_the_same_order(self):
        self.folder_sessions()
        self.open_picker()
        for query in ("in:here", "in:here branch:FIX", "branch:fix after:5d", "in:here ledger"):
            printed = subprocess.run([str(SUPERFIND), "search", *query.split()], env=self.env, cwd=self.here,
                                     capture_output=True, text=True, check=True).stdout
            want = [row.split("\t")[4] for row in printed.splitlines()]
            self.keys("C-u", query)
            self.assertTrue(self.until(lambda: self.titles() == want), (query, want, self.screen()))
        self.keys("C-u", "in:here")
        self.assertTrue(self.until(lambda: self.titles() == ["Ledger migration", "Old ledger fix"]), self.screen())

    def test_a_half_typed_filter_keeps_the_list_says_what_it_takes_and_cannot_ask(self):
        stub = self.home / "stub"
        stub.mkdir()
        (stub / "claude").write_text(f"#!/bin/sh\ncat > /dev/null\ntouch {self.out}.asked\necho '{{\"result\": \"[0]\"}}'\n")
        (stub / "claude").chmod(0o755)
        self.env["PATH"] = f"{stub}:{self.env['PATH']}"
        self.three_sessions()
        self.open_picker()
        self.keys("ledger after:4")
        self.wait_for("after: takes a date like 2026-10-09, yesterday, or 3d.")
        self.assertTrue(self.until(lambda: self.titles() == ["Ledger migration"]), self.screen())
        self.keys("C-r")
        time.sleep(1)
        self.assertFalse(Path(f"{self.out}.asked").exists())
        self.assertIn("after: takes a date", self.screen())
        self.keys(*["BSpace"] * 7, "in:")
        self.wait_for("in: takes here, as in in:here.")
        self.assertTrue(self.until(lambda: self.titles() == ["Ledger migration"]), self.screen())
        self.keys(*["BSpace"] * 3, "after:4d")
        self.assertTrue(self.until(lambda: "takes" not in self.screen()), self.screen())
        self.assertTrue(self.until(lambda: self.titles() == ["Ledger migration"]), self.screen())


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

        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=serve(self, TypeSafe))
        return received

    def test_explanation_passages_cover_every_message_and_all_of_a_long_one(self):
        received = self.explaining_typesafe()
        long_reply = " ".join(f"word{n:03d}" for n in range(125))
        self.session("ledger", "Ledger migration", user("migrate the ledger"), tool("Bash"),
                     claude("I changed ledger.go to batch the writes."), user("now add a test for the ledger"),
                     claude(long_reply))
        self.open_picker()
        self.keys("where did we migrate the ledger", "C-r")
        self.assertTrue(self.until(lambda: received), "TypeSafe was never asked")
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
        self.assertTrue(self.until(lambda: received), "TypeSafe was never asked")
        passages = [received[0][k] for k in received[0] if k != "none"]
        self.assertEqual(passages, ["migrate the ledger " + paragraphs[0] + " " + paragraphs[1], paragraphs[2], paragraphs[3]])
        self.assertEqual(list(received[0]), [f"p{i}" for i in range(len(received[0]) - 1)] + ["none"])


class PtyPicker(unittest.TestCase):
    EXTRA_ENV = {}
    INDEX = True

    def setUp(self):
        home = Path(tempfile.mkdtemp()).resolve()
        self.addCleanup(shutil.rmtree, home, ignore_errors=True)
        projects = home / ".claude" / "projects" / "-tmp"
        projects.mkdir(parents=True)
        (projects / "a1.jsonl").write_text(user("migrate the ledger") + "\n" + tool("Bash") + "\n" +
                                          claude("Migrated.") + "\n" + line(type="ai-title", aiTitle="Ledger") + "\n")
        env = {"HOME": str(home), "XDG_CACHE_HOME": str(home / ".cache"), "PATH": "/usr/bin:/bin",
               "TERM": "xterm-256color", "COLORTERM": "truecolor", "TYPESAFE_API_KEY": "", "SUPERFIND_TELEMETRY": "off", "SUPERFIND_CHECKIN_URL": "http://127.0.0.1:1/v1/checkin", **self.EXTRA_ENV}
        if self.INDEX:
            subprocess.run([str(SUPERFIND), "index"], env=env, capture_output=True, check=True)
        else:
            for n in range(400):
                (projects / f"s{n}.jsonl").write_text(user(f"reconcile ledger batch {n} " * 2000) + "\n" + claude("Done.") + "\n")
        pid, self.terminal = pty.fork()
        self.pid = pid
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
    DEVICE = b"\x1b[?62;22c"
    TINT = b"48;2;46;63;82"

    def test_colors_that_arrive_after_the_first_paint_still_theme_the_picker(self):
        asked = self.read(until=b"\x1b[c")
        self.assertIn(b"\x1b[c", asked)
        self.assertLess(max(asked.index(b"\x1b]10;?"), asked.index(b"\x1b]11;?")), asked.index(b"\x1b[c"))
        os.write(self.terminal, self.DEVICE)
        first = asked + self.read(until=b"\x1b[7m")
        self.assertIn(b"\x1b[7m \x1b[1mLedger", first)
        os.write(self.terminal, self.COLORS)
        themed = self.read(until=self.TINT)
        self.assertIn(self.TINT, themed)
        self.assertNotIn(b"62;22c", first + themed)

    def test_a_terminal_that_answers_no_queries_still_gets_the_picker(self):
        self.assertIn(b"\x1b[7m \x1b[1mLedger", self.read(until=b"\x1b[7m"))

    def test_the_picker_appears_in_one_finished_frame_once_colors_are_known(self):
        asked = self.read(until=b"\x1b]11;?")
        os.write(self.terminal, self.COLORS + self.DEVICE)
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
        self.assertNotIn(b"\x1b[c", first)
        self.assertEqual(colors_in(first), [])

def working(title):
    return b"\x1b]7501;state=working:app=superfind:title=" + base64.b64encode(title.encode()) + b"\x07"


class PickerStatus(PtyPicker):
    WORKING = working("Asking")
    CLEAR = b"\x1b]7501;state=clear\x07"

    def setUp(self):
        stub = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, stub, ignore_errors=True)
        self.release = stub / "release"
        (stub / "claude").write_text("#!/bin/sh\ncat > /dev/null\n"
                                     f"while [ ! -e '{self.release}' ]; do sleep 0.05; done\n"
                                     "echo '{\"result\": \"[0]\"}'\n")
        (stub / "claude").chmod(0o755)
        self.EXTRA_ENV = {"PATH": f"{stub}:/usr/bin:/bin"}
        super().setUp()

    def ask(self):
        seen = self.read(until=b"Migrated.")
        os.write(self.terminal, b"ledger\x12")
        seen += self.read(until=self.WORKING)
        self.assertIn(self.WORKING, seen, "asking never reported working")
        return seen

    def last_status(self, seen):
        found = re.findall(rb"\x1b\]7501;[^\x07]*\x07", seen)
        return found[-1] if found else b""

    def rest(self):
        seen = self.read(seconds=5)
        os.close(self.terminal)
        self.terminal = os.open("/dev/null", os.O_RDWR)
        return seen


class AskStatus(PickerStatus):
    def test_asking_reports_working_until_the_answer_arrives(self):
        seen = self.ask()
        seen += self.read(seconds=1)
        self.assertNotIn(self.CLEAR, seen, "the status was cleared while the ask was still running")
        self.release.touch()
        seen += self.read(until=self.CLEAR)
        self.assertEqual(self.last_status(seen), self.CLEAR)


class StoppedAskStatus(PickerStatus):

    def test_stopping_an_ask_with_esc_clears_its_status(self):
        self.ask()
        os.write(self.terminal, b"\x1b")
        self.assertEqual(self.last_status(self.read(until=self.CLEAR)), self.CLEAR)

    def test_ctrl_c_while_asking_clears_the_status_before_exit(self):
        self.ask()
        os.write(self.terminal, b"\x03")
        self.assertEqual(self.last_status(self.rest()), self.CLEAR)

    def test_sigterm_while_asking_clears_the_status_before_exit(self):
        self.ask()
        os.kill(self.pid, signal.SIGTERM)
        self.assertEqual(self.last_status(self.rest()), self.CLEAR)

    def test_choosing_while_asking_clears_the_status_before_the_agent_starts(self):
        self.ask()
        os.write(self.terminal, b"\r")
        self.assertEqual(self.last_status(self.rest()), self.CLEAR)


class IndexingStatus(PtyPicker):
    INDEX = False

    def test_the_first_index_reports_working_until_it_finishes(self):
        os.write(self.terminal, PickerColors.COLORS + PickerColors.DEVICE)
        seen = self.read(until=working("Indexing"), seconds=20)
        self.assertIn(working("Indexing"), seen, "the first index never reported working")
        if PickerStatus.CLEAR not in seen.split(working("Indexing"))[-1]:
            seen += self.read(until=PickerStatus.CLEAR, seconds=60)
        self.assertEqual(PickerStatus.last_status(self, seen), PickerStatus.CLEAR)


class QuietPicker(PtyPicker):
    def test_a_picker_that_does_no_work_sends_no_status(self):
        seen = self.read(until=b"Migrated.")
        os.write(self.terminal, b"\x03")
        seen += self.read(seconds=2)
        os.close(self.terminal)
        self.terminal = os.open("/dev/null", os.O_RDWR)
        self.assertFalse(b"7501" in seen, "a picker that did no work sent a status")


class QuietPickerWithoutColor(QuietPicker):
    EXTRA_ENV = {"NO_COLOR": "1"}


class EnterOnAnOpenSession(unittest.TestCase):
    def setUp(self):
        self.home = Path(tempfile.mkdtemp()).resolve()
        self.addCleanup(shutil.rmtree, self.home, True)
        self.sockets = tempfile.mkdtemp(dir="/tmp", prefix="sfd")
        self.addCleanup(shutil.rmtree, self.sockets, True)
        self.tmux_env = {k: v for k, v in os.environ.items() if k != "TMUX"}
        self.tmux_env["TMUX_TMPDIR"] = self.sockets
        self.out = self.home / "out"
        self.stub = self.home / "stub"
        self.stub.mkdir()
        (self.stub / "osascript").write_text(f"""#!/bin/sh
printf '%s\\n' "$2" >> {self.stub}/told
[ "$(/bin/cat {self.stub}/mode)" = sluggish ] && /bin/sleep 0.6
case "$2" in
'{GHOSTTY}get tty of every terminal')
    case "$(/bin/cat {self.stub}/mode)" in
    error) echo "execution error: Ghostty got an error: Can't get tty of every terminal. (-1728)" >&2; exit 1 ;;
    slow) /bin/sleep 30 & echo $! > {self.stub}/straggler; /bin/sleep 3 ;;
    esac
    /bin/cat {self.stub}/ttys ;;
'{GHOSTTY}focus (first terminal whose tty is "'*) printf '%s\\n' "$2" >> {self.stub}/focused ;;
*) echo "osascript: a script that could start Ghostty" >&2; exit 1 ;;
esac
""")
        (self.stub / "osascript").chmod(0o755)
        self.osascript("error", "")
        projects = self.home / ".claude" / "projects" / "-tmp"
        projects.mkdir(parents=True)
        transcript = projects / f"{uid(1)}.jsonl"
        transcript.write_text("\n".join([user("migrate the ledger tables"), tool("Bash"), claude("Migrated."),
                                           line(type="ai-title", aiTitle="Ledger migration")]) + "\n")
        os.utime(transcript, (time.time() - 86400, time.time() - 86400))

    def tmux(self, *args):
        return subprocess.run(["tmux", *args], capture_output=True, text=True, env=self.tmux_env, check=True).stdout

    def osascript(self, mode, ttys):
        (self.stub / "mode").write_text(mode)
        (self.stub / "ttys").write_text(ttys + "\n")

    def focused(self):
        log = self.stub / "focused"
        return log.read_text() if log.exists() else ""

    def told(self):
        log = self.stub / "told"
        return log.read_text() if log.exists() else ""

    def env(self, inside=None):
        env = {"HOME": str(self.home), "XDG_CACHE_HOME": str(self.home / ".cache"), "TYPESAFE_API_KEY": "", "NO_COLOR": "1",
               "PATH": f"{self.stub}:" + os.path.dirname(shutil.which("tmux")) + ":/usr/bin:/bin", "TERM": "xterm-256color",
               "TMUX_TMPDIR": self.sockets, "PWD": "/tmp", "SUPERFIND_TELEMETRY": "off", "SUPERFIND_CHECKIN_URL": "http://127.0.0.1:1/v1/checkin"}
        if inside:
            env["TMUX"] = inside
        return env

    def register(self, pid):
        sessions = self.home / ".claude" / "sessions"
        sessions.mkdir(exist_ok=True)
        (sessions / f"{pid}.json").write_text(json.dumps(
            {"pid": int(pid), "sessionId": uid(1), "cwd": "/tmp", "startedAt": int(time.time() * 1000), "status": "idle"}))

    def in_a_ghostty_tab(self):
        ghostty = self.home / "Ghostty" / "ghostty"
        ghostty.parent.mkdir()
        ghostty.symlink_to(shutil.which("dash") or "/bin/sh")
        pid_file, tty_file = self.home / "agent.pid", self.home / "agent.tty"
        child, terminal = pty.fork()
        if child == 0:
            os.execve(str(ghostty), [str(ghostty), "-c", f"tty > {tty_file}; sleep 300 & echo $! > {pid_file}; wait"],
                      {"PATH": "/usr/bin:/bin"})
        threading.Thread(target=self.drain, args=(terminal,), daemon=True).start()
        self.addCleanup(os.waitpid, child, 0)
        self.addCleanup(os.kill, child, signal.SIGKILL)
        deadline = time.time() + 5
        while time.time() < deadline and not (pid_file.exists() and pid_file.read_text().strip() and tty_file.exists()):
            time.sleep(0.05)
        agent = int(pid_file.read_text())
        self.addCleanup(os.kill, agent, signal.SIGKILL)
        self.register(agent)
        other, other_tty = os.openpty()
        self.addCleanup(os.close, other)
        self.addCleanup(os.close, other_tty)
        return tty_file.read_text().strip(), os.ttyname(other_tty)

    def host(self):
        self.tmux("new-session", "-d", "-s", "work", "-x", "140", "-y", "32", "exec sleep 300")
        self.addCleanup(subprocess.run, ["tmux", "kill-server"], capture_output=True, env=self.tmux_env)

    def open_elsewhere(self):
        self.tmux("new-session", "-d", "-s", "work", "-x", "140", "-y", "32", "exec sleep 300")
        self.addCleanup(subprocess.run, ["tmux", "kill-server"], capture_output=True, env=self.tmux_env)
        self.tmux("new-window", "-d", "-t", "work:1", "exec sleep 300")
        pid, pane = self.tmux("display-message", "-p", "-t", "work:1", "#{pane_pid}\t#{pane_id}").strip().split("\t")
        socket, server = self.tmux("display-message", "-p", "-t", "work:0", "#{socket_path}\t#{pid}").strip().split("\t")
        self.register(pid)
        return pane, f"{socket},{server},0"

    def pick(self, inside):
        env = self.env(inside)
        subprocess.run([str(SUPERFIND), "index"], env=env, capture_output=True, check=True)
        assignments = " ".join(f"{k}='{v}'" for k, v in env.items())
        command = f"env -i {assignments} {SUPERFIND} pick > {self.out}.stdout 2> {self.out}.stderr; echo $? > {self.out}.code; sleep 60"
        self.tmux("respawn-pane", "-k", "-t", "work:0", command)
        self.wait_for("Claude sessions")
        self.keys("Ledger migration")
        self.wait_for("Migrated.")

    def screen(self):
        return self.tmux("capture-pane", "-p", "-t", "work:0")

    def wait_for(self, text, timeout=5):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if text in self.screen():
                return
            time.sleep(0.1)
        self.fail(f"never saw {text!r} on screen:\n{self.screen()}")

    def keys(self, *keys):
        self.tmux("send-keys", "-t", "work:0", *keys)
        time.sleep(0.4)

    def result(self, timeout=5):
        deadline = time.time() + timeout
        while time.time() < deadline and not Path(f"{self.out}.code").exists():
            time.sleep(0.1)
        return int(Path(f"{self.out}.code").read_text()), Path(f"{self.out}.stdout").read_text()

    @staticmethod
    def read_until_closed(terminal):
        try:
            while os.read(terminal, 4096):
                pass
        except OSError:
            pass

    @staticmethod
    def drain(terminal):
        try:
            while os.read(terminal, 4096):
                pass
        except OSError:
            pass
        os.close(terminal)

    def test_pick_inside_tmux_hands_the_shell_a_switch_to_the_agents_pane(self):
        pane, inside = self.open_elsewhere()
        self.pick(inside)
        self.keys("Enter")
        code, out = self.result()
        title, command = out.splitlines()
        self.assertEqual((code, command), (0, f"tmux switch-client -t '{pane}'"))
        self.assertTrue(title.startswith("Ledger migration"), title)
        self.assertIn(" · in tmux session work, window 1", title)

    @unittest.skipUnless(shutil.which("bash"), "the shell setup runs the switch")
    def test_enter_through_the_shell_setup_moves_an_attached_client_to_the_agents_pane(self):
        pane, inside = self.open_elsewhere()
        env = self.env(inside)
        subprocess.run([str(SUPERFIND), "index"], env=env, capture_output=True, check=True)
        assignments = " ".join(f"{k}='{v}'" for k, v in env.items())
        shell = f"eval \"$({SUPERFIND} init bash)\"; superfind; sleep 60"
        self.tmux("respawn-pane", "-k", "-t", "work:0", f"env -i {assignments} {shutil.which('bash')} --norc --noprofile -c '{shell}'")
        client, terminal = pty.fork()
        if client == 0:
            os.execvpe("tmux", ["tmux", "attach-session", "-t", "work:0"], {**self.tmux_env, "TERM": "xterm-256color"})
        threading.Thread(target=self.drain, args=(terminal,), daemon=True).start()
        self.addCleanup(os.waitpid, client, 0)
        self.addCleanup(subprocess.run, ["tmux", "kill-server"], capture_output=True, env=self.tmux_env)
        deadline = time.time() + 5
        while time.time() < deadline and not self.tmux("list-clients", "-F", "#{pane_id}").strip():
            time.sleep(0.1)
        self.wait_for("Claude sessions")
        self.keys("Ledger migration")
        self.wait_for("Migrated.")
        self.keys("Enter")
        deadline = time.time() + 5
        while time.time() < deadline and self.tmux("list-clients", "-F", "#{pane_id}").strip() != pane:
            time.sleep(0.1)
        self.assertEqual(self.tmux("list-clients", "-F", "#{session_name} #{window_index} #{pane_id}").strip(),
                         f"work 1 {pane}")

    def test_enter_outside_tmux_asks_before_resuming_a_copy_and_esc_goes_back(self):
        self.open_elsewhere()
        self.pick(None)
        self.keys("Enter")
        self.wait_for(ASK_FIRST)
        self.keys("Escape")
        self.assertNotIn(ASK_FIRST, self.screen())
        self.assertFalse(Path(f"{self.out}.code").exists())
        self.keys("Escape")
        self.assertEqual(self.result(), (1, ""))
        self.assertEqual(self.focused(), "")

    def test_enter_on_a_session_outside_ghostty_asks_first_even_when_a_ghostty_tab_shares_its_terminal(self):
        self.open_elsewhere()
        self.osascript("match", self.tmux("display-message", "-p", "-t", "work:1", "#{pane_tty}").strip())
        self.pick(None)
        self.keys("Enter")
        self.wait_for(ASK_FIRST)
        self.keys("Escape")
        self.keys("Escape")
        self.assertEqual(self.result(), (1, ""))
        self.assertEqual(self.focused(), "")

    def test_enter_on_a_session_in_a_ghostty_tab_switches_to_that_tab_and_hands_the_shell_nothing(self):
        tty, other = self.in_a_ghostty_tab()
        self.osascript("match", f"{other}, {tty}")
        self.host()
        self.pick(None)
        self.keys("Enter")
        self.assertEqual(self.result(), (1, ""))
        self.assertEqual(self.focused(), f'{GHOSTTY}focus (first terminal whose tty is "{tty}")\n')

    def test_enter_switches_tabs_when_ghostty_answers_each_question_within_a_second(self):
        tty, other = self.in_a_ghostty_tab()
        self.osascript("sluggish", f"{other}, {tty}")
        self.host()
        self.pick(None)
        self.keys("Enter")
        self.assertEqual(self.result(), (1, ""))
        self.assertEqual(self.focused(), f'{GHOSTTY}focus (first terminal whose tty is "{tty}")\n')

    def test_enter_when_no_ghostty_tab_has_the_sessions_terminal_asks_first_and_enter_again_resumes_a_copy(self):
        tty, other = self.in_a_ghostty_tab()
        self.osascript("match", other)
        self.host()
        self.pick(None)
        self.keys("Enter")
        self.wait_for(ASK_FIRST)
        self.keys("Enter")
        code, out = self.result()
        title, command = ANSI.sub("", out).splitlines()
        self.assertEqual((code, title), (0, "Ledger migration · already open in another window"))
        self.assertTrue(command.endswith(f"claude --resume {uid(1)}"), command)
        self.assertEqual(self.focused(), "")

    def asks_first_within_a_second(self, mode):
        tty, _ = self.in_a_ghostty_tab()
        self.osascript(mode, tty)
        self.host()
        self.pick(None)
        self.tmux("send-keys", "-t", "work:0", "Enter")
        entered = time.time()
        self.wait_for(ASK_FIRST, timeout=2.5)
        self.assertLess(time.time() - entered, 1.5)
        self.keys("Escape")
        self.assertFalse(Path(f"{self.out}.code").exists())
        self.keys("Escape")
        self.assertEqual(self.result(), (1, ""))
        self.assertEqual(self.focused(), "")

    def test_enter_asks_first_when_ghostty_cannot_list_its_terminals(self):
        self.asks_first_within_a_second("error")

    def test_enter_asks_first_within_a_second_when_ghostty_does_not_answer(self):
        self.asks_first_within_a_second("slow")
        straggler = int((self.stub / "straggler").read_text())
        deadline = time.time() + 1
        while time.time() < deadline and self.alive(straggler):
            time.sleep(0.05)
        if self.alive(straggler):
            os.kill(straggler, signal.SIGKILL)
            self.fail("a process osascript started outlived the second superfind gave it")

    @staticmethod
    def alive(pid):
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            return False
        return True

    def last(self):
        env = self.env()
        subprocess.run([str(SUPERFIND), "index"], env=env, capture_output=True, check=True)
        result = subprocess.run([str(SUPERFIND), "last"], env=env, cwd="/tmp", stdin=subprocess.DEVNULL, capture_output=True, text=True)
        return result.returncode, result.stdout, result.stderr

    def at_a_terminal(self, command, key):
        env = self.env()
        subprocess.run([str(SUPERFIND), "index"], env=env, capture_output=True, check=True)
        child, terminal = pty.fork()
        if child == 0:
            os.chdir("/tmp")
            os.execve("/bin/sh", ["/bin/sh", "-c", command], env)
        screen, sent = b"", key is None
        deadline = time.time() + 10
        while time.time() < deadline:
            ready, _, _ = select.select([terminal], [], [], 0.1)
            if not ready:
                continue
            try:
                chunk = os.read(terminal, 4096)
            except OSError:
                break
            if not chunk:
                break
            screen += chunk
            if not sent and b"Esc cancels." in screen:
                os.write(terminal, key)
                sent = True
        os.close(terminal)
        _, status = os.waitpid(child, 0)
        return os.waitstatus_to_exitcode(status), screen.decode(errors="replace")

    def test_last_in_a_script_says_where_the_session_is_open_and_starts_no_copy(self):
        tty, other = self.in_a_ghostty_tab()
        self.osascript("match", other)
        self.assertEqual(self.last(), (1, "", "superfind last: Ledger migration is already open in another window. "
                                              "superfind last doesn't start a second copy.\n"))
        self.assertEqual(self.focused(), "")

    def test_last_at_a_terminal_asks_first_and_only_enter_resumes_a_copy(self):
        tty, other = self.in_a_ghostty_tab()
        self.osascript("match", other)
        for key, want in ((b"\r", 0), (b"\x1b", 1)):
            with self.subTest(key=key):
                code, screen = self.at_a_terminal(f"{SUPERFIND} last > {self.out}.stdout", key)
                self.assertIn("Ledger migration is open in another window. Enter resumes a copy; Esc cancels.", screen)
                out = Path(f"{self.out}.stdout").read_text()
                self.assertEqual(code, want, screen)
                if want == 0:
                    title, command = ANSI.sub("", out).splitlines()
                    self.assertEqual(title, "Ledger migration · already open in another window")
                    self.assertTrue(command.endswith(f"claude --resume {uid(1)}"), command)
                else:
                    self.assertEqual(out, "")

    def test_a_signal_while_last_asks_first_gives_the_terminal_back_as_it_was(self):
        self.in_a_ghostty_tab()
        env = self.env()
        subprocess.run([str(SUPERFIND), "index"], env=env, capture_output=True, check=True)
        for sig, want in ((signal.SIGTERM, 1), (signal.SIGINT, 130), (signal.SIGHUP, 1)):
            with self.subTest(signal=sig.name):
                terminal, follower = os.openpty()
                self.addCleanup(os.close, terminal)
                self.addCleanup(os.close, follower)
                last = subprocess.Popen([str(SUPERFIND), "last"], env=env, cwd="/tmp", stdin=follower, stdout=subprocess.DEVNULL,
                                        stderr=follower, start_new_session=True,
                                        preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
                self.addCleanup(last.wait)
                screen, deadline = b"", time.time() + 5
                while time.time() < deadline and b"Esc cancels." not in screen:
                    if select.select([terminal], [], [], 0.1)[0]:
                        screen += os.read(terminal, 4096)
                self.assertIn(b"Esc cancels.", screen)
                time.sleep(0.2)
                threading.Thread(target=self.read_until_closed, args=(terminal,), daemon=True).start()
                last.send_signal(sig)
                self.assertEqual(last.wait(timeout=5), want)
                lflag = termios.tcgetattr(terminal)[3]
                self.assertEqual(lflag & (termios.ECHO | termios.ICANON), termios.ECHO | termios.ICANON)

    def test_last_without_a_terminal_leaves_ghostty_alone_even_when_a_tab_has_the_session(self):
        tty, other = self.in_a_ghostty_tab()
        self.osascript("match", f"{tty}, {other}")
        self.assertEqual(self.last(), (1, "", "superfind last: Ledger migration is already open in another window. "
                                              "superfind last doesn't start a second copy.\n"))
        self.assertEqual(self.told(), "")

    def test_last_at_a_terminal_on_a_session_in_a_ghostty_tab_switches_to_that_tab_and_prints_nothing(self):
        tty, other = self.in_a_ghostty_tab()
        self.osascript("match", f"{tty}, {other}")
        code, screen = self.at_a_terminal(f"{SUPERFIND} last > {self.out}.stdout", None)
        self.assertEqual((code, Path(f"{self.out}.stdout").read_text()), (0, ""), screen)
        self.assertIn("Ledger migration is open in another Ghostty tab; switched to it.", screen)
        self.assertEqual(self.focused(), f'{GHOSTTY}focus (first terminal whose tty is "{tty}")\n')

    @unittest.skipUnless(shutil.which("bash"), "runs the bash shell setup")
    def test_the_shell_setup_does_nothing_after_last_switches_to_a_ghostty_tab(self):
        tty, _ = self.in_a_ghostty_tab()
        self.osascript("match", tty)
        setup = f'eval "$({SUPERFIND} init bash)"; superfind last; echo "status $?"'
        code, screen = self.at_a_terminal(f"{shutil.which('bash')} --norc --noprofile -c '{setup}'", None)
        self.assertIn("switched to it.", screen)
        self.assertIn("status 0", screen)
        self.assertNotIn("Resuming", screen)


if __name__ == "__main__":
    unittest.main()
