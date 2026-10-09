import json
import os
import re
import shutil
import subprocess
import tempfile
import time
import unittest
import urllib.parse
from pathlib import Path

HOP = Path(os.environ.get("HOP_BIN", Path(__file__).resolve().parent.parent / "bin" / "hop"))
ANSI = re.compile(r"\x1b\[[0-9;]*m")


def line(**entry):
    return json.dumps(entry, separators=(",", ":"))


def user(text, cwd="/tmp", branch="main"):
    return line(type="user", cwd=cwd, gitBranch=branch, message={"role": "user", "content": text})


def claude(text):
    return line(type="assistant", message={"role": "assistant", "content": [{"type": "text", "text": text}]})


def tool(name):
    return line(type="assistant", message={"role": "assistant", "content": [
        {"type": "tool_use", "id": "t1", "name": name, "input": {}}]})


def uid(n):
    return f"0199{n:04d}-a751-7860-aac9-738072c82895"


def codex_meta(cwd="/tmp", parent=None, branch=None):
    payload = {"id": "meta", "cwd": cwd, "originator": "codex_cli_rs", "source": "cli",
               "git": {"branch": branch} if branch else None}
    if parent:
        payload.update(thread_source="subagent", parent_thread_id=parent)
    return line(timestamp="2026-10-09T10:00:00Z", type="session_meta", payload=payload)


def codex_item(**payload):
    return line(timestamp="2026-10-09T10:00:00Z", type="response_item", payload=payload)


def codex_user(*blocks):
    return codex_item(type="message", role="user", content=[
        b if isinstance(b, dict) else {"type": "input_text", "text": b} for b in blocks])


def codex_reply(text):
    return codex_item(type="message", role="assistant", content=[{"type": "output_text", "text": text}])


def codex_call():
    return codex_item(type="function_call", name="shell", arguments="{}", call_id="c1")


def codex_output(output="ok"):
    return codex_item(type="function_call_output", call_id="c1", output=output)


def codex_turn(cwd):
    return line(timestamp="2026-10-09T10:00:00Z", type="turn_context", payload={"cwd": cwd})


def codex_answers(*answers):
    replies = [{"answer": a, "question": "?", "questionItemId": f"[\"request_user_input_async\",\"c1\",{n}]"}
               for n, a in enumerate(answers)]
    return codex_user(f"<send_user_message_question_reply>\n{json.dumps(replies)}\n</send_user_message_question_reply>")


def codex_aborted():
    return line(timestamp="2026-10-09T10:00:00Z", type="event_msg", payload={"type": "turn_aborted", "reason": "interrupted"})


def grok_user(text, raw=False, **extra):
    wrapped = text if raw else f"<user_query>\n{text}\n</user_query>"
    return line(type="user", content=[{"type": "text", "text": wrapped}], prompt_index=0, **extra)


def grok_reply(text, tools=0):
    return line(type="assistant", content=text, model_id="grok-4.7",
                tool_calls=[{"id": f"call{i}", "name": "run_terminal_cmd", "arguments": "{}"} for i in range(tools)])


def grok_result():
    return line(type="tool_result", content="ok", tool_call_id="call0")


def grok_synthetic(text):
    return line(type="user", content=[{"type": "text", "text": text}], synthetic_reason="system_reminder")


GROK_SYSTEM = line(type="system", content="You are Grok.")
GROK_OPENER = line(type="user", content=[{"type": "text", "text": "<user_info>\nOS: macOS\n</user_info>\n<rules>\nbe careful\n</rules>"}])
CODEX_ENVIRONMENT = "<environment_context>\n  <cwd>/tmp</cwd>\n</environment_context>"


class HopBehavior(unittest.TestCase):
    def setUp(self):
        self.home = Path(tempfile.mkdtemp())
        self.projects = self.home / ".claude" / "projects" / "-work-billing"
        self.projects.mkdir(parents=True)
        self.env = {**os.environ, "HOME": str(self.home), "XDG_CACHE_HOME": str(self.home / ".cache"),
                    "PWD": "/elsewhere", "TYPESAFE_API_KEY": ""}
        for name in ("CLAUDE_CONFIG_DIR", "HOP_CLAUDE_DIRS", "CODEX_HOME", "GROK_HOME", "HOP_CODEX_DIRS", "HOP_GROK_DIRS"):
            self.env.pop(name, None)
        self.clock = time.time() - 2 * 86400

    def tick(self, *paths):
        self.clock += 60
        for p in paths:
            os.utime(p, (self.clock, self.clock))

    def codex_session(self, session_id, *lines, home=".codex", day="2026/10/09", archived=False):
        root = self.home / home
        folder = root / "archived_sessions" if archived else root / "sessions" / day
        folder.mkdir(parents=True, exist_ok=True)
        path = folder / f"rollout-{day.replace('/', '-')}T10-00-00-{session_id}.jsonl"
        path.write_text("\n".join(lines) + "\n")
        self.tick(path)
        return path.resolve()

    def codex_name(self, session_id, name, home=".codex"):
        with open(self.home / home / "session_index.jsonl", "a") as index:
            index.write(line(id=session_id, thread_name=name, updated_at="2026-10-09T10:00:00Z") + "\n")

    def grok_session(self, session_id, cwd, *lines, title=None, manual=False, kind=None, home=".grok", summary=True):
        folder = self.home / home / "sessions" / urllib.parse.quote(cwd, safe="") / session_id
        folder.mkdir(parents=True)
        chat = folder / "chat_history.jsonl"
        chat.write_text("\n".join(lines) + "\n")
        written = [chat]
        if summary:
            written.append(folder / "summary.json")
            self.grok_summary(chat, cwd, title, manual, kind)
        self.tick(*written)
        return chat.resolve()

    def grok_summary(self, chat, cwd, title=None, manual=False, kind=None):
        fields = {"info": {"id": chat.parent.name, "cwd": cwd}, "head_branch": "main", "title_is_manual": manual,
                  "created_at": "2026-10-09T10:00:00Z", "num_messages": 4}
        if title:
            fields.update(session_summary=title, generated_title=title)
        if kind:
            fields["session_kind"] = kind
        (chat.parent / "summary.json").write_text(json.dumps(fields, indent=2))

    def record_typesafe(self):
        import http.server
        import threading
        requests = []

        class Recorder(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                requests.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
                self.send_response(500)
                self.send_header("Content-Length", "0")
                self.end_headers()

            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Recorder)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=f"http://127.0.0.1:{server.server_port}")
        return requests

    def session(self, session_id, *lines, title=None):
        path = self.projects / f"{session_id}.jsonl"
        body = list(lines) + ([line(type="ai-title", aiTitle=title)] if title else [])
        path.write_text("\n".join(body) + "\n")
        self.clock += 60
        os.utime(path, (self.clock, self.clock))
        return path.resolve()

    def hop(self, *args):
        result = subprocess.run([str(HOP), *args], env=self.env, capture_output=True, text=True, check=True)
        return ANSI.sub("", result.stdout)

    def run_hop(self, *args):
        return subprocess.run([str(HOP), *args], env=self.env, capture_output=True, text=True)

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
            {str(path): {"mtime_ns": st.st_mtime_ns, "size": st.st_size, "status": status, "p": p}}))

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
        self.assertNotIn("**", self.hop("preview", str(path), "--width", "40"))

    def test_a_label_expires_once_you_have_left_a_session_for_two_weeks(self):
        waiting = self.session("a1", user("deploy the ledger migration"), tool("Bash"),
                               claude("Should I run it on production now?"), title="Ledger migration")
        old = time.time() - 15 * 86400
        os.utime(waiting, (old, old))
        self.judged(waiting, "waiting")
        self.assertEqual(self.rows("is:waiting"), [])

    def test_deep_search_says_why_it_failed_when_no_ranker_can_answer(self):
        self.session("a1", user("deploy the ledger migration"), tool("Bash"), claude("Done."), title="Ledger migration")
        self.hop("index")
        self.env["PATH"] = "/usr/bin:/bin"
        result = self.run_hop("search", "--deep", "where did we migrate the ledger")
        self.assertEqual((result.returncode, result.stdout, result.stderr.strip()),
                         (1, "", "Deep search failed: Claude Code didn't answer."))

    def test_a_hostile_transcript_name_cannot_inject_shell_commands(self):
        path = self.session("a;echo PWNED", user("fix the build"), tool("Bash"), claude("Fixed."), title="Build fix")
        self.hop("index")
        self.assertEqual(self.hop("command", str(path)).strip(), "cd /tmp && claude --resume 'a;echo PWNED'")

    def test_an_empty_deep_search_lists_sessions_without_asking_anyone(self):
        self.session("a1", user("fix the build"), tool("Bash"), claude("Fixed."), title="Build fix")
        self.hop("index")
        self.env["PATH"] = "/usr/bin:/bin"
        self.assertEqual([r.split("\t")[4] for r in self.hop("search", "--deep").splitlines()], ["Build fix"])

    def test_tabs_expand_to_the_next_tab_stop(self):
        path = self.session("a1", user("show the table"), tool("Read"), claude("aaa\tbbb"), title="Table")
        self.hop("index")
        self.assertIn("  aaa     bbb", self.hop("preview", str(path), "--width", "40"))

    def test_wide_characters_wrap_by_their_width_and_none_are_lost(self):
        text = "数据迁移已完成所有表都已更新并通过了检查"
        path = self.session("a1", user("summarize in chinese"), tool("Read"), claude(text), title="Summary")
        self.hop("index")
        body = self.hop("preview", str(path), "--width", "24").split("claude\n", 1)[1]
        lines = [l for l in body.splitlines() if l.strip()]
        self.assertEqual("".join(l.strip() for l in lines), text)
        self.assertTrue(all(sum(2 for _ in l.strip()) + 2 <= 22 for l in lines), lines)

    def test_carriage_returns_and_non_breaking_indents_render_like_ordinary_lines(self):
        path = self.session("a1", user("list the steps"), tool("Read"),
                           claude("First step\r\nSecond step\rThird step\r\n\u00a0\u00a0  nested step"), title="Steps")
        self.hop("index")
        body = self.hop("preview", str(path), "--width", "40").split("claude\n", 1)[1]
        self.assertEqual(body.split("\n")[:4], ["  First step", "  Second step", "  Third step", "      nested step"])
        self.assertNotIn("\r", body)

    def test_an_indent_wider_than_the_preview_still_renders(self):
        path = self.session("a1", user("show the tree"), tool("Read"), claude("intro\n" + "\u00a0" * 30 + "x"),
                            title="Tree")
        self.hop("index")
        result = subprocess.run([str(HOP), "preview", str(path), "--width", "20"], env=self.env,
                                capture_output=True, text=True, timeout=5)
        self.assertEqual(ANSI.sub("", result.stdout).split("claude\n", 1)[1], "  intro\n" + " " * 32 + "x\n")

    def test_a_cache_folder_with_url_characters_in_its_name_still_works(self):
        self.env["XDG_CACHE_HOME"] = str(self.home / "cache ?#%20 dir")
        self.session("a1", user("make payment retries safe to repeat"), claude("Added idempotency keys."),
                     title="Payment retries")
        self.assertEqual(self.titles("idempotency"), ["Payment retries"])
        self.assertTrue((self.home / "cache ?#%20 dir" / "hop" / "search.db").exists())

    def test_a_relative_cache_folder_works(self):
        self.env["XDG_CACHE_HOME"] = "relative-cache"
        self.session("a1", user("make payment retries safe to repeat"), claude("Added idempotency keys."),
                     title="Payment retries")
        for command in (["index"], ["search", "idempotency"]):
            result = subprocess.run([str(HOP), *command], env=self.env, cwd=self.home, capture_output=True, text=True)
            self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertIn("Payment retries", result.stdout)
        self.assertTrue((self.home / "relative-cache" / "hop" / "search.db").exists())

    def test_indexing_waits_for_another_writer_instead_of_failing(self):
        import sqlite3
        import threading

        self.session("a1", user("rename the invoice PDF files"), claude("Renamed them."), title="Invoice file names")
        self.hop("index")
        self.session("b2", user("make payment retries safe to repeat"), claude("Added idempotency keys."),
                     title="Payment retries")
        writer = sqlite3.connect(self.home / ".cache" / "hop" / "search.db", isolation_level=None, check_same_thread=False)
        writer.execute("BEGIN IMMEDIATE")
        threading.Timer(1.0, lambda: writer.execute("COMMIT")).start()
        result = self.run_hop("index")
        writer.close()
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        self.assertEqual(self.titles("idempotency"), ["Payment retries"])

    def test_a_transcript_written_with_spaced_json_is_still_read(self):
        spaced = [json.dumps(json.loads(l)) for l in (
            user("make payment retries safe to repeat"), tool("Edit"), claude("Added idempotency keys."))]
        self.session("a1", *spaced, title="Payment retries")
        self.assertEqual(self.titles("idempotency"), ["Payment retries"])

    def test_a_tool_call_whose_input_looks_like_a_transcript_line_still_counts_as_work(self):
        self.session("old", user("rename the invoice PDF files"), tool("Edit"), claude("Renamed them."),
                     title="Invoice file names")
        self.session("new", user("replay the fixture"), line(type="assistant", message={"role": "assistant", "content": [
            {"type": "tool_use", "id": "t1", "name": "Replay", "input": {"type": "user", "text": "hi"}}]}),
                     claude("Replayed."), title="Fixture replay")
        self.assertEqual(self.titles(), ["Fixture replay", "Invoice file names"])

    def test_a_transcript_that_could_not_be_read_is_read_again_next_time(self):
        path = self.session("a1", user("make payment retries safe to repeat"), claude("Added idempotency keys."),
                            title="Payment retries")
        path.chmod(0)
        self.addCleanup(path.chmod, 0o644)
        self.hop("index")
        path.chmod(0o644)
        self.assertEqual(self.titles("idempotency"), ["Payment retries"])

    def test_an_edit_that_keeps_the_size_and_lands_within_a_microsecond_is_still_noticed(self):
        path = self.session("a1", user("tune the alpha retries"), claude("Done."), title="Retries")
        self.hop("index")
        before = os.stat(path).st_mtime_ns
        path.write_text(path.read_text().replace("alpha", "omega"))
        os.utime(path, ns=(before + 500, before + 500))
        self.assertEqual(self.titles("omega"), ["Retries"])

    def test_a_session_written_after_the_last_index_is_found_without_reindexing(self):
        self.session("a1", user("rename the invoice PDF files"), claude("Renamed them."), title="Invoice file names")
        self.hop("index")
        path = self.session("b2", user("upgrade the kubernetes cluster"), claude("Upgraded."), title="Cluster upgrade")
        self.assertEqual([r.split("\t")[4] for r in self.hop("search", "kubernetes").splitlines()], ["Cluster upgrade"])
        self.assertEqual(self.hop("command", str(path)), "cd /tmp && claude --resume b2\n")

    def break_search_index(self):
        import sqlite3
        db = sqlite3.connect(self.home / ".cache" / "hop" / "search.db")
        db.executescript("DROP TABLE docs; CREATE TABLE docs (path, title, body);")
        db.close()

    def test_a_broken_search_index_says_so_instead_of_finding_nothing(self):
        self.session("a1", user("upgrade the kubernetes cluster"), claude("Upgraded."), title="Cluster upgrade")
        self.hop("index")
        self.break_search_index()
        result = self.run_hop("search", "kubernetes")
        self.assertEqual((result.returncode, result.stdout), (1, ""))
        self.assertTrue(result.stderr.startswith("hop search: the search index couldn't be read: "), result.stderr)
        deep = self.run_hop("search", "--deep", "where did we upgrade the cluster")
        self.assertEqual((deep.returncode, deep.stdout), (1, ""))
        self.assertTrue(deep.stderr.startswith("Deep search failed: the search index couldn't be read ("), deep.stderr)

    def test_labeling_without_a_key_says_what_it_needs(self):
        result = self.run_hop("label")
        self.assertEqual((result.returncode, result.stderr),
                         (1, "hop label: needs a TypeSafe key in TYPESAFE_API_KEY or ~/.config/hop/env\n"))

    def test_labels_for_deleted_sessions_are_forgotten(self):
        import http.server
        import threading

        class Finished(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                self.rfile.read(int(self.headers["Content-Length"]))
                body = b'{"answers": {"status": {"choice": "finished", "probabilities": {"finished": 0.9}}}}'
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Finished)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        gone = self.session("gone", user("try a spike"), tool("Bash"), claude("Spiked."), title="Spike")
        self.judged(gone, "waiting")
        gone.unlink()
        kept = self.session("kept", user("deploy the ledger migration"), tool("Bash"), claude("Deployed."),
                            title="Ledger migration")
        os.utime(kept, (time.time() - 600, time.time() - 600))
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=f"http://127.0.0.1:{server.server_port}")
        self.hop("label")
        status = json.loads((self.home / ".cache" / "hop" / "status.json").read_text())
        self.assertEqual({p: e["status"] for p, e in status.items()}, {str(kept): "finished"})

    def test_typesafe_requests_are_ascii_json_with_sessions_in_list_order(self):
        import http.server
        import threading
        bodies = []

        class Recorder(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                bodies.append(self.rfile.read(int(self.headers["Content-Length"])))
                self.send_response(500)
                self.send_header("Content-Length", "0")
                self.end_headers()

            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Recorder)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        self.session("a1", user("rename the invoice PDF files"), tool("Edit"), claude("Renamed them."),
                     title="Invoice file names")
        self.session("b2", user("fix the café layout"), tool("Edit"), claude("Fixed."),
                     title="Fix <b> & “quotes” 🚀 in café")
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=f"http://127.0.0.1:{server.server_port}",
                        PATH="/usr/bin:/bin")
        self.run_hop("search", "--deep", "where did we fix the cafe")
        raw = bodies[0].decode("ascii")
        self.assertIn('"s0":"Fix <b> & \\u201cquotes\\u201d \\ud83d\\ude80 in caf\\u00e9 | ', raw)
        self.assertLess(raw.index('"s0":'), raw.index('"s1":'))
        self.assertLess(raw.index('"s1":'), raw.index('"none":'))

    def test_prose_wraps_like_python_textwrap_at_tokens_hyphens_and_dash_runs(self):
        cases = [
            ("see https://example.com/a/very/long/path/that/cannot/fit/on/one/line ok", 24,
             ["see https://example.", "com/a/very/long/path", "/that/cannot/fit/on/", "one/line ok"]),
            ("we shipped the well-known state-of-the-art double-charge-prevention fix", 24,
             ["we shipped the well-", "known state-of-the-", "art double-charge-", "prevention fix"]),
            ("first part--second part--third part of the sentence here", 30,
             ["first part--second part--", "third part of the sentence", "here"]),
        ]
        for n, (text, width, expected) in enumerate(cases):
            with self.subTest(text=text):
                path = self.session(f"w{n}", user("go"), tool("Read"), claude(text), title=f"Wrap {n}")
                self.hop("index")
                body = self.hop("preview", str(path), "--width", str(width)).split("claude\n", 1)[1]
                self.assertEqual(body.splitlines(), ["  " + l for l in expected])

    def test_a_transcript_that_could_not_be_read_is_not_labeled_until_it_can_be(self):
        import http.server
        import threading
        judged = []

        class Waiting(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                judged.append(json.loads(self.rfile.read(int(self.headers["Content-Length"])))["state"])
                body = b'{"answers": {"status": {"choice": "waiting", "probabilities": {"waiting": 0.95}}}}'
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Waiting)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        path = self.session("a1", user("deploy the ledger migration"), tool("Bash"),
                            claude("Should I run it on production?"), title="Ledger migration")
        self.hop("index")
        path.chmod(0)
        self.addCleanup(path.chmod, 0o644)
        os.utime(path, (time.time() - 600, time.time() - 600))
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=f"http://127.0.0.1:{server.server_port}")
        unreadable = self.run_hop("label")
        status = self.home / ".cache" / "hop" / "status.json"
        self.assertEqual((unreadable.returncode, judged), (1, []))
        self.assertNotIn(str(path), json.loads(status.read_text()) if status.exists() else {})
        path.chmod(0o644)
        self.hop("label")
        self.assertEqual(json.loads(status.read_text())[str(path)]["status"], "waiting")
        self.assertEqual(judged[0]["recent_turns"][-1]["text"], "Should I run it on production?")

    def test_a_preview_of_an_unreadable_transcript_says_so_instead_of_claiming_no_reply(self):
        path = self.session("a1", user("deploy the ledger migration"), tool("Bash"), claude("Deployed."),
                            title="Ledger migration")
        self.hop("index")
        path.chmod(0)
        self.addCleanup(path.chmod, 0o644)
        result = self.run_hop("preview", str(path))
        body = ANSI.sub("", result.stdout)
        self.assertEqual(result.returncode, 1)
        self.assertIn("  Couldn't read this session: open ", body)
        self.assertNotIn("No reply yet", body)

    def test_shell_setup_calls_hop_by_its_full_path(self):
        self.assertIn(f"typeset -g _hop_bin={HOP}", self.hop("init", "zsh"))

    def test_malformed_typesafe_answers_never_crash_deep_search_or_get_cached_as_labels(self):
        import http.server
        import threading

        class Malformed(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                self.rfile.read(int(self.headers["Content-Length"]))
                body = b'{"answers": {"which": {"probabilities": {"": 1, "s999": 0.5}}, "status": {"choice": "bogus"}}}'
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Malformed)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        self.session("a1", user("deploy the ledger migration"), tool("Bash"), claude("Should I run it on production?"),
                     title="Ledger migration")
        self.env.update(TYPESAFE_API_KEY="test", TYPESAFE_URL=f"http://127.0.0.1:{server.server_port}",
                        PATH="/usr/bin:/bin")
        self.hop("index")
        result = self.run_hop("search", "--deep", "where did we migrate the ledger")
        self.assertEqual((result.returncode, result.stderr.strip()),
                         (1, "Deep search failed: TypeSafe and Claude didn't answer."))
        os.utime(self.projects / "a1.jsonl", (time.time() - 600, time.time() - 600))
        labeled = self.run_hop("label")
        self.assertEqual((labeled.returncode, labeled.stderr.strip()),
                         (1, "hop label: 1 of 1 sessions couldn't be labeled: TypeSafe returned no usable status"))
        status = self.home / ".cache" / "hop" / "status.json"
        self.assertEqual(json.loads(status.read_text()) if status.exists() else {}, {})

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
        parent = (self.home / "repo").resolve()
        parent.mkdir()
        path = self.session("abc-123", user("fix the flaky checkout test", cwd=str(parent / ".claude" / "worktrees" / "gone")),
                            tool("Edit"), claude("Fixed."), title="Flaky checkout test")
        self.hop("index")
        self.assertEqual(self.hop("command", str(path)).strip(), f"cd {parent} && claude --resume abc-123")

    def test_a_codex_session_is_listed_and_resumes_with_codex(self):
        path = self.codex_session(uid(1), codex_meta(), codex_user(CODEX_ENVIRONMENT),
                                  codex_user("make payment retries safe to repeat"), codex_call(), codex_output(),
                                  codex_reply("Added idempotency keys."))
        self.assertEqual(self.titles("idempotency"), ["make payment retries safe to repeat"])
        self.assertEqual(self.hop("command", str(path)), f"cd /tmp && codex resume {uid(1)}\n")

    def test_codex_subagent_threads_are_hidden(self):
        self.codex_session(uid(1), codex_meta(), codex_user("fix the build"), codex_reply("Fixed."))
        self.codex_session(uid(2), codex_meta(parent=uid(1)), codex_user("inspect the failing test"),
                           codex_reply("Inspected."))
        self.assertEqual(self.titles(), ["fix the build"])

    def test_archived_codex_sessions_are_hidden(self):
        self.codex_session(uid(1), codex_meta(), codex_user("fix the build"), codex_reply("Fixed."), archived=True)
        self.codex_session(uid(2), codex_meta(), codex_user("fix the build again"), codex_reply("Fixed."))
        self.assertEqual(self.titles(), ["fix the build again"])

    def test_a_codex_thread_name_titles_the_session_and_a_rename_is_picked_up(self):
        self.codex_session(uid(1), codex_meta(), codex_user("fix the build"), codex_call(), codex_output(),
                           codex_reply("Fixed."))
        self.codex_session(uid(2), codex_meta(), codex_user("rename the export job"), codex_call(), codex_output(),
                           codex_reply("Renamed."))
        self.codex_name(uid(1), "Build fix draft")
        self.codex_name(uid(1), "Build fix")
        self.assertEqual(self.titles(), ["rename the export job", "Build fix"])
        self.codex_name(uid(1), "Flaky build fix")
        self.assertEqual(self.titles(), ["rename the export job", "Flaky build fix"])

    def test_a_codex_prompt_with_an_image_attachment_keeps_the_words(self):
        self.codex_session(uid(1), codex_meta(), codex_user(
            '<image name=[Image #1] path="a.png">', {"type": "input_image", "image_url": "data:image/png;base64,AA"},
            "</image>", "help me with this: [Image #1]"), codex_reply("It is a chart."))
        self.assertEqual(self.titles(), ["help me with this: [Image #1]"])

    def test_codex_machine_messages_are_not_prompts(self):
        path = self.codex_session(
            uid(1), codex_meta(), codex_user("# AGENTS.md instructions for /tmp\n\n<INSTRUCTIONS>be careful</INSTRUCTIONS>"),
            codex_user("<user_instructions>\nbe brief\n</user_instructions>"),
            codex_user("# Context from my IDE setup:\n\n## Open tabs:\n- main.go\n\n## My request for Codex:\nfix the build"),
            codex_call(), codex_output(), codex_reply("Fixed."), codex_user("now add a test for it"), codex_reply("Added."))
        self.hop("index")
        header = self.hop("preview", str(path)).splitlines()
        self.assertEqual((header[0], header[1].split(" · ")[-1]), ("fix the build", "2 prompts"))

    def test_an_old_format_codex_rollout_finds_its_folder_in_the_environment_context(self):
        def item(role, kind, text):
            return line(type="message", role=role, content=[{"type": kind, "text": text}])
        first = line(id=uid(1), timestamp="2025-08-01T10:00:00Z", instructions=None, git={"branch": "main"})
        path = self.codex_session(uid(1), first, item("user", "input_text", CODEX_ENVIRONMENT),
                                  item("user", "input_text", "fix the build"), line(type="local_shell_call", status="completed"),
                                  item("assistant", "output_text", "Fixed."), line(record_type="state"), day="2025/08/01")
        self.codex_session(uid(2), line(id=uid(2), timestamp="2025-08-01T10:00:00Z", instructions=None, git=None),
                           item("user", "input_text", "rename the export job"), item("assistant", "output_text", "Renamed."),
                           day="2025/08/01")
        self.assertEqual(self.titles(), ["fix the build"])
        self.assertEqual(self.hop("command", str(path)), f"cd /tmp && codex resume {uid(1)}\n")
        self.assertEqual(self.hop("preview", str(path)).splitlines()[1].split(" · ")[2:], ["main", "1 prompt"])

    def test_a_codex_interruption_is_reported_to_the_labeler(self):
        self.codex_session(uid(1), codex_meta(), codex_user("run the ledger migration"), codex_call(), codex_output(),
                           codex_reply("Starting the migration now."), codex_aborted())
        requests = self.record_typesafe()
        self.run_hop("label")
        state = requests[0]["state"]
        self.assertEqual((state["last_event"], [t["who"] for t in state["recent_turns"]]),
                         ("The developer interrupted Codex while it was working.", ["developer", "codex"]))

    def test_a_codex_home_other_than_the_default_is_passed_to_codex(self):
        work = self.codex_session(uid(1), codex_meta(), codex_user("fix the build"), codex_call(), codex_output(),
                                  codex_reply("Fixed."), home=".codex-work")
        self.codex_session(uid(2), codex_meta(), codex_user("rename the export job"), codex_call(), codex_output(),
                           codex_reply("Renamed."))
        self.assertEqual(self.rows(), ["rename the export job  1d ago · tmp · .codex",
                                       "fix the build  1d ago · tmp · .codex-work"])
        home = self.home.resolve()
        self.assertEqual(self.hop("command", str(work)), f"cd /tmp && CODEX_HOME={home}/.codex-work codex resume {uid(1)}\n")
        shutil.rmtree(self.home / ".codex")
        self.env["CODEX_HOME"] = str(home / ".codex-work")
        self.assertEqual(self.hop("command", str(work)), f"cd /tmp && codex resume {uid(1)}\n")

    def test_a_long_codex_session_preview_shows_its_last_request(self):
        path = self.codex_session(uid(1), codex_meta(), codex_user("load the fixtures"), codex_call(),
                                  codex_output("x" * (1 << 20) + "y" * 300_000), codex_reply("Loaded."),
                                  codex_user("now add a test for the retries"), codex_call(), codex_output(),
                                  codex_reply("Added a retry test."))
        self.hop("index")
        self.assertEqual(self.hop("preview", str(path)).splitlines()[5:],
                         ["you", "  now add a test for the retries", "", "codex", "  Added a retry test."])

    def test_codex_and_grok_sessions_report_their_last_request(self):
        self.codex_session(uid(1), codex_meta(), codex_user("inspect the invoices"), codex_call(), codex_output(),
                           codex_reply("Inspected."), codex_user("now fix the OAuth login"), codex_reply("Fixed."))
        self.grok_session(uid(2), "/tmp", grok_user("look at the ledger"), grok_reply("Looked.", tools=1),
                          grok_user("now add the retry test"), grok_reply("Added."))
        self.hop("index")
        requests = self.record_typesafe()
        self.env["PATH"] = "/usr/bin:/bin"
        self.run_hop("search", "--deep", "where did we fix the login")
        cards = [c for k, c in requests[0]["questions"]["which"]["criteria"].items() if k != "none"]
        self.assertEqual({c.split(" | ")[0]: c.split(" | ")[3] for c in cards},
                         {"inspect the invoices": "now fix the OAuth login", "look at the ledger": "now add the retry test"})

    def test_a_short_codex_session_that_ran_any_kind_of_tool_is_not_treated_as_throwaway(self):
        self.codex_session(uid(1), codex_meta(), codex_user("find the jira tool"),
                           codex_item(type="tool_search_call", call_id="c1", status="completed", arguments={"query": "jira"}),
                           codex_item(type="tool_search_output", call_id="c1", status="completed", tools=[]))
        self.codex_session(uid(2), codex_meta(), codex_user("apply the patch"),
                           codex_item(type="custom_tool_call", name="apply_patch", input="*** Begin Patch", call_id="c2"),
                           codex_item(type="custom_tool_call_output", call_id="c2", output="Done"))
        self.codex_session(uid(3), codex_meta(), codex_user("hi"), codex_reply("Hello."))
        self.assertEqual(self.titles(), ["apply the patch", "find the jira tool", "hi"])

    def test_a_codex_prompt_that_mentions_the_ide_marker_is_kept_whole(self):
        prompt = "Please explain the heading ## My request for Codex: and preserve the whole sentence"
        self.codex_session(uid(1), codex_meta(), codex_user(prompt), codex_reply("It marks the request."))
        self.assertEqual(self.titles(), [prompt])

    def test_a_codex_session_resumes_in_the_folder_of_its_latest_turn(self):
        later = self.home.resolve()
        path = self.codex_session(uid(1), codex_meta(), codex_turn("/tmp"), codex_user("fix the build"),
                                  codex_turn(str(later)), codex_user("now add a test"), codex_reply("Added."))
        self.hop("index")
        self.assertEqual(self.hop("command", str(path)), f"cd {later} && codex resume {uid(1)}\n")

    def test_a_developers_answer_to_a_codex_question_counts_as_a_prompt(self):
        path = self.codex_session(uid(1), codex_meta(), codex_user("pick a name for the app"), codex_call(),
                                  codex_output(), codex_reply("Does the name need a .com domain, and which markets?"),
                                  codex_answers("A .dev, .app, or get<name>.com is fine", "United States"))
        self.hop("index")
        self.assertEqual(self.hop("preview", str(path)).splitlines()[5:],
                         ["you", "  A .dev, .app, or get<name>.com is fine United States", "",
                          "  No reply yet. Codex stopped before answering."])
        requests = self.record_typesafe()
        self.run_hop("label")
        self.assertEqual(requests[0]["state"]["last_event"],
                         "The developer's last action was sending a request that Codex never answered.")

    def test_a_codex_branch_shows_in_the_preview(self):
        path = self.codex_session(uid(1), codex_meta(branch="retries"), codex_user("fix the build"), codex_call(),
                                  codex_output(), codex_reply("Fixed."))
        self.hop("index")
        self.assertEqual(self.hop("preview", str(path)).splitlines()[1].split(" · ")[2:], ["retries", "1 prompt"])

    def test_a_grok_session_is_listed_and_resumes_with_grok(self):
        path = self.grok_session(uid(1), "/tmp", GROK_SYSTEM, GROK_OPENER, grok_user("rate the code in this repo"),
                                 grok_reply("", tools=1), grok_result(), grok_reply("It reads well."), title="Rating the code")
        self.assertEqual(self.titles(), ["Rating the code"])
        self.assertEqual(self.hop("command", str(path)), f"cd /tmp && grok --resume {uid(1)}\n")

    def test_a_grok_home_other_than_the_default_is_passed_to_grok(self):
        work = self.grok_session(uid(1), "/tmp", grok_user("fix the build"), grok_reply("Fixed.", tools=1), home="work-grok")
        self.grok_session(uid(2), "/tmp", grok_user("rename the export job"), grok_reply("Renamed.", tools=1))
        home = self.home.resolve()
        self.env["HOP_GROK_DIRS"] = str(home / "work-grok")
        self.assertEqual(self.rows(), ["rename the export job  1d ago · tmp · .grok",
                                       "fix the build  1d ago · tmp · work-grok"])
        self.assertEqual(self.hop("command", str(work)), f"cd /tmp && GROK_HOME={home}/work-grok grok --resume {uid(1)}\n")
        shutil.rmtree(self.home / ".grok")
        self.env["GROK_HOME"] = str(home / "work-grok")
        self.assertEqual(self.hop("command", str(work)), f"cd /tmp && grok --resume {uid(1)}\n")

    def test_a_grok_title_that_arrives_after_the_chat_is_picked_up(self):
        chat = self.grok_session(uid(1), "/tmp", grok_user("rate the code in this repo"), grok_reply("It reads well."))
        self.assertEqual(self.titles(), ["rate the code in this repo"])
        self.grok_summary(chat, "/tmp", title="Rating the code")
        self.tick(chat.parent / "summary.json")
        self.assertEqual(self.titles(), ["Rating the code"])

    def test_a_grok_reply_written_after_the_summary_is_picked_up(self):
        chat = self.grok_session(uid(1), "/tmp", grok_user("rate the code in this repo"), grok_reply("It reads well."),
                                 title="Rating the code")
        self.assertEqual(self.titles("kubernetes"), [])
        with open(chat, "a") as history:
            history.write(grok_reply("Also check the kubernetes manifests.") + "\n")
        self.tick(chat)
        self.assertEqual(self.titles("kubernetes"), ["Rating the code"])
        self.assertIn("  Also check the kubernetes manifests.", self.hop("preview", str(chat)))

    def test_a_grok_session_without_a_summary_takes_its_folder_from_the_directory_name(self):
        self.grok_session(uid(1), "/work/billing", grok_user("fix the build"), grok_reply("Fixed."), summary=False)
        self.hop("index")
        self.assertEqual([row.split("\t")[1] for row in self.hop("search").splitlines()], ["/work/billing"])

    def test_a_grok_summary_folder_wins_over_the_directory_name(self):
        chat = self.grok_session(uid(1), "/tmp", grok_user("fix the build"), grok_reply("Fixed."))
        self.grok_summary(chat, "/work/billing")
        self.hop("index")
        self.assertEqual([row.split("\t")[1] for row in self.hop("search").splitlines()], ["/work/billing"])

    def test_a_grok_prompt_that_mentions_the_query_tag_is_kept_whole(self):
        self.grok_session(uid(1), "/tmp", grok_user("Fix handling of </user_query> in grok.go"), grok_reply("Fixed."))
        self.assertEqual(self.titles(), ["Fix handling of </user_query> in grok.go"])

    def test_a_grok_summary_that_cannot_be_read_is_read_again_next_time(self):
        chat = self.grok_session(uid(1), "/tmp", grok_user("fix the build"), grok_reply("Fixed."), title="Build fix")
        summary = chat.parent / "summary.json"
        good = summary.read_text()
        summary.write_text("x" + good[1:])
        self.tick(summary)
        self.assertEqual(self.run_hop("index").returncode, 0)
        self.assertEqual(self.titles(), [])
        stamp = os.stat(summary).st_mtime_ns
        summary.write_text(good)
        os.utime(summary, ns=(stamp, stamp))
        self.assertEqual(self.titles(), ["Build fix"])

    def test_grok_subagent_runs_are_hidden(self):
        self.grok_session(uid(1), "/tmp", grok_user("fix the build"), grok_reply("Fixed."), title="Build fix")
        self.grok_session(uid(2), "/tmp", grok_user("inspect the failing test"), grok_reply("Inspected."),
                          title="Failing test", kind="subagent")
        self.assertEqual(self.titles(), ["Build fix"])

    def test_grok_prompts_are_the_words_inside_the_query_tag(self):
        chat = self.grok_session(
            uid(1), "/tmp", GROK_SYSTEM, GROK_OPENER, grok_synthetic("<system-reminder>Keep working on:\n<user_query>\nadd retries to the uploader\n</user_query>\n"
                                                    "</system-reminder>"),
            grok_user("add retries to the uploader"), grok_reply("", tools=1), grok_result(),
            grok_user("The user interrupted the previous turn:\n<user_query>\nstop\n</user_query>\n"
                      "Make sure to complete any unfinished tasks from previous turns.", raw=True,
                      prior_turn_interrupt="mid_turn_abort"),
            grok_reply("Stopped."),
            grok_user("The user sent a message while you were working:\n<user_query>\nuse the request library\n</user_query>\n"
                      "Make sure to complete any unfinished tasks from previous turns.", raw=True),
            grok_reply("Switched to the request library."), title="Uploader retries")
        self.hop("index")
        preview = self.hop("preview", str(chat))
        self.assertEqual(preview.splitlines()[1].split(" · ")[-1], "3 prompts")
        self.assertIn("you\n  use the request library\n", preview)
        self.assertNotIn("Make sure", preview)

    def test_a_grok_prompt_with_no_reply_says_grok_stopped(self):
        chat = self.grok_session(uid(1), "/tmp", grok_user("deploy the ledger migration"), grok_reply("", tools=1),
                                 grok_result(), grok_reply("Deployed to staging."),
                                 grok_user("now deploy it to production please"), title="Ledger deploy")
        self.hop("index")
        self.assertEqual(self.hop("preview", str(chat)).splitlines()[-1], "  No reply yet. Grok stopped before answering.")
        requests = self.record_typesafe()
        self.run_hop("label")
        state = requests[0]["state"]
        self.assertEqual((state["last_event"], [t["who"] for t in state["recent_turns"]]),
                         ("The developer's last action was sending a request that Grok never answered.",
                          ["developer", "grok", "developer"]))

    def test_a_hostile_grok_folder_name_cannot_inject_shell_commands(self):
        folder = self.home.resolve() / "a'b;echo PWNED"
        folder.mkdir()
        chat = self.grok_session(uid(1), str(folder), grok_user("fix the build"), grok_reply("Fixed."), summary=False)
        self.hop("index")
        quoted = str(folder).replace("'", "'\"'\"'")
        self.assertEqual(self.hop("command", str(chat)), f"cd '{quoted}' && grok --resume {uid(1)}\n")

    def test_claude_label_requests_are_byte_identical_to_before(self):
        self.session("a1", user("deploy the ledger migration"), tool("Bash"), claude("Should I run it on production now?"),
                     title="Ledger migration")
        requests = self.record_typesafe()
        self.run_hop("label")
        sent = requests[0]
        self.assertEqual(sent["questions"]["status"]["instructions"],
                         "This is the end of a coding session between a developer and Claude. In what state did the session end?")
        self.assertEqual(sent["questions"]["status"]["criteria"], {
            "waiting": "Claude cannot finish the developer's current request without them: it asked a question it needs "
                       "answered, or needs their approval, credentials, or a choice before continuing.",
            "unfinished": "Work on the developer's last request stopped before it was done: Claude was mid-step, was "
                          "interrupted, hit an error or limit, or never answered.",
            "finished": "The developer's last request was completed. An optional offer of more work, such as "
                        "\"want me to also\u2026?\", does not mean Claude is waiting.",
            "unclear": "The ending does not show which of the other states applies.",
        })
        self.assertEqual((sent["state"]["last_event"], [t["who"] for t in sent["state"]["recent_turns"]]),
                         ("Claude's last action was writing a reply to the developer.", ["developer", "claude"]))

    def test_rows_name_their_agent_only_when_sessions_come_from_more_than_one(self):
        self.session("a1", user("deploy the ledger migration"), tool("Bash"), claude("Deployed."), title="Ledger migration")
        self.assertEqual(self.rows(), ["Ledger migration  1d ago · tmp"])
        self.codex_session(uid(1), codex_meta(), codex_user("fix the build"), codex_call(), codex_output(),
                           codex_reply("Fixed."))
        self.assertEqual(self.rows(), ["fix the build  1d ago · tmp · codex", "Ledger migration  1d ago · tmp · claude"])

    def test_agent_filter_keeps_only_that_agents_sessions(self):
        waiting = self.session("a1", user("deploy the ledger migration"), tool("Bash"),
                               claude("Should I run it on production now?"), title="Ledger migration")
        self.codex_session(uid(1), codex_meta(), codex_user("migrate the ledger index"), codex_call(), codex_output(),
                           codex_reply("Migrated."))
        self.grok_session(uid(2), "/tmp", grok_user("check the ledger totals"), grok_reply("Checked.", tools=1),
                          title="Ledger totals")
        self.judged(waiting, "waiting")
        every = ["Ledger totals", "migrate the ledger index", "Ledger migration"]
        self.assertEqual(self.titles("agent:codex", "ledger"), ["migrate the ledger index"])
        self.assertEqual(self.titles("agent:claude", "is:waiting"), ["Ledger migration"])
        self.assertEqual(self.titles("agent:grok"), ["Ledger totals"])
        self.assertEqual(sorted(self.titles("agent:bogus", "ledger")), sorted(every))
        self.assertEqual(sorted(self.titles("is:bogus", "ledger")), sorted(every))

    def test_the_preview_names_the_agent_that_replied(self):
        paths = {
            "claude": self.session("a1", user("deploy the ledger migration"), tool("Bash"), claude("Deployed."),
                                   title="Ledger migration"),
            "codex": self.codex_session(uid(1), codex_meta(), codex_user("fix the build"), codex_call(), codex_output(),
                                        codex_reply("Fixed.")),
            "grok": self.grok_session(uid(2), "/tmp", grok_user("check the ledger totals"), grok_reply("Checked.", tools=1),
                                      title="Ledger totals"),
        }
        self.hop("index")
        speakers = {name: self.hop("preview", str(path)).splitlines()[-2] for name, path in paths.items()}
        self.assertEqual(speakers, {"claude": "claude", "codex": "codex", "grok": "grok"})

    def test_a_preview_of_a_path_outside_every_agent_home_says_so(self):
        stray = self.home.resolve() / "notes" / "a1.jsonl"
        stray.parent.mkdir()
        stray.write_text(user("deploy the ledger migration") + "\n" + claude("Deployed.") + "\n")
        for command in ("preview", "command"):
            result = self.run_hop(command, str(stray))
            self.assertEqual((result.returncode, result.stdout, result.stderr),
                             (1, "", f"hop {command}: not a session hop reads: {stray}\n"))

    def test_an_older_cache_is_rebuilt_and_keeps_its_labels(self):
        waiting = self.session("a1", user("deploy the ledger migration"), tool("Bash"),
                               claude("Should I run it on production now?"), title="Ledger migration")
        st = os.stat(waiting)
        cache = self.home / ".cache" / "hop"
        cache.mkdir(parents=True)
        (cache / "index.json").write_text(json.dumps({"version": 11, "files": {str(waiting): {
            "mtime_ns": st.st_mtime_ns, "size": st.st_size, "session": {
                "session_id": "a1", "cwd": "/tmp", "title": "Title from an older hop", "last_prompt": "deploy",
                "branch": "main", "prompts": 1, "words": 300, "tool_calls": 1, "mtime": st.st_mtime}}}}))
        self.judged(waiting, "waiting")
        self.assertEqual(self.rows("is:waiting"), ["Ledger migration  waiting on you · 1d ago · tmp"])
        self.assertEqual(json.loads((cache / "index.json").read_text())["version"], 12)

    def test_an_older_search_index_cannot_crowd_out_new_matches(self):
        import sqlite3
        self.session("a1", user("upgrade the kubernetes cluster"), claude("Upgraded."), title="Cluster upgrade")
        cache = self.home / ".cache" / "hop"
        cache.mkdir(parents=True)
        db = sqlite3.connect(cache / "search.db")
        db.execute("CREATE VIRTUAL TABLE docs USING fts5(path UNINDEXED, title, body, tokenize='porter unicode61')")
        db.executemany("INSERT INTO docs VALUES (?, ?, ?)",
                       [(f"/gone/{n}.jsonl", "kubernetes", "kubernetes " * 20) for n in range(200)])
        db.commit()
        db.close()
        (cache / "index.json").write_text(json.dumps({"version": 11, "files": {}}))
        self.assertEqual(self.titles("kubernetes"), ["Cluster upgrade"])


if __name__ == "__main__":
    unittest.main()
