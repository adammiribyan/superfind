package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func trailOf(t *testing.T, a *Agent, path string, lines ...string) Trail {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := ReadTrail(a, path)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

const commitCommand = `git add -A && git commit -qm "fix: retry a charge once"`

func TestAClaudeTrailHasItsEditsCommitsAndPullRequests(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "s1.jsonl")
	trailOf(t, Named("claude"), filepath.Join(dir, "s1", "subagents", "agent-a.jsonl"),
		`{"type":"assistant","timestamp":"2026-10-09T10:05:00Z","message":{"content":[{"type":"tool_use","id":"w1","name":"Write","input":{"file_path":"/w/b.go","content":"x"}}]}}`,
		`{"type":"user","timestamp":"2026-10-09T10:05:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"w1","content":"File created"}]}}`)
	tr := trailOf(t, Named("claude"), parent,
		`{"type":"assistant","timestamp":"2026-10-09T10:00:00Z","message":{"content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":"/w/a.go","old_string":"a","new_string":"b"}}]}}`,
		`{"type":"user","timestamp":"2026-10-09T10:00:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"e1","content":"The file has been updated"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-09T10:00:30Z","message":{"content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":"/w/c.go","old_string":"a","new_string":"b"}}]}}`,
		`{"type":"user","timestamp":"2026-10-09T10:00:31Z","toolUseResult":"Error: String to replace not found","message":{"content":[{"type":"tool_result","tool_use_id":"e2","is_error":true,"content":"String to replace not found"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-09T10:00:40Z","message":{"content":[{"type":"tool_use","id":"r1","name":"Read","input":{"file_path":"/w/README.md"}}]}}`,
		`{"type":"user","timestamp":"2026-10-09T10:00:41Z","message":{"content":[{"type":"tool_result","tool_use_id":"r1","content":"# Billing"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-09T10:00:50Z","message":{"content":[{"type":"tool_use","id":"r2","name":"Read","input":{"file_path":"/w/missing.md"}}]}}`,
		`{"type":"user","timestamp":"2026-10-09T10:00:51Z","message":{"content":[{"type":"tool_result","tool_use_id":"r2","is_error":true,"content":"File does not exist."}]}}`,
		`{"type":"assistant","timestamp":"2026-10-09T10:01:00Z","message":{"content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"assistant","timestamp":"2026-10-09T10:02:00Z","message":{"content":[{"type":"tool_use","id":"b2","name":"Bash","input":{"command":"`+strings.ReplaceAll(commitCommand, `"`, `\"`)+`"}}]}}`,
		`{"type":"user","timestamp":"2026-10-09T10:02:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"b2","content":"[main 1a2b3c4d] fix: retry a charge once"}]}}`,
		`{"type":"pr-link","prRepository":"acme/billing","prNumber":7}`)
	want := Trail{
		Edits:   []Edit{{"/w/a.go", 1791540000, false}, {"/w/c.go", 1791540030, true}, {"/w/b.go", 1791540300, false}},
		Reads:   []Edit{{"/w/README.md", 1791540040, false}},
		Commits: []Commit{{commitCommand, "1a2b3c4d", 1791540120, ""}},
		PRs:     []PR{{"acme/billing", 7}},
	}
	if !reflect.DeepEqual(tr, want) {
		t.Fatalf("got %+v\nwant %+v", tr, want)
	}
}

func TestAClaudeTrailTakesTheShaFromGitOperationWhenTheCommitWasQuiet(t *testing.T) {
	tr := trailOf(t, Named("claude"), filepath.Join(t.TempDir(), "s.jsonl"),
		`{"type":"assistant","timestamp":"2026-10-09T10:02:00Z","message":{"content":[{"type":"tool_use","id":"b2","name":"Bash","input":{"command":"git commit -qm wip"}}]}}`,
		`{"type":"user","timestamp":"2026-10-09T10:02:01Z","toolUseResult":{"stdout":"","gitOperation":{"commit":{"sha":"9f8e7d6c","kind":"committed"}}},"message":{"content":[{"type":"tool_result","tool_use_id":"b2","content":""}]}}`)
	if want := []Commit{{"git commit -qm wip", "9f8e7d6c", 1791540120, ""}}; !reflect.DeepEqual(tr.Commits, want) {
		t.Fatalf("got %+v", tr.Commits)
	}
}

func TestACodexTrailReadsFileChangesPatchesAndCommandsWithTheirOutput(t *testing.T) {
	tr := trailOf(t, Named("codex"), filepath.Join(t.TempDir(), "rollout-2026-10-09T10-00-00-"+strings.Repeat("a", 36)+".jsonl"),
		`{"timestamp":"2026-10-09T10:00:00Z","type":"session_meta","payload":{"cwd":"/w"}}`,
		`{"timestamp":"2026-10-09T10:01:00Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"p1","input":"*** Begin Patch\n*** Update File: src/a.ts\n@@\n-a\n+b\n*** Delete File: src/old.ts\n*** End Patch"}}`,
		`{"timestamp":"2026-10-09T10:01:01Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"p1","output":"Success. Updated the following files:\nM src/a.ts\nD src/old.ts"}}`,
		`{"timestamp":"2026-10-09T10:01:30Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"p2","input":"*** Begin Patch\n*** Update File: src/c.ts\n*** End Patch"}}`,
		`{"timestamp":"2026-10-09T10:01:31Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"p2","output":"apply_patch verification failed: Failed to find expected lines in src/c.ts"}}`,
		`{"timestamp":"2026-10-09T10:02:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","changes":{"/w/src/b.ts":{"type":"add"}}}}}`,
		`{"timestamp":"2026-10-09T10:03:00Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"git commit -m 'feat: add the b module'\"}"}}`,
		`{"timestamp":"2026-10-09T10:03:01Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Process running with session ID 77\nOutput:\n"}}`,
		`{"timestamp":"2026-10-09T10:03:02Z","type":"response_item","payload":{"type":"function_call","name":"write_stdin","call_id":"c2","arguments":"{\"session_id\":77,\"chars\":\"\"}"}}`,
		`{"timestamp":"2026-10-09T10:03:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c2","output":"Process exited with code 0\nOutput:\n[detached HEAD 0abc1234] feat: add the b module\n 1 file changed"}}`,
		`{"timestamp":"2026-10-09T10:03:30Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c3","arguments":"{\"cmd\":\"git commit -m 'feat: rejected by the hook'\"}"}}`,
		`{"timestamp":"2026-10-09T10:03:31Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c3","output":"Process exited with code 1\nOutput:\nhook failed"}}`,
		`{"timestamp":"2026-10-09T10:04:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["/bin/zsh","-lc","gh pr create --fill"],"aggregated_output":"https://github.com/acme/billing/pull/12\n"}}}`)
	want := Trail{
		Edits: []Edit{{"/w/src/a.ts", 1791540060, false}, {"/w/src/old.ts", 1791540060, false}, {"/w/src/c.ts", 1791540090, true},
			{"/w/src/b.ts", 1791540120, false}},
		Commits: []Commit{{"git commit -m 'feat: add the b module'", "0abc1234", 1791540180, "/w"}},
		PRs:     []PR{{"acme/billing", 12}},
	}
	if !reflect.DeepEqual(tr, want) {
		t.Fatalf("got %+v\nwant %+v", tr, want)
	}
}

func TestAGrokTrailReadsEditsAndTerminalCommits(t *testing.T) {
	tr := trailOf(t, Named("grok"), filepath.Join(t.TempDir(), "sessions", "%2Fw", "s1", "chat_history.jsonl"),
		`{"type":"assistant","content":"","tool_calls":[{"id":"t0","name":"read_file","arguments":"{\"target_file\":\"/w/a.rb\",\"limit\":40}"}]}`,
		`{"type":"assistant","content":"","tool_calls":[{"id":"t1","name":"search_replace","arguments":"{\"file_path\":\"/w/a.rb\",\"old_string\":\"a\",\"new_string\":\"b\"}"}]}`,
		`{"type":"assistant","content":"","tool_calls":[{"id":"t2","name":"run_terminal_command","arguments":"{\"command\":\"git commit -am 'fix: keep the crumbs on one line'\"}"}]}`,
		`{"type":"tool_result","tool_call_id":"t2","content":"[main 5d6e7f80] fix: keep the crumbs on one line"}`)
	want := Trail{
		Edits:   []Edit{{"/w/a.rb", 0, false}},
		Reads:   []Edit{{"/w/a.rb", 0, false}},
		Commits: []Commit{{"git commit -am 'fix: keep the crumbs on one line'", "5d6e7f80", 0, ""}},
	}
	if !reflect.DeepEqual(tr, want) {
		t.Fatalf("got %+v\nwant %+v", tr, want)
	}
}

func TestOnlyCommandsThatRunGitCommitAreCommits(t *testing.T) {
	var lines []string
	for i, command := range []string{
		"git log --oneline -3 && echo commit",
		`echo 'git commit -m "fix: retry a charge once"'`,
		`git commit --dry-run -m "fix: retry a charge once"`,
		"git -C /w commit --amend --no-edit",
		`cd /w && GIT_EDITOR=true git commit -m "fix: retry a charge once"`,
		`bash -lc 'git commit -qm "fix: retry a charge once"'`,
		`git commit -qm "fix: refund; document --dry-run"`,
	} {
		quoted, _ := json.Marshal(command)
		lines = append(lines, fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b%d","name":"Bash","input":{"command":%s}}]}}`, i, quoted))
	}
	tr := trailOf(t, Named("claude"), filepath.Join(t.TempDir(), "s.jsonl"), lines...)
	var got []string
	for _, c := range tr.Commits {
		got = append(got, c.Command)
	}
	want := []string{"git -C /w commit --amend --no-edit", `cd /w && GIT_EDITOR=true git commit -m "fix: retry a charge once"`,
		`bash -lc 'git commit -qm "fix: retry a charge once"'`, `git commit -qm "fix: refund; document --dry-run"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestAFailedCommitWhoseMessageHoldsSeparatorsIsDropped(t *testing.T) {
	command := "git commit -m \"$(cat <<'EOF'\nfix: retry \\\" & refund\n\nBody.\nEOF\n)\""
	quoted, _ := json.Marshal(command)
	for failed, want := range map[bool][]Commit{true: {}, false: {{Command: command}}} {
		tr := trailOf(t, Named("claude"), filepath.Join(t.TempDir(), "s.jsonl"),
			fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":%s}}]}}`, quoted),
			fmt.Sprintf(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b1","is_error":%t,"content":"done"}]}}`, failed))
		if !reflect.DeepEqual(tr.Commits, want) {
			t.Fatalf("failed=%v: got %+v", failed, tr.Commits)
		}
	}
}
