import os
import pty
import subprocess
import unittest

from test_superfind import SUPERFIND, SuperfindBehavior, claude, uid, user

U1, U2 = uid(1), uid(2)
CHECKOUT = f"Checkout double-charges\ncd /tmp && claude --resume {U1}\n"
INVOICE = f"Invoice file names\ncd /tmp && claude --resume {U2}\n"


class Resume(SuperfindBehavior):
    def two_sessions(self):
        checkout = self.session(U1, user("the checkout double-charges on slow networks"),
                                claude("Use an idempotency key derived from the cart id."), title="Checkout double-charges")
        self.session(U2, user("rename the invoice PDF files"), claude("Renamed them."), title="Invoice file names")
        return checkout

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
