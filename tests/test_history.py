import json
import os
import time
import unittest

from test_superfind import SuperfindBehavior, claude, tool, user

GONE = "aaaaaaaa-1111-4111-8111-111111111111"
OTHER = "bbbbbbbb-2222-4222-8222-222222222222"
NOTE = ("Claude Code deleted this session's transcript, so only your prompts are left. "
        "To keep future transcripts longer, set cleanupPeriodDays in ~/.claude/settings.json.")


class HistoryBehavior(SuperfindBehavior):
    def setUp(self):
        super().setUp()
        self.env["TZ"] = "UTC"

    def stamp(self, ago):
        return time.strftime("%Y-%m-%d %H:%M", time.gmtime(int(self.clock - ago)))

    def json_rows(self, *search):
        self.superfind("index")
        return [json.loads(row) for row in self.superfind("search", "--json", *search).splitlines()]

    def test_search_finds_a_deleted_transcript_by_its_prompts_and_marks_it(self):
        self.history(GONE, "fix the billing retry loop", "now add a test for the backoff")
        self.session("a1", user("rename the invoice PDF files", cwd="/work/billing"), claude("Renamed them."), title="Invoice names")
        self.assertEqual(self.rows("backoff"), ["fix the billing retry loop  1d ago · billing · folder removed · transcript deleted"])
        self.assertEqual(self.titles(""), ["Invoice names", "fix the billing retry loop"])

    def test_search_json_says_the_transcript_is_gone_and_offers_no_resume(self):
        path = self.history(GONE, "fix the billing retry loop", "now add a test for the backoff")
        updated = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(int(self.clock)))
        found = self.json_rows("retry")
        self.assertEqual([(r["path"], r["id"], r["cwd"], r["transcript_gone"], r["resume"], r["updated"], r["prompts"])
                          for r in found], [(str(path), GONE, "/work/billing", True, "", updated, 2)])

    def test_in_here_and_dates_filter_by_the_folder_and_the_last_prompt(self):
        self.history(GONE, "fix the billing retry loop", project="/tmp")
        self.history(OTHER, "fix the infra retry loop", project="/work/infra")
        self.env["PWD"] = "/tmp"
        self.assertEqual(self.titles("in:here", "retry"), ["fix the billing retry loop"])
        day = time.strftime("%Y-%m-%d", time.localtime(self.clock + 2 * 86400))
        self.assertEqual(self.titles("after:" + day, "retry"), [])
        self.assertEqual(self.titles("before:" + day, "in:here", "retry"), ["fix the billing retry loop"])

    def test_show_prints_the_note_and_each_prompt_with_its_time(self):
        self.history(GONE, "fix the billing retry loop", "now add a test for the backoff")
        self.superfind("index")
        self.assertEqual(self.superfind("show", GONE[:8]),
                         f"# fix the billing retry loop\nclaude · /work/billing · {GONE}\n{NOTE}\n"
                         f"\n## user · {self.stamp(60)}\n\nfix the billing retry loop\n"
                         f"\n## user · {self.stamp(0)}\n\nnow add a test for the backoff\n")

    def test_command_explains_that_claude_cant_resume_it_and_fails(self):
        self.history(GONE, "fix the billing retry loop")
        self.superfind("index")
        result = self.run_superfind("command", GONE[:8])
        self.assertEqual((result.returncode, result.stdout, result.stderr), (1, "", (
            "superfind command: Claude Code deleted this session's transcript, so Claude can't resume it. "
            f"superfind show {GONE} prints your prompts. "
            "To keep future transcripts longer, set cleanupPeriodDays in ~/.claude/settings.json.\n")))

    def test_last_skips_a_newer_session_whose_transcript_is_gone(self):
        self.session("a1", user("rename the invoice PDF files", cwd="/tmp"), tool("Edit"), claude("Renamed them."),
                     title="Invoice names")
        self.history(GONE, "fix the billing retry loop", "and the refund path", "and the receipts", project="/tmp")
        self.env["PWD"] = "/tmp"
        result = self.run_superfind("last")
        self.assertEqual((result.returncode, result.stdout), (0, "Invoice names\ncd /tmp && claude --resume a1\n"))

    def test_preview_shows_the_note_and_the_prompts_with_their_times(self):
        self.history(GONE, "fix the billing retry loop", "now add a test for the backoff")
        self.superfind("index")
        self.env["NO_COLOR"] = "1"
        self.assertEqual(self.superfind("preview", GONE[:8], "--width", "100").splitlines()[5:], [
            "  Claude Code deleted this session's transcript, so only your prompts are left. To keep future",
            "  transcripts longer, set cleanupPeriodDays in ~/.claude/settings.json.",
            "",
            f"you · {self.stamp(60)}",
            "  fix the billing retry loop",
            "",
            f"you · {self.stamp(0)}",
            "  now add a test for the backoff",
            "",
        ])

    def test_handoff_says_the_transcript_is_gone_and_quotes_the_prompts(self):
        self.history(GONE, "fix the billing retry loop", "now add a test for the backoff")
        self.superfind("index")
        out = self.superfind("handoff", GONE[:8])
        self.assertIn(f"- Resume the original: can't. {NOTE}\n", out)
        self.assertIn("## How it ended\n\nClaude Code deleted the transcript, so only the user's prompts are left. "
                      "What Claude replied or did is unknown. The last request is turn 2.\n", out)
        self.assertIn(f"### Turn 2 · user · {self.stamp(0)} · last request\n\n> now add a test for the backoff\n", out)

    def test_slash_commands_are_dropped_and_a_session_of_only_slash_commands_is_skipped(self):
        self.history(GONE, "/model", "/tmp/cache is full again", "/review the auth change", "rotate the cache")
        self.history(OTHER, "/clear", "/exit")
        self.superfind("index")
        self.assertEqual(self.titles(""), ["/tmp/cache is full again"])
        self.assertEqual(self.json_rows("")[0]["prompts"], 2)

    def test_a_prompt_with_no_session_id_is_skipped(self):
        self.history(None, "an old prompt about the ledger")
        self.history(GONE, "fix the billing retry loop")
        self.assertEqual(self.titles(""), ["fix the billing retry loop"])

    def test_pasted_text_is_searchable_and_masked_like_a_transcript(self):
        self.history(GONE, "use this key [Pasted text #1]",
                     pasted={"1": {"id": 1, "type": "text", "content": "ghp_" + "a1B2c3D4" * 5 + " for the zebrafix deploy"}})
        self.superfind("index")
        self.assertEqual(self.titles("zebrafix"), ["use this key [Pasted text #1] [masked GitHub token] for the zebrafix deploy"])
        shown = self.superfind("show", GONE[:8])
        self.assertIn("zebrafix", shown)
        self.assertNotIn("a1B2c3D4", shown)

    def test_purged_prompts_take_the_session_out_of_the_index(self):
        self.history(GONE, "fix the billing retry loop")
        self.assertEqual(self.titles("retry"), ["fix the billing retry loop"])
        (self.home / ".claude" / "history.jsonl").write_text("")
        self.assertEqual(self.titles("retry"), [])
        result = self.run_superfind("show", GONE[:8])
        self.assertEqual(result.returncode, 1)

    def test_a_transcript_that_reappears_replaces_the_prompts_only_row(self):
        self.history(GONE, "fix the billing retry loop")
        self.assertEqual(self.rows("retry"), ["fix the billing retry loop  1d ago · billing · folder removed · transcript deleted"])
        self.session(GONE, user("fix the billing retry loop", cwd="/work/billing"), tool("Edit"), claude("Fixed."))
        found = self.json_rows("retry")
        self.assertEqual([(r["path"], r["transcript_gone"], r["resume"]) for r in found],
                         [(str(self.projects.resolve() / f"{GONE}.jsonl"), False, f"cd / && claude --resume {GONE}")])

    def test_a_second_claude_home_keeps_its_own_history_and_names_its_own_settings(self):
        work = self.home / "claude-work"
        (work / "projects").mkdir(parents=True)
        self.env["CLAUDE_CONFIG_DIR"] = str(work)
        self.history(GONE, "fix the billing retry loop", home=work)
        self.history(GONE, "fix the billing retry loop")
        self.history(GONE, "and the refund path", home=work)
        found = self.json_rows("retry")
        self.assertEqual([(r["path"], r["home"], r["prompts"]) for r in found],
                         [(str(work.resolve() / "history.jsonl" / GONE), str(work.resolve()), 2)])
        self.assertIn("set cleanupPeriodDays in ~/claude-work/settings.json.", self.superfind("show", GONE[:8]))

    def test_label_never_asks_about_a_session_whose_transcript_is_gone(self):
        requests = self.record_typesafe(lambda q: {"status": {"choice": "finished", "probabilities": {"finished": 0.9}}})
        kept = self.session("a1", user("deploy the ledger migration", cwd="/work/billing"), tool("Bash"), claude("Deployed."),
                            title="Ledger migration")
        os.utime(kept, (time.time() - 600, time.time() - 600))
        self.history(GONE, "fix the billing retry loop", "and the refund path", "and the receipts")
        self.superfind("label")
        status = json.loads((self.home / ".cache" / "superfind" / "status.json").read_text())
        self.assertEqual((len(requests), list(status)), (1, [str(kept)]))


def load_tests(loader, tests, pattern):
    own = sorted(name for name in vars(HistoryBehavior) if name.startswith("test_"))
    return unittest.TestSuite(HistoryBehavior(name) for name in own)


if __name__ == "__main__":
    unittest.main()
