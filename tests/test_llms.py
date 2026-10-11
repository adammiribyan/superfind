import json
import os
import re
import subprocess
import sys
import unittest
from pathlib import Path

from test_superfind import SUPERFIND, SuperfindBehavior, claude, tool, uid, user

ROOT = Path(__file__).resolve().parent.parent


def rank_every_card(questions):
    if "which" in questions:
        return {"which": {"probabilities": {k: 0.9 for k in questions["which"]["criteria"] if k != "none"}}}
    return {k: {"noul": 0.9} for k in questions}


class LlmsFiles(SuperfindBehavior):
    def test_llms_full_txt_matches_what_the_generator_writes(self):
        result = subprocess.run([sys.executable, str(ROOT / "scripts" / "llms.py"), "--check"],
                                env={**os.environ, "SUPERFIND_BIN": str(SUPERFIND)}, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)

    def test_every_superfind_command_in_llms_txt_runs(self):
        self.session(uid(1), user("the checkout button double-charges on slow networks", cwd="/work/storefront"),
                     tool("Grep"), claude("Each request now carries an idempotency key derived from the cart id."),
                     title="Checkout double-charges")
        self.record_typesafe(answer=rank_every_card)
        examples = re.findall(r"`(superfind (?:search|ask|show|command)[^`]*)`", (ROOT / "llms.txt").read_text())
        self.assertGreaterEqual(len(examples), 4, examples)
        for example in examples:
            args = example.split()[1:]
            args = [{"WORDS": "idempotency", "ID": uid(1)}.get(a, a) for a in args]
            result = self.run_superfind(*args)
            self.assertEqual(result.returncode, 0, f"{example}: {result.stderr}")
            if "--json" in args:
                parsed = [json.loads(line) for line in result.stdout.splitlines()]
                self.assertEqual(parsed[0]["id"], uid(1), example)
            else:
                self.assertIn(uid(1), result.stdout, example)

    def test_the_shell_setup_and_exit_codes_in_llms_txt_are_true(self):
        text = (ROOT / "llms.txt").read_text(encoding="utf-8")
        shells = re.findall(r'`eval "\$\(superfind init (\w+)\)"`', text)
        self.assertEqual(shells, ["zsh", "bash"])
        for shell in shells:
            self.assertIn("superfind", self.superfind("init", shell))
        self.assertIn("Exit codes: 0 done, even when nothing matches; 1 failed; 2 a usage mistake or an ambiguous id", text)
        self.session(uid(1), user("fix the flaky test please", cwd="/w"), claude("Fixed."), title="Flaky")
        self.assertEqual(self.run_superfind("search", "nothing-matches-this").returncode, 0)
        self.assertEqual(self.run_superfind("show", "deadbeef-0000").returncode, 1)
        self.assertEqual(self.run_superfind("show", "--wide", uid(1)).returncode, 2)

    def test_llms_txt_follows_the_llms_txt_layout(self):
        text = (ROOT / "llms.txt").read_text()
        lines = text.splitlines()
        self.assertEqual(lines[0], "# superfind")
        self.assertTrue(lines[2].startswith("> "), lines[2])
        self.assertEqual(sum(line.startswith("# ") for line in lines), 1)
        sections = re.findall(r"^## (.+)$", text, re.M)
        self.assertEqual(sections[-1], "Optional")
        for section in re.split(r"^## .+$", text, flags=re.M)[1:]:
            for line in section.strip().splitlines():
                self.assertRegex(line, r"^- \[[^\]]+\]\(https://[^)]+\)(: .+)?$")

    def test_llms_txt_names_every_command_scripts_use(self):
        text = (ROOT / "llms.txt").read_text()
        for command in ("search", "ask", "show", "command"):
            self.assertIn(f"`superfind {command} ", text)
        self.assertIn("JSON Lines", text)
        self.assertIn("Exit codes: 0", text)


def load_tests(loader, tests, pattern):
    own = sorted(name for name in vars(LlmsFiles) if name.startswith("test_"))
    return unittest.TestSuite(LlmsFiles(name) for name in own)


if __name__ == "__main__":
    unittest.main()
