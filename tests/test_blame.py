import json
import os
import re
import subprocess
import time
import unittest
from datetime import datetime, timezone

from test_superfind import (ANSI, GROK_SYSTEM, SuperfindBehavior, claude, codex_meta, codex_reply, codex_user, grok_user, line, uid,
                            user)


def iso(at):
    return datetime.fromtimestamp(at, timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.000Z")


def claude_tool(at, tool_id, name, **given):
    return line(type="assistant", timestamp=iso(at), message={"role": "assistant", "content": [
        {"type": "tool_use", "id": tool_id, "name": name, "input": given}]})


def claude_result(at, tool_id, text, failed=False):
    error = {"toolUseResult": "Error: " + text} if failed else {}
    return line(type="user", timestamp=iso(at), **error, message={"role": "user", "content": [
        {"type": "tool_result", "tool_use_id": tool_id, "content": text, "is_error": failed}]})


def edited(at, tool_id, path, failed=False):
    error = {"toolUseResult": "Error: String to replace not found"} if failed else {}
    result = line(type="user", timestamp=iso(at + 1), **error, message={"role": "user", "content": [
        {"type": "tool_result", "tool_use_id": tool_id, "content": "The file has been updated", "is_error": failed}]})
    return [claude_tool(at, tool_id, "Edit", file_path=str(path), old_string="a", new_string="b"), result]


def codex_at(at, kind, **payload):
    return line(timestamp=iso(at), type=kind, payload=payload)


def grok_call(name, call_id, **arguments):
    return line(type="assistant", content="", tool_calls=[{"id": call_id, "name": name, "arguments": json.dumps(arguments)}])


class Blame(SuperfindBehavior):
    def setUp(self):
        super().setUp()
        self.now = int(time.time()) - 86400
        self.work = (self.home / "work" / "billing").resolve()
        self.work.mkdir(parents=True)
        self.git("init", "-q", "-b", "main")
        self.git("remote", "add", "origin", "git@github.com:acme/billing.git")

    def git(self, *args, at=None):
        when = f"@{at or self.now} +0000"
        env = {**self.env, "GIT_AUTHOR_NAME": "a", "GIT_AUTHOR_EMAIL": "a@example.com", "GIT_COMMITTER_NAME": "a",
               "GIT_COMMITTER_EMAIL": "a@example.com", "GIT_AUTHOR_DATE": when, "GIT_COMMITTER_DATE": when}
        return subprocess.run(["git", *args], cwd=self.work, env=env, capture_output=True, text=True, check=True).stdout.strip()

    def commit(self, subject, files, at, body=""):
        for name, text in files.items():
            (self.work / name).write_text(text)
        self.git("add", "-A")
        self.git("commit", "-q", "-m", subject, *(["-m", body] if body else []), at=at)
        return self.git("rev-parse", "HEAD")

    def claude_at(self, n, *lines, title):
        return self.session(uid(n), user(title, cwd=str(self.work)), *lines, claude("Done."))

    def blame(self, *args, cwd=None):
        return subprocess.run([str(self.superfind_bin()), "blame", *args], cwd=cwd or self.work, env=self.env, capture_output=True, text=True)

    def superfind_bin(self):
        from test_superfind import SUPERFIND
        return SUPERFIND

    def found(self, *args):
        result = self.blame(*args, "--json")
        self.assertEqual(result.returncode, 0, result.stderr)
        return [(r["id"], r["match"]) for r in map(json.loads, result.stdout.splitlines())]

    def test_blame_names_the_session_that_made_a_commit_with_any_agent(self):
        t = self.now - 3600
        first = self.commit("fix: retry a charge only once", {"charge.go": "retry once\n"}, t + 60)
        second = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t + 120)
        third = self.commit("feat: keep a receipt for each charge", {"receipt.go": "receipt\n"}, t + 180)
        maker = self.claude_at(1, *edited(t, "e1", self.work / "charge.go"),
                               claude_tool(t + 60, "b1", "Bash", command='git add -A && git commit -qm "fix: retry a charge only once"'),
                               title="Retry charges once")
        self.claude_at(2, *edited(t, "e1", self.work / "charge.go"), title="Look at charges")
        self.claude_at(5, claude_tool(t + 60, "b1", "Bash", command='echo \'git commit -qm "fix: retry a charge only once"\''),
                       title="Only wrote the command down")
        self.codex_session(uid(3), codex_meta(cwd=str(self.work)), codex_user("add refunds"),
                           codex_at(t + 120, "response_item", type="function_call", name="exec_command", call_id="c1",
                                    arguments=json.dumps({"cmd": "git commit -am 'feat: refund a charge in full'"})),
                           codex_at(t + 121, "response_item", type="function_call_output", call_id="c1",
                                    output=f"Process exited with code 0\nOutput:\n[main {second[:7]}] feat: refund a charge in full"),
                           codex_reply("Committed."))
        self.grok_session(uid(4), str(self.work), GROK_SYSTEM, grok_user("keep receipts"),
                          grok_call("run_terminal_command", "g1", command="git commit -am 'feat: keep a receipt for each charge'"),
                          line(type="tool_result", tool_call_id="g1", content=f"[main {third[:7]}] feat: keep a receipt for each charge"),
                          title="Receipts")
        self.assertEqual(self.found(first), [(uid(1), "made this commit")])
        self.assertEqual(self.found(second[:9]), [(uid(3), "made this commit")])
        self.assertEqual(self.found("HEAD"), [(uid(4), "made this commit")])
        result = self.blame("HEAD~2")
        fields = result.stdout.rstrip("\n").split("\t")
        self.assertEqual((fields[:3], result.stderr), ([str(maker), str(self.work), uid(1)], ""))
        self.assertTrue(re.match(r" Retry charges once  made this commit · \S+ ago · billing · claude$", ANSI.sub("", fields[5])), fields[5])

    def test_a_session_that_saw_the_commit_hash_ranks_above_one_that_only_typed_its_subject(self):
        t = self.now - 3600
        sha = self.commit("fix: retry a charge only once", {"charge.go": "retry once\n"}, t)
        self.claude_at(1, claude_tool(t - 600, "b1", "Bash", command='git commit -qm "fix: retry a charge only once"'),
                       claude_result(t - 599, "b1", "[main 0000000] fix: retry a charge only once"), title="An earlier attempt")
        self.claude_at(2, claude_tool(t + 3000, "b1", "Bash", command='git commit -m "fix: retry a charge only once"'),
                       claude_result(t + 3001, "b1", f"[main {sha[:7]}] fix: retry a charge only once"), title="The commit")
        self.assertEqual(self.found("HEAD"), [(uid(2), "made this commit"), (uid(1), "made this commit")])

    def test_only_a_session_that_typed_the_exact_message_in_this_repository_made_the_commit(self):
        t = self.now - 3600
        self.commit("fix: retry a charge", {"charge.go": "retry\n"}, t)
        elsewhere = self.home / "other"
        elsewhere.mkdir()
        self.claude_at(1, claude_tool(t - 60, "b1", "Bash", command='git commit -qm "fix: retry a charge only once"'), title="Longer")
        self.claude_at(2, claude_tool(t - 60, "b1", "Bash", command="git commit -qm 'Revert \"fix: retry a charge\"'"), title="Revert")
        self.session(uid(3), user("start over", cwd=str(elsewhere)),
                     claude_tool(t, "b1", "Bash", command='git commit -qm "fix: retry a charge"'), claude("Done."))
        self.claude_at(4, claude_tool(t - 30, "b1", "Bash", command="git commit -qam 'fix: retry a charge' && git push"),
                       claude_result(t - 29, "b1", "[main 1f2e3d4] fix: retry a charge\nfatal: no upstream branch", failed=True),
                       title="Committed, then the push failed")
        self.claude_at(5, claude_tool(t - 10, "b1", "Bash", command='cd ' + str(self.work) + ' && git commit -m "$(cat <<\'EOF\'\nfix: retry a charge\n\nBody.\nEOF\n)"'),
                       title="Heredoc message")
        self.assertEqual(self.found("HEAD"), [(uid(5), "made this commit"), (uid(4), "made this commit")])

    def test_a_quiet_commit_in_a_failed_chain_with_a_wrapped_message_still_counts(self):
        t = self.now - 3600
        self.commit("feat: refund a charge in full now", {"refund.go": "refund\n"}, t)
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qam "feat: refund a charge\nin full now" && git push'),
                       claude_result(t + 1, "b1", "fatal: no upstream branch", failed=True), title="Wrapped")
        self.assertEqual(self.found("HEAD"), [(uid(1), "made this commit")])
        self.commit("fix: round\tthe charge rules", {"refund.go": "r\n"}, t + 60)
        self.claude_at(2, claude_tool(t + 60, "b1", "Bash", command='git commit -qam "fix: round the charge rules"'), title="Rounding")
        fields = self.blame("refund.go").stdout.splitlines()[0].split("\t")
        self.assertEqual((len(fields), fields[2]), (6, uid(2)))
        self.assertIn("made", fields[5])
        self.assertIn("round?the charge rules", fields[5])

    def test_bullets_count_only_in_a_squash_merge_and_a_new_repository_still_lists_editors(self):
        t = self.now - 3600
        (self.work / "first.go").write_text("first\n")
        self.claude_at(1, *edited(t - 60, "e1", self.work / "first.go"), title="Before any commit")
        self.assertEqual(self.found("first.go"), [(uid(1), "edited it once")])
        self.claude_at(2, claude_tool(t - 30, "b1", "Bash", command='git commit -qm "fix: retry a charge only once"'), title="Earlier")
        self.commit("chore: notes", {"notes.md": "n\n"}, t, body="* fix: retry a charge only once")
        self.assertEqual(self.found("HEAD"), [(uid(1), "edited 1 of the commit's 2 files")])

    def test_a_commit_a_codex_subagent_thread_made_names_the_session_that_started_it(self):
        t = self.now - 3600
        self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        self.codex_session(uid(1), codex_meta(cwd=str(self.work)), codex_user("start a helper"), codex_reply("Started one."))
        self.codex_session(uid(2), codex_meta(cwd=str(self.work), parent=uid(1)), codex_user("write refunds"),
                           codex_at(t, "response_item", type="function_call", name="exec_command", call_id="c1",
                                    arguments=json.dumps({"cmd": "git commit -qam 'feat: refund a charge in full'"})),
                           codex_at(t + 1, "response_item", type="function_call_output", call_id="c1", output="Process exited with code 0"))
        self.codex_session(uid(3), codex_meta(cwd=str(self.work), parent=uid(2)), codex_user("check refunds"),
                           codex_at(t + 5, "response_item", type="function_call", name="exec_command", call_id="c1",
                                    arguments=json.dumps({"cmd": "git commit -qam 'feat: refund a settled charge'"})),
                           codex_at(t + 6, "response_item", type="function_call_output", call_id="c1", output="Process exited with code 0"))
        self.assertEqual(self.found("HEAD"), [(uid(1), "made this commit")])
        self.commit("feat: refund a settled charge", {"refund.go": "settled\n"}, t + 10)
        self.assertEqual(self.found("HEAD"), [(uid(1), "made this commit")])

    def test_an_old_session_whose_codex_thread_edited_recently_is_in_the_fallback(self):
        t = self.now - 3600
        parent = self.codex_session(uid(1), codex_meta(cwd=str(self.work)), codex_user("refunds, with a helper"), codex_reply("Started one."))
        os.utime(parent, (t - 5 * 86400, t - 5 * 86400))
        self.codex_session(uid(2), codex_meta(cwd=str(self.work), parent=uid(1)), codex_user("write refunds"),
                           codex_at(t - 600, "event_msg", type="item_completed",
                                    item={"type": "FileChange", "id": "f1", "status": "completed", "changes": {str(self.work / "refund.go"): {}}}))
        self.commit("chore: by hand", {"refund.go": "refund\n"}, t)
        self.assertEqual(self.found("HEAD"), [(uid(1), "edited 1 of the commit's 1 files")])

    def test_a_long_lived_pull_request_and_a_merge_commit_still_find_their_sessions(self):
        t = self.now - 3600
        self.git("remote", "add", "upstream", "https://github.com/Acme/Billing.git")
        self.claude_at(1, claude_tool(t - 9 * 86400, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'),
                       line(type="pr-link", prRepository="Acme/Billing", prNumber=9), title="Refunds, a week ago")
        session = self.projects / f"{uid(1)}.jsonl"
        os.utime(session, (t - 9 * 86400, t - 9 * 86400))
        self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t - 9 * 86400)
        self.git("checkout", "-q", "-b", "feat")
        self.commit("fix: refund only settled charges", {"refund.go": "settled\n"}, t - 8 * 86400)
        self.git("checkout", "-q", "main")
        self.git("merge", "-q", "--no-ff", "feat", "-m", "Merge pull request #9 from acme/feat", at=t)
        self.assertEqual(self.found("HEAD"), [(uid(1), "opened #9")])
        self.assertEqual(self.found("HEAD~1"), [(uid(1), "made this commit")])

    def test_blame_finds_files_in_folders_with_odd_names_and_symlinked_paths(self):
        t = self.now - 3600
        (self.work / "src").mkdir()
        self.commit("feat: charge rules", {"src/charge rules.go": "rules\n", "a:12": "literal\n", "a": "x\n" * 20}, t)
        unresolved = self.home / "work" / "billing"
        self.claude_at(1, *edited(t + 60, "e1", unresolved / "src" / "charge rules.go"), title="Rules")
        self.claude_at(2, *edited(t + 60, "e1", self.work / "a:12"), title="The file named a:12")
        self.claude_at(3, claude_tool(t + 120, "b1", "Bash", command='git commit -qam "fix: round each charge rule"'), title="Rounding")
        self.commit("fix: round each charge rule", {"src/charge rules.go": "rounded\n"}, t + 120)
        self.assertEqual(self.found("src/charge rules.go"), [(uid(3), f"made {self.git('rev-parse', 'HEAD')[:7]}: fix: round each charge rule"),
                                                             (uid(1), "edited it once")])
        self.assertEqual(self.found(str(self.work / "src" / "charge rules.go") + ":1"), [(uid(3), "made this commit")])
        self.assertEqual(self.found("a:12"), [(uid(2), "edited it once")])
        (self.work / "LINKED.md").symlink_to("a:12")
        self.assertEqual(self.found("LINKED.md"), [])
        self.claude_at(6, *edited(t + 200, "e1", self.work / "LINKED.md"), title="Through the link")
        self.assertEqual(self.found("LINKED.md"), [(uid(6), "edited it once")])
        nested = self.blame("charge rules.go", "--json", cwd=self.work / "src")
        self.assertEqual([json.loads(r)["id"] for r in nested.stdout.splitlines()], [uid(3), uid(1)], nested.stderr)

    def test_a_squash_merge_names_the_sessions_behind_its_commits_then_the_one_that_opened_it(self):
        t = self.now - 7200
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'), title="Refunds")
        self.claude_at(2, claude_tool(t + 3000, "b1", "Bash", command='git commit -qm "fix: refund only settled charges"'),
                       title="Refund fixes")
        self.claude_at(3, line(type="pr-link", prRepository="acme/billing", prNumber=7), title="Open the refunds PR")
        self.claude_at(4, line(type="pr-link", prRepository="acme/other", prNumber=7), title="Another repository's PR 7")
        self.commit("feat: refunds (#7)", {"refund.go": "refund\n"}, t + 3600,
                    body="* feat: refund a charge in full\n\n* fix: refund only settled charges")
        self.assertEqual(self.found("HEAD"), [(uid(2), "made a commit it squashed"), (uid(1), "made a commit it squashed"), (uid(3), "opened #7")])

    def test_a_commit_no_session_made_lists_the_sessions_that_edited_its_files_before_it(self):
        t = self.now - 3600
        edit = lambda name, at: edited(at, "e" + name, str(self.work / name))
        self.claude_at(1, *edit("a.go", t), *edit("b.go", t + 10), title="Both files")
        self.claude_at(2, *edit("a.go", t + 20), title="One file")
        self.claude_at(3, *edit("a.go", t + 7200), title="Edited after the commit")
        self.claude_at(5, *edit("a.go", t - 30 * 86400), title="Edited a month before the commit")
        self.claude_at(4, *edited(t, "e1", "/elsewhere/a.go"),
                       title="Another repository")
        self.commit("chore: by hand", {"a.go": "a\n", "b.go": "b\n"}, t + 600)
        self.assertEqual(self.found("HEAD"), [(uid(1), "edited 2 of the commit's 2 files"), (uid(2), "edited 1 of the commit's 2 files")])
        self.assertEqual(self.blame("HEAD").stderr,
                         "superfind blame: no session left a record of making HEAD; these edited its files in the three days before it\n")
        self.commit("chore: nobody touched this", {"c.go": "c\n"}, t + 900)
        nobody = self.blame("HEAD")
        self.assertEqual((nobody.returncode, nobody.stdout, nobody.stderr), (0, "",
                         "superfind blame: no session on this machine made HEAD or edited its files in the three days before it\n"))

    def test_blame_on_a_file_lists_who_edited_it_or_made_its_commits_newest_first(self):
        t = self.now - 3600
        self.claude_at(1, *edited(t, "e1", str(self.work / "charge.go")), *edited(t + 5, "e2", str(self.work / "charge.go")),
                       *edited(t + 9, "e3", str(self.work / "charge.go"), failed=True), title="Edited the charge")
        self.claude_at(2, claude_tool(t + 600, "b1", "Bash", command='git commit -qam "fix: round charges to the cent"'),
                       title="Rounded the charge")
        self.claude_at(3, *edited(t + 900, "e1", str(self.work / "refund.go")),
                       title="Another file")
        sha = self.commit("fix: round charges to the cent", {"charge.go": "rounded\n", "refund.go": "r\n"}, t + 600)
        self.assertEqual(self.found("charge.go"), [(uid(2), f"made {sha[:7]}: fix: round charges to the cent"),
                                                   (uid(1), "edited it 2 times")])

    def test_blame_finds_edits_made_in_a_claude_worktree_that_was_removed_since(self):
        t = self.now - 3600
        gone = self.work / ".claude" / "worktrees" / "refunds"
        self.claude_at(1, *edited(t, "e1", str(gone / "charge.go")), *edited(t + 5, "e2", str(gone / "nested" / "charge.go")),
                       title="Edited in a worktree")
        self.claude_at(2, *edited(t + 9, "e1", str(self.home / "elsewhere" / ".claude" / "worktrees" / "x" / "charge.go")),
                       title="Another repository")
        sha = self.commit("fix: round charges to the cent", {"charge.go": "rounded\n"}, t + 600)
        self.assertFalse(gone.exists())
        self.assertEqual(self.found("charge.go"), [(uid(1), "edited it once")])
        self.assertEqual(self.found(sha[:7]), [(uid(1), "edited 1 of the commit's 1 files")])

    def test_blame_answers_from_the_index_without_reading_transcripts_again(self):
        t = self.now - 3600
        path = self.claude_at(1, *edited(t, "e1", str(self.work / "charge.go")), title="Edited the charge")
        self.commit("fix: round charges to the cent", {"charge.go": "rounded\n"}, t + 600)
        self.assertEqual(self.found("charge.go"), [(uid(1), "edited it once")])
        stat = os.stat(path)
        os.chmod(path, 0)
        try:
            self.assertEqual(self.found("charge.go"), [(uid(1), "edited it once")])
        finally:
            os.chmod(path, stat.st_mode)

    def test_an_edit_a_claude_subagent_makes_after_the_session_was_indexed_counts(self):
        t = self.now - 3600
        path = self.claude_at(1, *edited(t, "e1", str(self.work / "charge.go")), title="Edited the charge")
        part = path.parent / path.stem / "subagents" / "agent-a1.jsonl"
        part.parent.mkdir(parents=True)
        part.write_text("\n".join(edited(t + 5, "s1", str(self.work / "charge.go"))) + "\n")
        self.commit("fix: round charges to the cent", {"charge.go": "rounded\n"}, t + 600)
        self.assertEqual(self.found("charge.go"), [(uid(1), "edited it 2 times")])
        with part.open("a") as f:
            f.write("\n".join(edited(t + 9, "s2", str(self.work / "charge.go"))) + "\n")
        self.assertEqual(self.found("charge.go"), [(uid(1), "edited it 3 times")])
        part.parent.chmod(0)
        self.addCleanup(part.parent.chmod, 0o755)
        os.utime(path)
        result = self.blame("charge.go", "--json")
        self.assertEqual([(r["id"], r["match"]) for r in map(json.loads, result.stdout.splitlines())], [(uid(1), "edited it 3 times")])
        self.assertIn("couldn't be read", result.stderr)

    def test_blame_says_when_a_cache_it_cannot_write_may_miss_new_edits(self):
        t = self.now - 3600
        self.claude_at(1, *edited(t, "e1", str(self.work / "charge.go")), title="Edited the charge")
        self.commit("fix: round charges to the cent", {"charge.go": "rounded\n"}, t + 600)
        self.assertEqual(self.found("charge.go"), [(uid(1), "edited it once")])
        self.freeze_cache()
        self.claude_at(2, *edited(t + 60, "e1", str(self.work / "charge.go")), title="Edited it again")
        result = self.blame("charge.go", "--json")
        self.assertEqual(result.returncode, 0)
        self.assertRegex(result.stderr, r"^superfind blame: results may miss what changed in 1 session since ")

    def test_a_grok_session_whose_summary_is_half_written_still_counts_its_edits(self):
        t = self.now - 3600
        chat = self.grok_session(uid(1), str(self.work), GROK_SYSTEM, grok_user("round the charges"),
                                 grok_call("search_replace", "t1", file_path=str(self.work / "charge.go"), old_string="a", new_string="b"),
                                 title="Rounding")
        (chat.parent / "summary.json").write_text('{"info": {"cwd": ')
        self.commit("fix: round charges to the cent", {"charge.go": "rounded\n"}, t + 600)
        self.assertEqual(self.found("charge.go"), [(uid(1), "edited it once")])

    def test_a_commit_command_that_failed_does_not_count(self):
        t = self.now - 3600
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "fix: round charges to the cent"'),
                       claude_result(t + 1, "b1", "pre-commit hook failed", failed=True), title="Hook failed")
        self.claude_at(2, claude_tool(t + 60, "b1", "Bash", command='git commit -qm "fix: round charges to the cent"'),
                       claude_result(t + 61, "b1", "1 file changed"), title="Committed")
        sha = self.commit("fix: round charges to the cent", {"charge.go": "rounded\n"}, t + 60)
        self.assertEqual(self.found(sha[:7]), [(uid(2), "made this commit")])

    def test_blame_on_a_line_blames_the_commit_that_last_changed_it(self):
        t = self.now - 3600
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: charge in cents"'), title="Cents")
        self.claude_at(2, claude_tool(t + 600, "b1", "Bash", command='git commit -qam "feat: charge in any currency"'),
                       title="Currencies")
        self.commit("feat: charge in cents", {"charge.go": "cents\nusd\n"}, t)
        self.commit("feat: charge in any currency", {"charge.go": "cents\nany currency\n"}, t + 600)
        self.assertEqual(self.found("charge.go:1"), [(uid(1), "made this commit")])
        self.assertEqual(self.found(f"{self.work}/charge.go:2"), [(uid(2), "made this commit")])

    def test_blame_on_a_line_not_yet_committed_lists_the_sessions_that_edited_the_file(self):
        t = self.now - 3600
        self.commit("feat: charge in cents", {"charge.go": "cents\n"}, t)
        self.claude_at(1, *edited(t + 60, "e1", str(self.work / "charge.go")),
                       title="Uncommitted work")
        (self.work / "charge.go").write_text("cents\nnew line\n")
        result = self.blame("charge.go:2", "--json")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([json.loads(r)["id"] for r in result.stdout.splitlines()], [uid(1)])
        self.assertEqual(result.stderr, "superfind blame: line 2 isn't committed yet, so this lists the sessions that edited the file\n")

    def test_blame_on_a_line_runs_no_textconv_helper(self):
        marker = self.home / "ran"
        helper = self.home / "helper.sh"
        helper.write_text(f"#!/bin/sh\ntouch {marker}\ncat \"$1\"\n")
        helper.chmod(0o755)
        self.git("config", "diff.shown.textconv", str(helper))
        self.commit("feat: charge in cents", {"charge.go": "cents\n", ".gitattributes": "*.go diff=shown\n"}, self.now)
        self.claude_at(1, claude_tool(self.now, "b1", "Bash", command='git commit -qm "feat: charge in cents"'), title="Cents")
        self.assertEqual(self.found("charge.go:1"), [(uid(1), "made this commit")])
        self.assertFalse(marker.exists())

    def test_blame_says_what_it_cannot_blame(self):
        self.commit("feat: charge in cents", {"charge.go": "cents\n"}, self.now)
        (self.work / "docs").mkdir()
        cases = [((), None, 2, "superfind blame: which file or commit? See superfind help blame.\n"),
                 (("nothing-like-this",), None, 1, "superfind blame: nothing-like-this is not a file or a commit in this repository\n"),
                 (("docs",), None, 1, "superfind blame: docs is a folder; give a file, a file:line, or a commit\n"),
                 (("charge.go:9",), None, 1, "superfind blame: charge.go has 1 line, not 9\n"),
                 (("HEAD",), self.home, 1, "superfind blame: not inside a git repository; run it in one to blame a commit\n"),
                 (("HEAD", "main"), None, 2, "superfind blame: one file or commit at a time, not main too.\n"),
                 (("--all",), None, 2, "superfind blame: there is no --all option. See superfind help blame.\n")]
        for args, cwd, code, message in cases:
            result = self.blame(*args, cwd=cwd)
            self.assertEqual((result.returncode, result.stderr), (code, message), args)
        self.assertIn("FILE:LINE    blames the commit that last changed that line", self.superfind("help", "blame"))
        self.assertIn("To resume one, run the command that superfind command prints for its id.", self.superfind("help", "blame"))
        self.assertIn("superfind blame FILE|FILE:LINE|COMMIT [--json]", self.superfind("--help"))


def load_tests(loader, tests, pattern):
    own = sorted(name for name in vars(Blame) if name.startswith("test_"))
    return unittest.TestSuite(Blame(name) for name in own)


if __name__ == "__main__":
    unittest.main()
