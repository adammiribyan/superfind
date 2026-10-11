import json
import random
import shutil
import subprocess
import time
import unittest

from test_superfind import (SUPERFIND, CODEX_ENVIRONMENT, line, codex_item, GROK_OPENER, GROK_SYSTEM, SuperfindBehavior, claude, codex_meta, codex_reply,
                            codex_user, grok_reply, grok_user, tool, uid, user)


KEY = "sk-ant-api03-" + "Q" * 40
COMMON = "the the the a to of and in is it that for on with this we be as are was not at by from or".split()
TOPICS = "retry ledger invoice webhook payout cart checkout refund charge queue worker deploy migration schema cache".split()


def prose(rng, words):
    out = [rng.choice(COMMON) if rng.random() < 0.5 else rng.choice(TOPICS) for _ in range(words)]
    return " ".join(w + "." if rng.random() < 0.08 else w for w in out)


class ScriptsAndAgents(SuperfindBehavior):
    def setUp(self):
        super().setUp()
        self.env["SUPERFIND_SECONDARY"] = "\x1b[2m"

    def checkout(self):
        return self.session(uid(1), user("the checkout double-charges on slow networks", cwd="/work/billing"),
                            claude("Use an idempotency key derived from the cart id."),
                            claude("Stripe keeps it for 24 hours."),
                            user("ship it", cwd="/work/billing"), claude("Shipped."),
                            title="Checkout double-charges")

    def show(self, *args):
        result = self.run_superfind("show", *args)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout

    def test_show_prints_the_conversation_as_plain_text(self):
        self.checkout()
        out = self.show(uid(1))
        self.assertNotIn("\x1b", out)
        self.assertEqual(out, f"# Checkout double-charges\nclaude · /work/billing · {uid(1)}\n\n"
                              "## user\n\nthe checkout double-charges on slow networks\n\n"
                              "## claude\n\nUse an idempotency key derived from the cart id.\n\nStripe keeps it for 24 hours.\n\n"
                              "## user\n\nship it\n\n## claude\n\nShipped.\n")

    def test_show_json_gives_the_session_fields_and_its_turns(self):
        path = self.checkout()
        shown = json.loads(self.show(uid(1), "--json"))
        self.assertEqual({k: shown[k] for k in ("schema", "agent", "id", "path", "cwd", "title", "prompts", "status")},
                         {"schema": 1, "agent": "claude", "id": uid(1), "path": str(path), "cwd": "/work/billing",
                          "title": "Checkout double-charges", "prompts": 2, "status": ""})
        self.assertEqual(shown["resume"], self.superfind("command", str(path)).strip())
        self.assertEqual([t["role"] for t in shown["turns"]], ["user", "assistant", "user", "assistant"])
        self.assertEqual(shown["turns"][1]["text"], "Use an idempotency key derived from the cart id.\n\nStripe keeps it for 24 hours.")
        self.assertEqual((shown["total_turns"], shown["omitted_turns"], shown["truncated"]), (4, 0, False))
        self.assertNotIn("cut", json.dumps(shown["turns"]))

    def test_show_reads_codex_and_grok_sessions_too(self):
        self.codex_session(uid(2), codex_meta(cwd="/work/api"), codex_user(CODEX_ENVIRONMENT),
                           codex_user("move tokens to cookies"), codex_reply("SameSite=Lax it is."))
        self.grok_session(uid(3), "/work/web", GROK_SYSTEM, GROK_OPENER, grok_user("why is LCP slow"),
                          grok_reply("The hero image is 3 MB."), title="Landing page LCP")
        self.assertIn("## codex\n\nSameSite=Lax it is.", self.show(uid(2)))
        self.assertIn("## grok\n\nThe hero image is 3 MB.", self.show(uid(3)))

    def test_show_tail_keeps_the_last_turns_and_says_how_many_it_left_out(self):
        self.checkout()
        out = self.show(uid(1), "--tail", "2")
        self.assertIn("\n… 2 earlier turns not shown\n", out)
        self.assertNotIn("double-charges on slow", out)
        shown = json.loads(self.show(uid(1), "--tail", "1", "--json"))
        self.assertEqual(([t["text"] for t in shown["turns"]], shown["omitted_turns"], shown["truncated"]), (["Shipped."], 3, False))

    def test_show_max_bytes_keeps_the_most_recent_turns_that_fit_and_marks_the_cut(self):
        self.checkout()
        shown = json.loads(self.show(uid(1), "--max-bytes", "20", "--json"))
        self.assertEqual(([t["text"] for t in shown["turns"]], shown["omitted_turns"], shown["truncated"]),
                         (["ship it", "Shipped."], 2, True))
        self.assertIn("… 2 earlier turns not shown", self.show(uid(1), "--max-bytes", "20"))

    def test_show_caps_long_sessions_by_default_and_zero_lifts_the_cap(self):
        self.session(uid(4), user("start", cwd="/work/big"), claude("x" * 70000), user("again", cwd="/work/big"),
                     claude("done"), title="Big session")
        capped = json.loads(self.show(uid(4), "--json"))
        self.assertTrue(capped["truncated"])
        self.assertLessEqual(sum(len(t["text"].encode()) for t in capped["turns"]), 60000 + len("…".encode()))
        whole = json.loads(self.show(uid(4), "--json", "--max-bytes", "0"))
        self.assertEqual((len(whole["turns"]), whole["truncated"]), (4, False))

    def test_show_masks_keys_and_passwords_in_text_and_json(self):
        self.session(uid(5), user(f"use {KEY} for now", cwd="/work/ai"),
                     claude("Set DATABASE_URL=postgres://app:hunter2hunter2@db:5432/app and password: correcthorsebattery"),
                     title=f"Rotate {KEY}")
        text = self.show(uid(5))
        shown = json.loads(self.show(uid(5), "--json"))
        for out in (text, json.dumps(shown)):
            self.assertNotIn(KEY, out)
            self.assertNotIn("hunter2hunter2", out)
            self.assertNotIn("correcthorsebattery", out)
        self.assertEqual(shown["turns"][0]["text"], "use [masked Anthropic key] for now")
        self.assertEqual(shown["title"], "Rotate [masked Anthropic key]")
        self.assertIn("postgres://app:[masked password in a URL]@db:5432/app", text)
        self.assertIn("password: [masked secret]", text)

    def test_show_leaves_text_that_only_looks_like_a_secret(self):
        said = "we tried sk-learn, the password field, and AKIAEXAMPLE as a name; see https://example.com/a@b"
        self.session(uid(6), user(said, cwd="/work/ml"), claude("ok"), title="Plain words")
        self.assertIn(said, self.show(uid(6)))

    def test_show_sends_nothing_anywhere(self):
        self.session(uid(1), user("the checkout double-charges on slow networks", cwd="/work/billing"), tool("Edit"),
                     claude("Use an idempotency key derived from the cart id."), title="Checkout double-charges")
        requests = self.record_typesafe(answer=lambda questions: [])
        log = self.logging_claude()
        self.show(uid(1))
        self.show(uid(1), "--json")
        self.assertEqual(requests, [])
        self.assertFalse(log.exists())
        self.run_superfind("ask", "charging", "twice")
        self.assertEqual(len(requests), 1, "the recording server sees a real request")

    def test_a_unique_id_prefix_finds_the_session_in_show_command_and_preview(self):
        path = self.checkout()
        prefix = uid(1)[:8]
        self.assertIn("# Checkout double-charges", self.show(prefix))
        self.assertEqual(self.superfind("command", prefix), self.superfind("command", str(path)))
        self.assertEqual(self.superfind("preview", prefix), self.superfind("preview", str(path)))

    def test_an_ambiguous_prefix_exits_2_and_lists_every_match(self):
        first = self.session("019a3100-0000-7000-8000-000000000001", user("one", cwd="/w"), claude("ok"), title="One")
        second = self.session("019a3100-0000-7000-8000-000000000002", user("two", cwd="/w"), claude("ok"), title="Two")
        for command in ("show", "command", "preview"):
            result = self.run_superfind(command, "019a3100")
            self.assertEqual(result.returncode, 2, command)
            self.assertEqual(result.stdout, "")
            self.assertIn("019a3100 matches 2 sessions; give more of the id", result.stderr)
            self.assertIn(str(first), result.stderr)
            self.assertIn(str(second), result.stderr)
        self.assertIn("# Two", self.show("019a3100-0000-7000-8000-000000000002"))

    def test_an_id_copied_into_two_homes_asks_for_a_path(self):
        path = self.checkout()
        copy = self.home / ".claude-work" / "projects" / "-work-billing"
        copy.mkdir(parents=True)
        shutil.copy(path, copy)
        result = self.run_superfind("show", uid(1))
        self.assertEqual(result.returncode, 2)
        self.assertIn("copies of it live in more than one place; give one of these paths", result.stderr)
        self.assertIn("# Checkout double-charges", self.show(str(copy / path.name)))

    def test_a_prefix_of_an_id_copied_into_two_homes_also_asks_for_a_path(self):
        path = self.checkout()
        copy = self.home / ".claude-work" / "projects" / "-work-billing"
        copy.mkdir(parents=True)
        shutil.copy(path, copy)
        result = self.run_superfind("show", uid(1)[:8])
        self.assertEqual(result.returncode, 2)
        self.assertIn("copies of it live in more than one place", result.stderr)

    def test_an_id_in_capitals_still_finds_the_session(self):
        self.checkout()
        self.assertIn("# Checkout double-charges", self.show(uid(1).upper()))

    def test_folder_and_project_names_stay_exact_even_when_they_look_like_keys(self):
        folder = "/work/sk-proj-" + "A" * 40
        self.session(uid(8), user("set it up please", cwd=folder), claude("ok"))
        shown = json.loads(self.show(uid(8), "--json"))
        self.assertEqual((shown["cwd"], shown["project"]), (folder, "sk-proj-" + "A" * 40))

    def test_a_cut_turn_is_marked(self):
        self.session(uid(9), user("start", cwd="/w"), claude("y" * 100), title="Long reply")
        shown = json.loads(self.show(uid(9), "--json", "--max-bytes", "10"))
        self.assertEqual(shown["turns"], [{"role": "assistant", "text": "…" + "y" * 10, "cut": True}])

    def test_dots_and_tildes_alone_are_paths_not_ids(self):
        self.checkout()
        for arg in (".", "..", "~"):
            result = self.run_superfind("show", arg)
            self.assertEqual(result.returncode, 1, arg)
            self.assertIn(f"{arg} is not inside a Claude, Codex, or Grok home", result.stderr)

    def test_an_unknown_id_says_so(self):
        self.checkout()
        result = self.run_superfind("show", "deadbeef-0000")
        self.assertEqual((result.returncode, result.stderr), (1, "superfind show: no session with id deadbeef-0000\n"))
        result = self.run_superfind("command", "deadbeef")
        self.assertEqual((result.returncode, result.stderr), (1, "superfind command: no session with id deadbeef\n"))

    def test_an_id_shorter_than_eight_characters_is_a_usage_mistake(self):
        self.checkout()
        result = self.run_superfind("show", uid(1)[:7])
        self.assertEqual(result.returncode, 2)
        self.assertIn("give at least 8 characters", result.stderr)

    def test_show_still_takes_the_path_search_prints(self):
        path = self.checkout()
        self.superfind("index")
        first = self.superfind("search", "idempotency").split("\t")[0]
        self.assertEqual(first, str(path))
        self.assertEqual(self.show(first), self.show(uid(1)))

    def test_a_file_name_given_from_its_folder_is_a_path_not_an_id(self):
        path = self.checkout()
        result = subprocess.run([str(SUPERFIND), "show", path.name], env=self.env, cwd=path.parent, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("# Checkout double-charges", result.stdout)

    def test_an_id_with_dots_is_still_an_id(self):
        self.session("notes.v1", user("dotted names happen", cwd="/work/n"), claude("ok"), title="Dotted")
        self.assertIn("# Dotted", self.show("notes.v1"))
        self.assertIn("claude --resume notes.v1", self.superfind("command", "notes.v1"))

    def exec_review(self, n=20):
        meta = line(timestamp="2026-10-09T10:00:00Z", type="session_meta",
                    payload={"id": "meta", "cwd": "/work/api", "originator": "codex_exec", "source": "exec"})
        return self.codex_session(uid(n), meta, codex_user("review the idempotency change for bugs"), codex_item(type="function_call", name="shell", arguments="{}", call_id="c1"),
                                  codex_reply("No bugs in the idempotency change."))

    def peer_driven(self, n=21):
        message = line(type="user", cwd="/work/billing", isMeta=True, entrypoint="cli",
                       origin={"kind": "peer", "body": "Check the idempotency tests pass"},
                       message={"role": "user", "content": "Another Claude session sent a message:\n<cross-session-message from=\"x\">\n"
                                                           "Check the idempotency tests pass\n</cross-session-message>"})
        return self.session(uid(n), message, tool("Bash"), claude("All idempotency tests pass."))

    def records(self, *args):
        result = self.run_superfind(*args)
        self.assertEqual(result.returncode, 0, result.stderr)
        return [json.loads(row) for row in result.stdout.splitlines()]

    def test_search_json_prints_one_object_per_session(self):
        path = self.checkout()
        [row] = self.records("search", "--json", "idempotency")
        self.assertEqual({k: row[k] for k in ("schema", "agent", "id", "path", "cwd", "title", "kind", "status", "active", "folder_gone")},
                         {"schema": 1, "agent": "claude", "id": uid(1), "path": str(path), "cwd": "/work/billing",
                          "title": "Checkout double-charges", "kind": "interactive", "status": "", "active": False, "folder_gone": True})
        self.assertEqual(row["match"], "the checkout double-charges on slow networks Use an idempotency…")
        self.assertEqual(row["resume"], self.superfind("command", str(path)).strip())
        [filtered] = self.records("search", "--json", "agent:claude", "idempotency")
        self.assertEqual(filtered["match"], row["match"])
        self.assertNotIn("\x1b", self.run_superfind("search", "--json", "idempotency").stdout)

    def test_search_json_has_an_empty_match_when_there_are_no_words(self):
        self.checkout()
        self.assertEqual([r["match"] for r in self.records("search", "--json")], [""])

    def test_sessions_an_agent_started_are_hidden_unless_asked_for_but_always_in_json(self):
        self.checkout()
        self.exec_review()
        self.peer_driven()
        self.superfind("index")
        titles = lambda *q: sorted(row.split("\t")[4] for row in self.superfind("search", *q).splitlines())
        self.assertEqual(titles("idempotency"), ["Checkout double-charges"])
        self.assertEqual(titles("is:agent", "idempotency"), ["Check the idempotency tests pass", "review the idempotency change for bugs"])
        kinds = sorted((r["kind"], r["agent"]) for r in self.records("search", "--json", "idempotency"))
        self.assertEqual(kinds, [("agent", "claude"), ("agent", "codex"), ("interactive", "claude")])
        self.assertEqual([r["kind"] for r in self.records("search", "--json", "is:agent", "agent:codex")], ["agent"])

    def test_labeling_sends_only_sessions_a_person_started(self):
        self.session(uid(1), user("deploy the ledger migration please", cwd="/work/billing"), tool("Bash"),
                     claude("Should I run it on production now?"), title="Ledger migration")
        self.exec_review()
        requests = self.record_typesafe(answer=lambda questions: {"status": {"choice": "waiting", "probabilities": {"waiting": 0.95}}})
        result = self.run_superfind("label")
        self.assertEqual(result.returncode, 0, result.stderr)
        sent = json.dumps(requests)
        self.assertIn("deploy the ledger migration", sent)
        self.assertNotIn("review the idempotency change", sent)
        self.assertEqual(len(requests), 1)

    def test_a_session_driven_by_another_session_shows_what_it_was_asked(self):
        self.peer_driven()
        self.assertIn("## user\n\nCheck the idempotency tests pass\n\n## claude\n\nAll idempotency tests pass.", self.show(uid(21)))
        self.assertEqual(json.loads(self.show(uid(21), "--json"))["kind"], "agent")

    def test_ask_json_with_only_filters_prints_json(self):
        self.checkout()
        self.exec_review()
        self.assertEqual([(r["agent"], r["kind"]) for r in self.records("ask", "--json", "agent:codex")], [("codex", "agent")])
        self.assertEqual([r["kind"] for r in self.records("ask", "--json")], ["agent", "interactive"])

    def test_ask_json_prints_the_ranked_sessions(self):
        self.session(uid(1), user("make payment retries safe to repeat", cwd="/work/billing"), tool("Edit"),
                     claude("Each request now carries an idempotency key."), title="Payment retries")
        self.logging_claude(reply="[0]")
        [row] = self.records("ask", "--json", "where", "did", "we", "stop", "charging", "twice")
        self.assertEqual((row["title"], row["kind"], row["match"]), ("Payment retries", "interactive", ""))

    def test_ask_json_ranks_sessions_another_program_started_too(self):
        self.session(uid(1), user("make payment retries safe to repeat", cwd="/work/billing"), tool("Edit"),
                     claude("Each request now carries an idempotency key."), title="Payment retries")
        self.exec_review()
        log = self.logging_claude(reply="[0, 1]")
        rows = self.records("ask", "--json", "who", "checked", "the", "retry", "change")
        self.assertEqual(sorted(r["kind"] for r in rows), ["agent", "interactive"])
        self.assertEqual(len(log.read_text().splitlines()), 1)

    def test_hidden_sessions_do_not_add_an_agent_column_to_text_search(self):
        self.checkout()
        self.exec_review()
        self.superfind("index")
        [row] = self.superfind("search", "idempotency").splitlines()
        self.assertNotIn("claude", row.split("\t")[5])
        [agent_row] = self.superfind("search", "is:agent", "idempotency").splitlines()
        self.assertNotIn("codex", agent_row.split("\t")[5])

    def test_hidden_sessions_do_not_add_an_agent_tag_to_ask_or_preview(self):
        path = self.checkout()
        self.exec_review()
        [row] = self.superfind("ask").splitlines()
        self.assertNotIn("claude", row.split("\t")[5])
        self.assertNotIn("claude", self.superfind("preview", str(path)).splitlines()[1])

    def test_a_match_only_in_the_title_has_an_empty_passage(self):
        self.session(uid(1), user("rename the invoice files", cwd="/w"), tool("Bash"), claude("Renamed them."), title="Quarterly cleanup")
        self.assertEqual([r["match"] for r in self.records("search", "--json", "quarterly")], [""])
        self.assertEqual([r["match"] for r in self.records("search", "--json", "invoice")], ["rename the invoice files Renamed them."])

    def test_search_json_takes_about_as_long_as_plain_search_on_a_common_word(self):
        rng = random.Random(7)
        for n in range(40):
            self.session(uid(n), user(prose(rng, 6000), cwd="/w"), claude(prose(rng, 6000)), title=f"Ledger work {n}")
        self.superfind("index")
        fastest = {"plain": float("inf"), "json": float("inf")}
        for _ in range(3):
            for side, args in (("plain", ["the"]), ("json", ["--json", "the"])):
                start = time.perf_counter()
                out = self.superfind("search", *args)
                fastest[side] = min(fastest[side], time.perf_counter() - start)
                self.assertEqual(len(out.splitlines()), 40)
        self.assertTrue(all(r["match"] for r in self.records("search", "--json", "the")))
        self.assertLess(fastest["json"], 3 * fastest["plain"], fastest)

    def test_text_ask_leaves_out_sessions_another_program_started_unless_asked(self):
        self.checkout()
        self.exec_review()
        self.assertEqual(self.superfind("ask", "agent:codex"), "")
        self.assertEqual([r.split("\t")[2] for r in self.superfind("ask", "is:agent", "agent:codex").splitlines()], [uid(20)])

    def test_json_is_an_option_only_before_a_double_dash(self):
        self.session(uid(1), user("we pass --json to the API", cwd="/w"), claude("ok"), title="Flag talk")
        self.superfind("index")
        self.assertEqual(self.superfind("search", "--", "--json").split("\t")[4], "Flag talk")
        self.assertEqual(self.records("search", "--json", "--", "--json")[0]["title"], "Flag talk")

    def test_the_text_header_stays_one_line_whatever_the_folder_is_called(self):
        self.session(uid(7), user("hi there friend", cwd="/work/evil\n## user\x1b[31m"), claude("ok"), title="Odd\nfolder")
        out = self.show(uid(7))
        self.assertNotIn("\x1b", out)
        self.assertTrue(out.startswith(f"# Odd folder\nclaude · /work/evil?## user · {uid(7)}\n\n## user\n"), out)
        self.assertEqual(json.loads(self.show(uid(7), "--json"))["cwd"], "/work/evil\n## user\x1b[31m")

    def test_show_takes_a_session_after_a_double_dash(self):
        self.checkout()
        self.assertEqual(self.show("--json", "--", uid(1)), self.show(uid(1), "--json"))

    def test_show_rejects_bad_options_with_exit_2(self):
        self.checkout()
        for args in (["--tail", "0", uid(1)], ["--tail", "x", uid(1)], ["--max-bytes", "-1", uid(1)], ["--max-bytes"],
                     ["--wide", uid(1)], [uid(1), uid(1)], []):
            result = self.run_superfind("show", *args)
            self.assertEqual(result.returncode, 2, args)
            self.assertIn("superfind show:", result.stderr)

    def test_help_explains_show_ids_and_exit_codes(self):
        self.assertIn("--max-bytes N", self.superfind("help", "show"))
        self.assertIn("--max-bytes N", self.superfind("show", "--help"))
        usage = self.superfind("--help")
        self.assertIn("superfind show SESSION", usage)
        self.assertIn("Exit codes: 0 done, even when nothing matches; 1 failed, or the picker was cancelled;", usage)
        self.assertIn("SESSION is a session id", self.superfind("help", "command"))


def load_tests(loader, tests, pattern):
    own = sorted(name for name in vars(ScriptsAndAgents) if name.startswith("test_"))
    return unittest.TestSuite(ScriptsAndAgents(name) for name in own)


if __name__ == "__main__":
    unittest.main()
