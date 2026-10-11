import json
import re
import subprocess
import unittest

from test_blame import Blame, claude_result, claude_tool, codex_at, grok_call
from test_superfind import GROK_SYSTEM, claude, codex_meta, codex_reply, codex_user, grok_user, line, uid, user


class Outcomes(Blame):
    def outcomes(self, n, *keys):
        result = self.run_superfind("show", uid(n), "--json")
        self.assertEqual((result.returncode, result.stderr), (0, ""))
        found = json.loads(result.stdout)["outcomes"]
        return [tuple(o[k] for k in keys) for o in found] if keys else found

    def text(self, n):
        return [l for l in self.superfind("show", uid(n)).split("\n\n")[0].splitlines()[2:]]

    def origin(self):
        bare = self.home / "origin.git"
        subprocess.run(["git", "init", "-q", "--bare", str(bare)], check=True)
        self.git("remote", "set-url", "origin", str(bare))

    def made_in(self, folder, subject, at):
        work, self.work = self.work, folder
        try:
            if not folder.exists():
                folder.mkdir(parents=True)
                self.git("init", "-q", "-b", "main")
            return self.commit(subject, {"made.txt": subject}, at)
        finally:
            self.work = work

    def test_each_agent_s_commit_says_whether_it_merged_was_pushed_or_stayed_local(self):
        t = self.now - 3600
        self.origin()
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t)
        merged = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t + 60)
        self.claude_at(1, claude_tool(t + 60, "b1", "Bash", command='git commit -qam "feat: refund a charge in full"'),
                       claude_result(t + 61, "b1", f"[main {merged[:7]}] feat: refund a charge in full"), title="Refunds")
        self.git("push", "-q", "origin", "main")
        self.git("checkout", "-q", "-b", "receipts")
        pushed = self.commit("feat: keep a receipt for each charge", {"receipt.go": "r\n"}, t + 120)
        self.git("push", "-q", "origin", "receipts")
        self.codex_session(uid(2), codex_meta(cwd=str(self.work)), codex_user("keep receipts"),
                           codex_at(t + 120, "response_item", type="function_call", name="exec_command", call_id="c1",
                                    arguments=json.dumps({"cmd": "git commit -qam 'feat: keep a receipt for each charge'"})),
                           codex_at(t + 121, "response_item", type="function_call_output", call_id="c1", output="Process exited with code 0"),
                           codex_reply("Committed."))
        self.git("checkout", "-q", "-b", "rounding")
        local = self.commit("fix: round each charge to the cent", {"round.go": "r\n"}, t + 180)
        self.grok_session(uid(3), str(self.work), GROK_SYSTEM, grok_user("round charges"),
                          grok_call("run_terminal_command", "g1", command="git commit -qam 'fix: round each charge to the cent'"),
                          line(type="tool_result", tool_call_id="g1", content="done"), title="Rounding")
        keys = ("kind", "commit", "pr", "subject", "repo", "stage", "branch", "landed", "reverted_by")
        self.assertEqual(self.outcomes(1, *keys), [("commit", merged, 0, "feat: refund a charge in full", str(self.work), "merged", "main", merged, "")])
        self.assertEqual(self.outcomes(2, *keys), [("commit", pushed, 0, "feat: keep a receipt for each charge", str(self.work), "pushed", "main", "", "")])
        self.assertEqual(self.outcomes(3, *keys), [("commit", local, 0, "fix: round each charge to the cent", str(self.work), "committed", "main", "", "")])
        self.assertEqual(self.outcomes(1)[0]["landed_at"], self.git("show", "-s", "--format=%cd", "--date=format:%Y-%m-%dT%H:%M:%SZ", merged))
        self.assertRegex(self.text(1)[0], rf"^made {merged[:7]} feat: refund a charge in full · merged into main \S+ ago$")
        self.assertEqual(self.text(2), [f"made {pushed[:7]} feat: keep a receipt for each charge · pushed, not on main"])
        self.assertEqual(self.text(3), [f"made {local[:7]} fix: round each charge to the cent · committed, not pushed"])

    def test_a_squash_merge_carries_the_session_s_commits_even_after_their_branch_is_deleted(self):
        t = self.now - 7200
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        self.git("checkout", "-q", "-b", "refunds")
        self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        kept = self.commit("fix: refund only settled charges", {"refund.go": "settled\n"}, t + 60)
        self.git("checkout", "-q", "-b", "kept")
        self.git("checkout", "-q", "main")
        self.git("branch", "-q", "-D", "refunds")
        squash = self.commit("feat: refunds (#7)", {"refund.go": "settled\n"}, t + 3600,
                             body="* feat: refund a charge in full\n\n* fix: refund only settled charges")
        self.git("branch", "-q", "-D", "kept")
        self.git("reflog", "expire", "--expire=now", "--all")
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'), title="Refunds")
        keys = ("kind", "commit", "pr", "subject", "stage", "landed")
        self.assertEqual(self.outcomes(1, *keys), [("commit", "", 7, "feat: refund a charge in full", "merged", squash)])
        self.assertRegex(self.text(1)[0], rf"^made feat: refund a charge in full · squashed into main as {squash[:7]} \(#7\) \S+ ago$")
        self.claude_at(2, claude_tool(t + 60, "b1", "Bash", command='git commit -qm "fix: refund only settled charges"'),
                       claude_result(t + 61, "b1", f"[refunds {kept[:7]}] fix: refund only settled charges"), title="Settled")
        self.assertEqual(self.outcomes(2, *keys), [("commit", kept, 7, "fix: refund only settled charges", "merged", squash)])
        self.assertRegex(self.text(2)[0], rf"^made {kept[:7]} fix: refund only settled charges · squashed into main as {squash[:7]} \(#7\) \S+ ago$")

    def test_a_commit_whose_branch_was_squashed_and_deleted_keeps_its_own_hash(self):
        t = self.now - 7200
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        self.git("checkout", "-q", "-b", "refunds")
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        self.git("checkout", "-q", "main")
        self.git("branch", "-q", "-D", "refunds")
        squash = self.commit("feat: refund a charge in full (#12)", {"refund.go": "refund\n"}, t + 600)
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'), title="Refunds")
        keys = ("commit", "pr", "stage", "landed")
        self.assertEqual(self.outcomes(1, *keys), [(made, 12, "merged", squash)])

    def test_a_commit_made_in_a_folder_since_deleted_is_found_in_the_repository_around_it(self):
        t = self.now - 3600
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        gone = self.work / ".claude" / "worktrees" / "refunds"
        self.session(uid(1), user("refunds", cwd=str(gone)),
                     claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'), claude("Done."))
        self.assertEqual(self.outcomes(1, "commit", "repo", "stage"), [(made, str(self.work), "merged")])

    def test_a_pull_request_the_session_opened_is_one_entry_merged_or_opened(self):
        t = self.now - 7200
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        self.git("checkout", "-q", "-b", "feat")
        made = self.commit("fix: refund only settled charges", {"refund.go": "settled\n"}, t)
        self.git("checkout", "-q", "main")
        self.git("merge", "-q", "--no-ff", "feat", "-m", "Merge pull request #8 from acme/feat", at=t + 600)
        merge = self.git("rev-parse", "HEAD")
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "fix: refund only settled charges"'),
                       line(type="pr-link", prRepository="acme/billing", prNumber=8),
                       line(type="pr-link", prRepository="acme/other", prNumber=11), title="Settled refunds")
        self.claude_at(2, line(type="pr-link", prRepository="Acme/Billing", prNumber=9), title="Receipts PR")
        keys = ("kind", "commit", "pr", "stage", "landed")
        self.assertEqual(self.outcomes(1, *keys), [("commit", made, 8, "merged", made)])
        self.assertRegex(self.text(1)[0], rf"^made {made[:7]} fix: refund only settled charges · merged into main via #8 \S+ ago$")
        self.assertEqual(self.outcomes(2, *keys), [("pull_request", "", 9, "opened", "")])
        self.assertEqual(self.text(2), ["opened #9 · not merged into main"])
        self.commit("feat: receipts (#9)", {"receipt.go": "r\n"}, t + 900)
        squash = self.git("rev-parse", "HEAD")
        self.assertEqual(self.outcomes(2, *keys), [("pull_request", "", 9, "merged", squash)])
        self.assertRegex(self.text(2)[0], rf"^opened #9 · merged into main as {squash[:7]} \S+ ago$")
        self.assertNotEqual(merge, squash)

    def test_without_a_default_branch_show_says_only_what_it_knows(self):
        t = self.now - 3600
        self.git("checkout", "-q", "-b", "trunk")
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'),
                       line(type="pr-link", prRepository="acme/billing", prNumber=9), title="Refunds")
        self.assertEqual(self.outcomes(1, "kind", "stage", "branch"), [("commit", "committed", ""), ("pull_request", "opened", "")])
        self.assertEqual(self.text(1), [f"made {made[:7]} feat: refund a charge in full · committed, not pushed", "opened #9"])
        self.origin()
        self.git("push", "-q", "origin", "trunk")
        self.assertEqual(self.text(1), [f"made {made[:7]} feat: refund a charge in full · pushed"])

    def test_a_merged_commit_that_main_later_reverts_says_so(self):
        t = self.now - 3600
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'), title="Refunds")
        self.git("revert", "--no-edit", "HEAD", at=t + 600)
        revert = self.git("rev-parse", "HEAD")
        self.assertEqual(self.outcomes(1, "commit", "stage", "landed", "reverted_by"), [(made, "reverted", made, revert)])
        self.assertEqual(self.text(1), [f"made {made[:7]} feat: refund a charge in full · merged into main, then reverted by {revert[:7]}"])

    def test_a_revert_counts_only_for_the_commits_its_notes_name(self):
        t = self.now - 3600
        older = self.commit("feat: refund a charge in full", {"old.go": "old\n"}, t - 600)
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        settled = self.commit("fix: refund only settled charges", {"settled.go": "settled\n"}, t + 60)
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'),
                       claude_result(t + 1, "b1", f"[main {made[:7]}] feat: refund a charge in full"),
                       claude_tool(t + 60, "b2", "Bash", command='git commit -qm "fix: refund only settled charges"'),
                       claude_result(t + 61, "b2", f"[main {settled[:7]}] fix: refund only settled charges"), title="Refunds")
        self.git("revert", "--no-edit", older, at=t + 600)
        keys = ("commit", "stage", "reverted_by")
        self.assertEqual(self.outcomes(1, *keys), [(made, "merged", ""), (settled, "merged", "")])
        both = self.commit("chore: back out the refund work", {"refund.go": "", "settled.go": ""}, t + 900,
                           body=f"This reverts commit {settled}.\n\nThis reverts commit {made}.")
        self.assertEqual(self.outcomes(1, *keys), [(made, "reverted", both), (settled, "reverted", both)])

    def test_reverting_the_revert_makes_a_commit_merged_again(self):
        t = self.now - 3600
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'), title="Refunds")
        self.git("revert", "--no-edit", "HEAD", at=t + 600)
        self.git("revert", "--no-edit", "HEAD", at=t + 900)
        self.assertEqual(self.outcomes(1, "commit", "stage", "reverted_by"), [(made, "merged", "")])
        self.git("revert", "--no-edit", "HEAD", at=t + 1200)
        again = self.git("rev-parse", "HEAD")
        self.assertEqual(self.outcomes(1, "commit", "stage", "reverted_by"), [(made, "reverted", again)])

    def test_reverting_a_pull_request_s_merge_reverts_the_commits_it_carried(self):
        t = self.now - 7200
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        self.git("checkout", "-q", "-b", "feat")
        made = self.commit("fix: refund only settled charges", {"refund.go": "settled\n"}, t)
        self.git("checkout", "-q", "main")
        self.git("merge", "-q", "--no-ff", "feat", "-m", "Merge pull request #8 from acme/feat", at=t + 600)
        merge = self.git("rev-parse", "HEAD")
        self.git("revert", "--no-edit", "-m", "1", merge, at=t + 900)
        revert = self.git("rev-parse", "HEAD")
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "fix: refund only settled charges"'),
                       line(type="pr-link", prRepository="acme/billing", prNumber=8), title="Settled refunds")
        self.assertEqual(self.outcomes(1, "kind", "commit", "pr", "stage", "reverted_by"), [("commit", made, 8, "reverted", revert)])

    def test_a_commit_found_by_its_message_is_the_one_made_near_that_time(self):
        t = self.now - 3 * 3600
        near = self.commit("fix: retry a charge only once", {"charge.go": "1\n"}, t)
        self.commit("fix: retry a charge only once", {"charge.go": "2\n"}, t + 7200)
        self.claude_at(1, claude_tool(t - 5, "b1", "Bash", command='git commit -qam "fix: retry a charge only once"'), title="Retry")
        self.assertEqual(self.outcomes(1, "commit", "stage"), [(near, "merged")])

    def test_a_short_line_in_a_commit_s_body_does_not_claim_another_commit(self):
        t = self.now - 3600
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 600)
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t - 30, body="wip")
        self.commit("wip", {"scratch.go": "s\n"}, t)
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -F - <<EOF\nfeat: refund a charge in full\n\nwip\nEOF'), title="Refunds")
        self.assertEqual(self.outcomes(1, "commit", "subject"), [(made, "feat: refund a charge in full")])

    def test_a_key_or_escape_code_in_an_outcome_is_masked_in_text_and_json(self):
        t = self.now - 3600
        key = "sk-ant-" + "a1B2c3D4e5" * 3
        made = self.commit(f"fix: stop logging {key}\x1b[31m", {"a.go": "a\n"}, t)
        self.claude_at(1, claude_tool(t, "b1", "Bash", command=f'git commit -qm "fix: stop logging {key}"'),
                       claude_result(t + 1, "b1", f"[main {made[:7]}] fix: stop logging"), title="Keys")
        result = self.run_superfind("show", uid(1), "--json")
        self.assertNotIn(key, result.stdout)
        self.assertEqual(self.outcomes(1, "subject"), [("fix: stop logging [masked Anthropic key]",)])
        self.assertRegex(self.text(1)[0], rf"^made {made[:7]} fix: stop logging \[masked Anthropic key\] · merged into main \S+ ago$")

    def test_a_signed_commit_is_read_without_running_the_signature_program(self):
        t = self.now - 3600
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        raw = self.git("cat-file", "commit", made)
        head, rest = raw.split("\n\n", 1)
        signed = f"{head}\ngpgsig -----BEGIN PGP SIGNATURE-----\n \n abc\n -----END PGP SIGNATURE-----\n\n{rest}\n"
        sha = subprocess.run(["git", "hash-object", "-t", "commit", "-w", "--stdin"], cwd=self.work, input=signed,
                             capture_output=True, text=True, check=True).stdout.strip()
        self.git("update-ref", "refs/heads/main", sha)
        marker, program = self.home / "gpg-ran", self.home / "gpg"
        program.write_text(f"#!/bin/sh\ntouch {marker}\nexit 1\n")
        program.chmod(0o755)
        self.git("config", "gpg.program", str(program))
        self.git("config", "log.showSignature", "true")
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'),
                       claude_result(t + 1, "b1", f"[main {sha[:7]}] feat: refund a charge in full"), title="Refunds")
        self.assertEqual(self.outcomes(1, "commit", "subject", "stage"), [(sha, "feat: refund a charge in full", "merged")])
        self.assertFalse(marker.exists())

    def test_of_two_copies_of_a_commit_the_one_on_main_counts(self):
        t = self.now - 3600
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        self.git("checkout", "-q", "-b", "scratch")
        self.git("commit", "-q", "--amend", "--no-edit", at=t + 900)
        self.git("checkout", "-q", "main")
        self.git("branch", "-q", "-D", "scratch")
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'), title="Refunds")
        self.assertEqual(self.outcomes(1, "commit", "stage"), [(made, "merged")])

    def test_a_commit_amended_and_then_fast_forwarded_onto_main_is_one_merged_entry(self):
        t = self.now - 3600
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        self.git("checkout", "-q", "-b", "refunds")
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        self.git("commit", "-q", "--amend", "--no-edit", at=t + 120)
        amended = self.git("rev-parse", "HEAD")
        self.git("checkout", "-q", "main")
        self.git("merge", "-q", "--ff-only", "refunds")
        self.git("branch", "-q", "-D", "refunds")
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'),
                       claude_result(t + 1, "b1", f"[refunds {made[:7]}] feat: refund a charge in full"),
                       claude_tool(t + 120, "b2", "Bash", command="git commit -q --amend --no-edit"),
                       claude_result(t + 121, "b2", f"[refunds {amended[:7]}] feat: refund a charge in full"), title="Refunds")
        self.assertEqual(self.outcomes(1, "commit", "stage", "landed"), [(amended, "merged", amended)])
        self.claude_at(2, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'),
                       claude_result(t + 1, "b1", f"[refunds {made[:7]}] feat: refund a charge in full"), title="Refunds")
        self.assertEqual(self.outcomes(2, "commit", "stage", "landed"), [(made, "merged", amended)])

    def test_a_commit_rebased_onto_main_and_merged_is_merged_as_its_copy(self):
        t = self.now - 3600
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        self.git("checkout", "-q", "-b", "refunds")
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        self.git("checkout", "-q", "main")
        self.commit("feat: keep a receipt for each charge", {"receipt.go": "r\n"}, t + 30)
        self.git("checkout", "-q", "refunds")
        self.git("rebase", "-q", "main", at=t + 600)
        rebased = self.git("rev-parse", "HEAD")
        self.git("checkout", "-q", "main")
        self.git("merge", "-q", "--ff-only", "refunds")
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git commit -qm "feat: refund a charge in full"'),
                       claude_result(t + 1, "b1", f"[refunds {made[:7]}] feat: refund a charge in full"), title="Refunds")
        self.assertEqual(self.outcomes(1, "commit", "stage", "landed"), [(made, "merged", rebased)])
        self.assertRegex(self.text(1)[0], rf"^made {made[:7]} feat: refund a charge in full · merged into main as {rebased[:7]} \S+ ago$")
        self.git("reset", "-q", "--hard", "HEAD~1")
        self.origin()
        self.git("push", "-q", "origin", "refunds")
        self.assertEqual(self.outcomes(1, "commit", "stage", "landed"), [(made, "pushed", "")])

    def test_a_commit_made_in_a_clone_since_deleted_is_found_in_the_session_s_repository(self):
        t = self.now - 3600
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t - 200)
        self.claude_at(1, claude_tool(t, "b1", "Bash", command=f'cd {self.home}/gone/clone && git commit -qm "feat: refund a charge in full" && git push'),
                       title="Refunds")
        self.assertEqual(self.outcomes(1, "commit", "stage"), [(made, "merged")])

    def test_a_commit_made_after_cd_lands_on_that_folder_s_repository(self):
        t = self.now - 3600
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        later = self.commit("fix: refund only settled charges", {"refund.go": "settled\n"}, t + 60)
        elsewhere = self.home / "elsewhere"
        elsewhere.mkdir()
        self.session(uid(1), user("refunds", cwd=str(elsewhere)),
                     claude_tool(t, "b1", "Bash", command=f'cd {self.work} && git commit -qm "feat: refund a charge in full"'),
                     claude_tool(t + 60, "b2", "Bash", command='cd work/billing && git commit -qm "fix: refund only settled charges"'),
                     claude_tool(t + 60, "b3", "Bash", command='git commit -qm "fix: refund only settled charges"'),
                     claude("Done."))
        self.assertEqual(self.outcomes(1, "commit", "repo"), [(made, str(self.work))])
        self.session(uid(2), user("refunds", cwd=str(self.home)),
                     claude_tool(t + 60, "b2", "Bash", command='cd work/billing && git commit -qm "fix: refund only settled charges"'),
                     claude("Done."))
        self.assertEqual(self.outcomes(2, "commit", "repo"), [(later, str(self.work))])

    def test_a_repository_whose_folder_name_ends_in_a_space_is_found(self):
        t = self.now - 3600
        folder = self.work.parent / "receipts "
        made = self.made_in(folder, "feat: keep a receipt for each charge", t)
        self.session(uid(1), user("receipts", cwd=str(folder)),
                     claude_tool(t, "b1", "Bash", command='git commit -qm "feat: keep a receipt for each charge"'), claude("Done."))
        self.assertEqual(self.outcomes(1, "commit", "repo"), [(made, str(folder))])

    def test_git_c_moves_a_commit_only_when_it_belongs_to_the_commit_call(self):
        t = self.now - 3600
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, t)
        receipts = self.work.parent / "receipts"
        kept = self.made_in(receipts, "feat: keep a receipt for each charge", t + 60)
        self.claude_at(1, claude_tool(t, "b1", "Bash", command='git -C ../receipts status && git commit -qm "feat: refund a charge in full"'),
                       claude_tool(t + 60, "b2", "Bash", command='git -C ../receipts commit -qm "feat: keep a receipt for each charge"'),
                       title="Refunds")
        self.assertEqual(self.outcomes(1, "commit", "repo"), [(made, str(self.work)), (kept, str(receipts))])

    def test_a_codex_commit_lands_on_the_repository_of_the_folder_it_ran_in(self):
        t = self.now - 3600
        self.commit("chore: start the billing service", {"a.go": "a\n"}, t - 60)
        receipts = self.work.parent / "receipts"
        kept = self.made_in(receipts, "feat: keep a receipt for each charge", t)
        self.codex_session(uid(1), codex_meta(cwd=str(self.work)), codex_user("keep receipts"),
                           codex_at(t, "response_item", type="function_call", name="exec_command", call_id="c1",
                                    arguments=json.dumps({"cmd": "git commit -qam 'feat: keep a receipt for each charge'", "workdir": str(receipts)})),
                           codex_at(t + 1, "response_item", type="function_call_output", call_id="c1", output="Process exited with code 0"),
                           codex_reply("Committed."))
        self.assertEqual(self.outcomes(1, "commit", "repo"), [(kept, str(receipts))])

    def test_an_old_commit_named_by_its_hash_is_measured_against_main(self):
        old = self.now - 60 * 86400
        self.commit("chore: start the billing service", {"a.go": "a\n"}, old - 60)
        made = self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, old)
        self.commit("feat: keep a receipt for each charge", {"receipt.go": "r\n"}, self.now)
        self.grok_session(uid(1), str(self.work), GROK_SYSTEM, grok_user("refunds"),
                          grok_call("run_terminal_command", "g1", command="git commit -qam 'feat: refund a charge in full'"),
                          line(type="tool_result", tool_call_id="g1", content=f"[main {made[:7]}] feat: refund a charge in full"), title="Refunds")
        self.assertEqual(self.outcomes(1, "commit", "stage", "landed"), [(made, "merged", made)])

    def test_a_session_with_no_commits_shows_as_before_with_an_empty_list(self):
        self.commit("feat: refund a charge in full", {"refund.go": "refund\n"}, self.now)
        self.claude_at(1, title="Just talk")
        self.assertEqual(self.outcomes(1), [])
        self.assertEqual(self.superfind("show", uid(1)), f"# Just talk\nclaude · {self.work} · {uid(1)}\n\n## user\n\nJust talk\n\n## claude\n\nDone.\n")
        shown = json.loads(self.run_superfind("show", uid(1), "--json").stdout)
        self.assertEqual(list(shown)[list(shown).index("outcomes") + 1], "total_turns")


def load_tests(loader, tests, pattern):
    own = sorted(name for name in vars(Outcomes) if name.startswith("test_"))
    return unittest.TestSuite(Outcomes(name) for name in own)


if __name__ == "__main__":
    unittest.main()
