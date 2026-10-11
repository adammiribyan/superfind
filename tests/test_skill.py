import json
import os
import re
import shutil
import subprocess
import time
import unittest
from pathlib import Path

from test_blame import claude_tool, edited
from test_llms import rank_every_card
from test_superfind import (CODEX_ENVIRONMENT, SUPERFIND, SuperfindBehavior, claude, codex_meta, codex_reply, codex_user,
                            interrupted, tool, uid, user)

ROOT = Path(__file__).resolve().parent.parent
PLUGIN = ROOT / "plugins" / "superfind"
SKILL = ROOT / "skills" / "superfind" / "SKILL.md"
JQ = shutil.which("jq")


def frontmatter(text):
    head, body = re.fullmatch(r"---\n(.*?)\n---\n(.*)", text, re.S).groups()
    return dict(re.match(r"(\w+): (.+)$", row).groups() for row in head.splitlines()), body


def recipes():
    return re.findall(r"```sh\n(.*?)```", SKILL.read_text(encoding="utf-8"), re.S)


class Skill(SuperfindBehavior):
    def test_the_skill_has_the_frontmatter_every_agent_reads(self):
        fields, body = frontmatter(SKILL.read_text(encoding="utf-8"))
        self.assertEqual(set(fields), {"name", "description"})
        self.assertEqual(fields["name"], SKILL.parent.name)
        self.assertRegex(fields["name"], r"^[a-z0-9]+(-[a-z0-9]+)*$")
        self.assertLessEqual(len(fields["description"]), 1024)
        self.assertNotRegex(fields["description"], r": | #|^[\"'>|&*!%@`{\[]")
        for trigger in ("Claude Code, Codex, and Grok", "continue", "resume", "recently", "decided"):
            self.assertIn(trigger, fields["description"])
        self.assertLessEqual(len(body.splitlines()), 120)

    def test_every_marketplace_installs_the_plugin_that_holds_a_copy_of_the_skill(self):
        for vendor in (".claude-plugin", ".cursor-plugin"):
            market = json.loads((ROOT / vendor / "marketplace.json").read_text())
            plugin = json.loads((PLUGIN / vendor / "plugin.json").read_text())
            self.assertEqual([(p["name"], p["source"]) for p in market["plugins"]], [("superfind", "./plugins/superfind")])
            self.assertEqual((market["name"], plugin["name"], plugin["license"]), ("superfind", "superfind", "Apache-2.0"))
            self.assertNotIn("version", plugin)
        self.assertEqual(sorted(p.name for p in PLUGIN.rglob("*") if p.is_file()),
                         ["SKILL.md", "plugin.json", "plugin.json"])
        self.assertEqual((PLUGIN / "skills" / "superfind" / "SKILL.md").read_bytes(), SKILL.read_bytes(),
                         "plugins/superfind/skills/superfind/SKILL.md must be a copy of skills/superfind/SKILL.md")

    def test_every_superfind_command_in_the_skill_exists(self):
        listed = re.search(r"The commands are (.+)\.\n", self.run_superfind("no-such-command").stderr).group(1)
        known = set(re.split(r", and |, ", listed))
        text = SKILL.read_text(encoding="utf-8")
        code = "\n".join(re.findall(r"```sh\n(.*?)```", text, re.S) + re.findall(r"`([^`\n]+)`", text))
        used = set(re.findall(r"\bsuperfind ([a-z]+)", code)) - {"help"}
        used |= set(re.findall(r"`(search|ask|show|handoff|command|last|blame) [^`]*`", text))
        self.assertGreaterEqual(used, {"search", "ask", "show", "handoff", "command", "last", "blame", "pick"})
        self.assertLessEqual(used, known)

    def fixtures(self):
        self.scrub()
        self.clock = time.time() - 6 * 3600
        work = (self.home / "work" / "billing").resolve()
        (work / "src" / "billing").mkdir(parents=True)
        at = int(self.clock)

        def git(*args):
            env = {**self.env, "GIT_AUTHOR_NAME": "a", "GIT_AUTHOR_EMAIL": "a@example.com", "GIT_COMMITTER_NAME": "a",
                   "GIT_COMMITTER_EMAIL": "a@example.com", "GIT_AUTHOR_DATE": f"@{at + 60} +0000",
                   "GIT_COMMITTER_DATE": f"@{at + 60} +0000"}
            subprocess.run(["git", *args], cwd=work, env=env, capture_output=True, check=True)

        git("init", "-q", "-b", "main")
        (work / "src" / "billing" / "retry.go").write_text("retry once\n")
        git("add", "-A")
        git("commit", "-q", "-m", "fix: retry a webhook only once")
        self.session(uid(1), user("the webhook retry fires twice and customers get charged twice", cwd=str(work)),
                     *edited(at, "e1", work / "src" / "billing" / "retry.go"),
                     claude_tool(at + 60, "b1", "Bash", command='git commit -qm "fix: retry a webhook only once"'),
                     claude("Fixed: the retry now carries an idempotency key, so a second delivery is ignored."),
                     user("good, keep the idempotency key for 24 hours", cwd=str(work)), claude("Done."),
                     title="Webhook retry charges twice")
        self.session(uid(2), user("migrate the invoices table", cwd=str(work)), tool("Bash"), claude("Backfilling first."),
                     interrupted(), title="Invoice migration")
        self.codex_session(uid(3), codex_meta(cwd=str(work), branch="main"), codex_user(CODEX_ENVIRONMENT),
                           codex_user("add refunds to the webhook handler"), codex_reply("Refunds are half done."))
        return work

    def scrub(self):
        for name in ("BASH_ENV", "ENV", "GIT_DIR", "GIT_WORK_TREE", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT"):
            self.env.pop(name, None)
        self.env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)

    def shell(self, work):
        tools = self.home / "tools"
        tools.mkdir()
        (tools / "superfind").symlink_to(SUPERFIND.resolve())
        self.scrub()
        self.env.update(PWD=str(work), PATH=f"{tools}:{Path(JQ).parent}:/usr/bin:/bin")

    def run_recipe(self, recipe, work, env=None):
        return subprocess.run(["bash", "--noprofile", "--norc", "-c", "set -euo pipefail\n" + recipe], cwd=work,
                              env=env or self.env, capture_output=True, text=True)

    @unittest.skipUnless(JQ, "the recipes need jq")
    def test_every_recipe_in_the_skill_runs_and_finds_its_session(self):
        work = self.fixtures()
        self.shell(work)
        plain = dict(self.env)
        self.record_typesafe(answer=rank_every_card)
        ranked = dict(self.env)
        wanted = [[json.dumps({"id": uid(1), "agent": "claude", "title": "Webhook retry charges twice", "cwd": str(work),
                               "branch": "main"}, separators=(",", ":"))[:-1] + ',"updated":"20'],
                  [f"- Session: claude `{uid(1)}`", "> good, keep the idempotency key for 24 hours\n"],
                  [f"- Session: codex `{uid(3)}`", "> add refunds to the webhook handler\n"],
                  ["\tclaude\tbilling\tunfinished\tInvoice migration\n", f"\tclaude\tInvoice migration\t{self.session_path(uid(2))}\n"],
                  [f'"id":"{uid(1)}","agent":"claude","title":"Webhook retry charges twice","match":"made ',
                   ': fix: retry a webhook only once"}\n', '"match":"made this commit"}\n'],
                  [f"cd {work} && claude --resume {uid(1)}\n"],
                  [f'"id":"{uid(1)}","agent":"claude","title":"Webhook retry charges twice"']]
        found = recipes()
        self.assertEqual(len(found), len(wanted))
        for recipe, want in zip(found, wanted):
            result = self.run_recipe(recipe, work, ranked if "superfind ask" in recipe else plain)
            self.assertEqual((result.returncode, result.stderr), (0, ""), recipe)
            for part in want:
                self.assertIn(part, result.stdout, recipe)
            for row in result.stdout.splitlines():
                if row.startswith("{"):
                    json.loads(row)

    def session_path(self, session_id):
        return next(p for p in self.home.rglob(f"*{session_id}.jsonl")).resolve()

    @unittest.skipUnless(JQ, "the recipes need jq")
    def test_the_recipes_skip_sessions_another_program_started(self):
        work = self.fixtures()
        self.session(uid(9), line_from_program(str(work)), claude("idempotency webhook retry"),
                     title="Webhook retry idempotency check")
        self.shell(work)
        self.assertEqual(json.loads(self.superfind("search", "--json", "--limit", "1", "idempotency"))["kind"], "agent")
        for recipe in (recipes()[0], recipes()[1], recipes()[5]):
            result = self.run_recipe(recipe, work)
            self.assertEqual((result.returncode, result.stderr), (0, ""), recipe)
            self.assertIn(uid(1), result.stdout, recipe)
            self.assertNotIn(uid(9), result.stdout, recipe)

    @unittest.skipUnless(JQ, "the recipes need jq")
    def test_the_recipes_skip_the_session_that_runs_them(self):
        work = self.fixtures()
        self.clock = time.time() - 5
        self.session(uid(7), user("where did we fix the webhook retry? what about idempotency?", cwd=str(work)),
                     title="Webhook retry idempotency question")
        self.codex_session(uid(8), codex_meta(cwd=str(work), branch="main"), codex_user(CODEX_ENVIRONMENT),
                           codex_user("continue the refunds work"))
        self.shell(work)
        self.assertEqual(json.loads(self.superfind("search", "--json", "--limit", "1", "idempotency"))["id"], uid(7))
        for recipe, want in ((recipes()[1], uid(1)), (recipes()[2], uid(3)), (recipes()[5], uid(1))):
            result = self.run_recipe(recipe, work)
            self.assertEqual((result.returncode, result.stderr), (0, ""), recipe)
            self.assertIn(want, result.stdout, recipe)
            self.assertNotIn(uid(7), result.stdout, recipe)
            self.assertNotIn(uid(8), result.stdout, recipe)

    @unittest.skipUnless(JQ, "the recipes need jq")
    def test_a_recipe_that_finds_nothing_says_so_instead_of_misusing_superfind(self):
        work = (self.home / "work" / "empty").resolve()
        work.mkdir(parents=True)
        self.shell(work)
        for recipe in (recipes()[1], recipes()[2], recipes()[5]):
            result = self.run_recipe(recipe, work)
            self.assertNotIn(result.returncode, (0, 2), recipe)
            self.assertRegex(result.stderr, r"session: no (Codex )?session (matched|here)\n$", recipe)


def line_from_program(cwd):
    return json.dumps({"type": "user", "cwd": cwd, "gitBranch": "main", "entrypoint": "sdk-cli",
                       "message": {"role": "user", "content": "idempotency webhook retry"}})


def load_tests(loader, tests, pattern):
    own = sorted(name for name in vars(Skill) if name.startswith("test_"))
    return unittest.TestSuite(Skill(name) for name in own)


if __name__ == "__main__":
    unittest.main()
