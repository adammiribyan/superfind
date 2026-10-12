import json
import os
import pty
import subprocess
import time
import unittest

from test_superfind import SUPERFIND, SuperfindBehavior, claude, tool, uid, user

U1, U2 = uid(1), uid(2)
CHECKOUT = f"Checkout double-charges\ncd /tmp && claude --resume {U1}\n"
INVOICE = f"Invoice file names\ncd /tmp && claude --resume {U2}\n"


class Resume(SuperfindBehavior):
    def two_sessions(self):
        checkout = self.session(U1, user("the checkout double-charges on slow networks"), tool("Edit"),
                                claude("Use an idempotency key derived from the cart id."), title="Checkout double-charges")
        self.session(U2, user("rename the invoice PDF files"), tool("Edit"), claude("Renamed them."), title="Invoice file names")
        return checkout

    def resume(self, *args):
        result = subprocess.run([str(SUPERFIND), "resume", *args], env=self.env, stdin=subprocess.DEVNULL, capture_output=True,
                                text=True)
        return result.returncode, result.stdout, result.stderr

    def without_claude(self):
        self.env["PATH"] = "/usr/bin:/bin"

    def marking_claude(self):
        stub = self.home / "stub"
        stub.mkdir()
        (stub / "claude").write_text(f"#!/bin/sh\ntouch {self.home / 'ran'}\n")
        (stub / "claude").chmod(0o755)
        self.env["PATH"] = f"{stub}:/usr/bin:/bin"

    def test_an_id_prints_the_title_and_the_command_that_resumes_it(self):
        self.two_sessions()
        self.assertEqual(self.resume(U1), (0, CHECKOUT, ""))

    def test_a_unique_id_prefix_prints_the_same(self):
        self.two_sessions()
        self.assertEqual(self.resume(U1[:8]), (0, CHECKOUT, ""))

    def test_a_transcript_path_prints_the_same(self):
        path = self.two_sessions()
        self.assertEqual(self.resume(str(path)), (0, CHECKOUT, ""))

    def test_pick_is_another_name_for_resume(self):
        self.two_sessions()
        result = subprocess.run([str(SUPERFIND), "pick", U1], env=self.env, stdin=subprocess.DEVNULL, capture_output=True, text=True)
        self.assertEqual((result.returncode, result.stdout, result.stderr), (0, CHECKOUT, ""))

    def test_an_unknown_id_is_an_error_and_never_a_search(self):
        self.two_sessions()
        self.marking_claude()
        self.assertEqual(self.resume("deadbeef00"), (1, "", "superfind resume: no session with id deadbeef00\n"))
        self.assertFalse((self.home / "ran").exists())

    def test_an_ambiguous_id_prefix_lists_the_sessions_it_matches(self):
        for n in (1, 2):
            self.session(f"019a3100-0000-7000-8000-00000000000{n}", user("start"), tool("Edit"), claude("done"), title=f"Twin {n}")
        code, out, err = self.resume("019a3100")
        self.assertEqual((code, out), (2, ""))
        self.assertTrue(err.startswith("superfind resume: 019a3100 matches 2 sessions; give more of the id, or one of these paths:\n"), err)

    def test_a_word_that_is_a_whole_session_id_resumes_that_session(self):
        self.session("notes.v1", user("dotted names happen"), tool("Edit"), claude("ok"), title="Dotted")
        self.assertEqual(self.resume("notes.v1"), (0, "Dotted\ncd /tmp && claude --resume notes.v1\n", ""))

    def test_words_print_the_session_ask_ranks_first(self):
        self.two_sessions()
        log = self.logging_claude(reply="[1]")
        self.assertEqual(self.resume("the", "slow", "network", "bug"), (0, CHECKOUT, ""))
        self.assertEqual(len(log.read_text().splitlines()), 1)

    def test_words_fall_back_to_the_best_keyword_match_when_ask_cant_run(self):
        self.two_sessions()
        self.without_claude()
        self.assertEqual(self.resume("invoice", "PDF"), (0, INVOICE, "superfind resume: Ask failed: Claude Code didn't answer. "
                                                                     "Taking the best keyword match instead.\n"))

    def test_words_that_fit_no_session_exit_1(self):
        self.two_sessions()
        self.without_claude()
        self.assertEqual(self.resume("zebra", "giraffe"), (1, "", "superfind resume: Ask failed: Claude Code didn't answer. "
                                                                   "Taking the best keyword match instead.\n"
                                                                   "superfind resume: no session fits zebra giraffe\n"))

    def test_filters_alone_print_the_newest_session_they_fit(self):
        self.two_sessions()
        self.marking_claude()
        self.assertEqual(self.resume("agent:claude"), (0, INVOICE, ""))
        self.assertFalse((self.home / "ran").exists())

    def test_a_bad_filter_is_a_usage_mistake_as_in_ask(self):
        self.two_sessions()
        self.assertEqual(self.resume("after:3days"), (2, "", "superfind resume: can't read after:3days. "
                                                          "after: takes a date like 2026-10-09, yesterday, or 3d.\n"))

    def test_an_option_resume_does_not_have_is_refused(self):
        self.assertEqual(self.resume("--json"), (2, "", "superfind resume: there is no --json option. See superfind help resume.\n"))

    def test_a_session_from_a_pipe_is_printed_and_never_run(self):
        self.two_sessions()
        self.marking_claude()
        result = self.piped(f"{U1}\n", "resume", "-")
        self.assertEqual((result.returncode, result.stdout, result.stderr), (0, CHECKOUT, ""))
        self.assertFalse((self.home / "ran").exists())

    def test_a_session_open_in_another_window_is_named_instead_of_printed(self):
        self.two_sessions()
        sessions = self.home / ".claude" / "sessions"
        sessions.mkdir()
        (sessions / f"{os.getpid()}.json").write_text(json.dumps({"pid": os.getpid(), "sessionId": U1, "cwd": "/tmp",
                                                                 "startedAt": int(time.time() * 1000), "status": "idle"}))
        self.assertEqual(self.resume(U1), (1, "", "superfind resume: Checkout double-charges is already open in another window. "
                                                  "superfind resume doesn't start a second copy.\n"))

    def piped(self, text, *args):
        return subprocess.run([str(SUPERFIND), *args], env=self.env, input=text, capture_output=True, text=True)

    def test_dash_reads_a_bare_id_from_a_pipe(self):
        self.two_sessions()
        self.assertEqual(self.piped(f"{U1}\n", "show", "-").stdout, self.superfind("show", U1))

    def test_dash_reads_a_quoted_id_from_a_pipe(self):
        self.two_sessions()
        result = self.piped(f'"{U1}"\n', "command", "-")
        self.assertEqual((result.returncode, result.stdout, result.stderr), (0, f"cd /tmp && claude --resume {U1}\n", ""))

    def test_dash_takes_the_first_session_search_prints(self):
        self.two_sessions()
        self.superfind("index")
        found = self.superfind("search", "the")
        self.assertEqual(len(found.splitlines()), 2)
        result = self.piped(found, "command", "-")
        self.assertEqual((result.returncode, result.stdout), (0, f"cd /tmp && claude --resume {found.split(chr(9))[2]}\n"))

    def test_dash_takes_a_json_line_and_never_runs_its_resume_field(self):
        self.two_sessions()
        self.superfind("index")
        record = self.superfind("search", "--json", "--limit", "1", "idempotency")
        hostile = record.replace(f'"resume":"cd /tmp && claude --resume {U1}"', '"resume":"touch PWNED"')
        self.assertNotEqual(hostile, record)
        result = self.piped(hostile, "handoff", "-")
        self.assertEqual((result.returncode, result.stdout), (0, self.run_superfind("handoff", U1).stdout))
        self.assertFalse((self.home / "PWNED").exists())

    def test_dash_takes_a_json_line_with_only_an_id(self):
        self.two_sessions()
        result = self.piped(f'{{"id":"{U2}"}}\n', "preview", "-")
        self.assertEqual((result.returncode, result.stdout), (0, self.run_superfind("preview", U2).stdout))

    def test_dash_reads_the_whole_pipe_so_a_long_list_upstream_still_succeeds(self):
        self.two_sessions()
        script = f"set -o pipefail; {{ echo {U1}; awk 'BEGIN {{ for (i = 0; i < 100000; i++) print \"{U2}\" }}'; }} | {SUPERFIND} command -"
        result = subprocess.run(["bash", "--noprofile", "--norc", "-c", script], env=self.env, capture_output=True, text=True)
        self.assertEqual((result.returncode, result.stdout, result.stderr), (0, f"cd /tmp && claude --resume {U1}\n", ""))

    def test_dash_with_nothing_piped_says_no_session_came_in(self):
        self.two_sessions()
        result = self.piped("", "show", "-")
        self.assertEqual((result.returncode, result.stdout, result.stderr), (1, "", "superfind show: no session came in on standard input\n"))

    def test_dash_with_a_json_line_that_names_no_session_is_a_usage_mistake(self):
        self.two_sessions()
        result = self.piped('{"title":"Checkout double-charges"}\n', "command", "-")
        self.assertEqual((result.returncode, result.stderr), (2, "superfind command: the JSON line on standard input has no path or id\n"))

    def test_dash_with_an_unknown_id_piped_says_so(self):
        self.two_sessions()
        result = self.piped("deadbeef-0000\n", "show", "-")
        self.assertEqual((result.returncode, result.stderr), (1, "superfind show: no session with id deadbeef-0000\n"))

    def test_dash_with_the_keyboard_on_standard_input_refuses_to_wait_for_it(self):
        self.two_sessions()
        keyboard, terminal = pty.openpty()
        self.addCleanup(os.close, keyboard)
        try:
            result = subprocess.run([str(SUPERFIND), "handoff", "-"], env=self.env, stdin=terminal, capture_output=True, text=True,
                                    timeout=10)
        finally:
            os.close(terminal)
        self.assertEqual((result.returncode, result.stdout, result.stderr),
                         (2, "", "superfind handoff: - reads a session from a pipe, such as superfind search --json WORDS | superfind handoff -\n"))


def load_tests(loader, tests, pattern):
    own = sorted(name for name in vars(Resume) if name.startswith("test_"))
    return unittest.TestSuite(Resume(name) for name in own)


if __name__ == "__main__":
    unittest.main()
