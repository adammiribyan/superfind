package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func stepsOf(t *testing.T, a *Agent, path string, lines ...string) (Transcript, []Step) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, steps, err := ReadSteps(a, path)
	if err != nil {
		t.Fatal(err)
	}
	return tr, steps
}

func TestClaudeStepsSitAfterTheTextThatCameBeforeThem(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.MkdirAll(filepath.Join(dir, "s1", "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := `{"type":"assistant","timestamp":"2026-10-09T10:05:00Z","message":{"content":[{"type":"tool_use","id":"w1","name":"Write","input":{"file_path":"/w/refund.go","content":"x"}}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-10-09T10:05:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"w1","content":"File created"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s1", "subagents", "agent-a1.jsonl"), []byte(sub), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, steps := stepsOf(t, Named("claude"), path,
		`{"type":"user","message":{"role":"user","content":"fix the double charge please"}}`,
		`{"type":"assistant","timestamp":"2026-10-09T10:00:00Z","message":{"content":[{"type":"text","text":"The retry resubmits the charge."},{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":"/w/charge.go","old_string":"a","new_string":"b"}}]}}`,
		`{"type":"user","timestamp":"2026-10-09T10:00:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"e1","content":"The file has been updated"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-09T10:00:30Z","message":{"content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"user","timestamp":"2026-10-09T10:00:31Z","message":{"content":[{"type":"tool_result","tool_use_id":"b1","is_error":true,"content":"Exit code 1\nFAIL TestCharge"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-09T10:01:00Z","message":{"content":[{"type":"text","text":"The test still charges twice."},{"type":"tool_use","id":"g1","name":"Agent","input":{"prompt":"write the refund"}}]}}`,
		`{"type":"user","timestamp":"2026-10-09T10:06:00Z","toolUseResult":{"agentId":"a1","status":"completed"},"message":{"content":[{"type":"tool_result","tool_use_id":"g1","content":"Wrote the refund."}]}}`,
		`{"type":"assistant","timestamp":"2026-10-09T10:07:00Z","message":{"content":[{"type":"text","text":"Fixed."}]}}`)
	want := []Step{{Message: 2, Edit: "/w/charge.go"}, {Message: 2, Error: "Exit code 1\nFAIL TestCharge"}, {Message: 3, Edit: "/w/refund.go", Subagent: true}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("got %+v", steps)
	}
	if strings.Join(tr.Messages, "|") != "fix the double charge please|The retry resubmits the charge.|The test still charges twice.|Fixed." {
		t.Fatalf("messages %q", tr.Messages)
	}
}

func TestASubagentIdNeverLeavesTheSessionsFolder(t *testing.T) {
	if got := claudeSubagent([]byte(`{"toolUseResult":{"agentId":"../../../etc/x"}}`), "/h/s1.jsonl"); got != nil {
		t.Fatalf("got %q", got)
	}
}

func TestCodexStepsHoldPatchesAndFailedCommands(t *testing.T) {
	_, steps := stepsOf(t, Named("codex"), filepath.Join(t.TempDir(), "rollout-x.jsonl"),
		`{"timestamp":"2026-10-09T10:00:00Z","type":"session_meta","payload":{"id":"x","cwd":"/w"}}`,
		`{"timestamp":"2026-10-09T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the charge"}]}}`,
		`{"timestamp":"2026-10-09T10:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Patching the retry."}]}}`,
		`{"timestamp":"2026-10-09T10:01:00Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"p1","input":"*** Begin Patch\n*** Update File: charge.go\n*** End Patch"}}`,
		`{"timestamp":"2026-10-09T10:01:01Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"p1","output":"Success. Updated the following files:\nM charge.go"}}`,
		`{"timestamp":"2026-10-09T10:01:30Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"go test\"}"}}`,
		`{"timestamp":"2026-10-09T10:01:31Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"FAIL TestCharge\nProcess exited with code 1"}}`,
		`{"timestamp":"2026-10-09T10:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Still failing."}]}}`,
		`{"timestamp":"2026-10-09T10:02:30Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","status":"failed","exit_code":2,"aggregated_output":"no such table"}}}`,
		`{"timestamp":"2026-10-09T10:02:40Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","status":"failed","changes":{"/w/refund.go":{}}}}}`)
	want := []Step{{Message: 2, Edit: "/w/charge.go"}, {Message: 2, Error: "FAIL TestCharge\nProcess exited with code 1"},
		{Message: 3, Error: "no such table"}, {Message: 3, Error: "the edit failed"}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("got %+v", steps)
	}
}

func TestGrokStepsHoldEditsWithoutErrors(t *testing.T) {
	_, steps := stepsOf(t, Named("grok"), filepath.Join(t.TempDir(), "s", "chat_history.jsonl"),
		`{"type":"user","content":[{"type":"text","text":"<user_query>\nfix the charge\n</user_query>"}]}`,
		`{"type":"assistant","content":"Editing the retry.","tool_calls":[{"id":"g1","name":"search_replace","arguments":"{\"file_path\":\"/w/charge.go\"}"}]}`,
		`{"type":"tool_result","content":"error: nothing matched","tool_call_id":"g1"}`,
		`{"type":"assistant","content":"Done."}`)
	if want := []Step{{Message: 2, Edit: "/w/charge.go"}}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("got %+v", steps)
	}
}
