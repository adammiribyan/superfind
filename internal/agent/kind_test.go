package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func readLines(t *testing.T, a *Agent, path string, lines ...string) Transcript {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := ReadTranscript(a, path)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func claudeLines(t *testing.T, lines ...string) Transcript {
	return readLines(t, Named("claude"), filepath.Join(t.TempDir(), "s.jsonl"), lines...)
}

const (
	typed = `{"type":"user","cwd":"/w","entrypoint":"cli","origin":{"kind":"human"},"message":{"content":"fix the login bug"}}`
	reply = `{"type":"assistant","message":{"content":[{"type":"text","text":"Fixed it."}]}}`
	peer  = `{"type":"user","cwd":"/w","entrypoint":"cli","isMeta":true,"origin":{"kind":"peer","body":"Review PR 14 please"},` +
		`"message":{"content":"Another Claude session sent a message:\n<cross-session-message from=\"x\">\nReview PR 14 please\n</cross-session-message>"}}`
)

func TestAClaudeSessionYouTypedInIsInteractive(t *testing.T) {
	tr := claudeLines(t, typed, reply)
	if tr.ByAgent || strings.Join(tr.Prompts, "|") != "fix the login bug" {
		t.Fatalf("got ByAgent=%v prompts=%q", tr.ByAgent, tr.Prompts)
	}
}

func TestAClaudeSessionRunWithPrintIsStartedByAnAgent(t *testing.T) {
	tr := claudeLines(t, strings.Replace(typed, `"cli"`, `"sdk-cli"`, 1), reply)
	if !tr.ByAgent || strings.Join(tr.Prompts, "|") != "fix the login bug" {
		t.Fatalf("got ByAgent=%v prompts=%q", tr.ByAgent, tr.Prompts)
	}
}

func TestAClaudeSessionDrivenOnlyByAnotherSessionIsShownAsStartedByAnAgent(t *testing.T) {
	tr := claudeLines(t, peer, reply)
	if !tr.ByAgent || strings.Join(tr.Prompts, "|") != "Review PR 14 please" {
		t.Fatalf("got ByAgent=%v prompts=%q", tr.ByAgent, tr.Prompts)
	}
	if len(tr.Turns) != 2 || tr.Turns[0] != (Turn{"user", "Review PR 14 please"}) || tr.Turns[1] != (Turn{"assistant", "Fixed it."}) {
		t.Fatalf("turns = %+v", tr.Turns)
	}
	if strings.Join(tr.Messages, "|") != "Review PR 14 please|Fixed it." {
		t.Fatalf("messages = %q", tr.Messages)
	}
}

func TestAClaudeSessionWithBothKeepsOnlyWhatYouTyped(t *testing.T) {
	tr := claudeLines(t, peer, reply, typed, reply, peer, reply)
	if tr.ByAgent || tr.PeerDriven || strings.Join(tr.Prompts, "|") != "fix the login bug" {
		t.Fatalf("got ByAgent=%v prompts=%q", tr.ByAgent, tr.Prompts)
	}
	want := []Turn{{"assistant", "Fixed it."}, {"user", "fix the login bug"}, {"assistant", "Fixed it.\n\nFixed it."}}
	if !slices.Equal(tr.Turns, want) {
		t.Fatalf("turns = %+v", tr.Turns)
	}
}

func TestEverySDKEntrypointMarksTheSessionAndOnlyTheFirstOneCounts(t *testing.T) {
	for entrypoint, want := range map[string]bool{"sdk-cli": true, "sdk-ts": true, "sdk-py": true, "cli": false, "claude-desktop": false} {
		if tr := claudeLines(t, strings.Replace(typed, `"cli"`, `"`+entrypoint+`"`, 1), reply); tr.ByAgent != want {
			t.Errorf("entrypoint %s: ByAgent=%v", entrypoint, tr.ByAgent)
		}
	}
	resumedByScript := strings.Replace(typed, `"cli"`, `"sdk-cli"`, 1)
	if tr := claudeLines(t, typed, reply, resumedByScript, reply); tr.ByAgent {
		t.Error("a later claude -p --resume marked a session a person started")
	}
}

func TestAPreviewOfAMixedSessionLooksPastPeerRequestsForWhatYouTyped(t *testing.T) {
	big := `{"type":"user","message":{"content":[{"type":"tool_result","content":"` + strings.Repeat("x", 2<<20) + `"}]}}`
	path := filepath.Join(t.TempDir(), "s.jsonl")
	tr := readLines(t, Named("claude"), path, typed, reply, big, peer, reply, peer, reply)
	if tr.ByAgent {
		t.Fatal("a session you typed in counted as started by another program")
	}
	turns, err := ReadEnding(Named("claude"), path, func(turns []Turn) bool {
		return slices.ContainsFunc(turns, func(t Turn) bool { return t.Role == "user" })
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(turns, Turn{"user", "fix the login bug"}) || slices.Contains(turns, Turn{"user", "Review PR 14 please"}) {
		t.Fatalf("preview turns = %+v", turns)
	}
}

func TestAPeerMessageWithoutABodyKeepsTheTextInsideItsWrapper(t *testing.T) {
	bare := `{"type":"user","cwd":"/w","isMeta":true,"origin":{"kind":"peer"},"message":{"content":"Another Claude session sent a message:\n` +
		`<cross-session-message from=\"x\">\nRun the release checklist\n</cross-session-message>\n\nThis came from another session."}}`
	if tr := claudeLines(t, bare, reply); strings.Join(tr.Prompts, "|") != "Run the release checklist" || !tr.ByAgent {
		t.Fatalf("got ByAgent=%v prompts=%q", tr.ByAgent, tr.Prompts)
	}
}

func TestACoordinatorMessageKeepsItsRequest(t *testing.T) {
	coordinator := `{"type":"user","cwd":"/w","isMeta":true,"origin":{"kind":"coordinator"},"message":{"content":` +
		`"The coordinator sent a message while you were working:\nFix the tenancy check.\n\nAddress this before completing your current task."}}`
	if tr := claudeLines(t, coordinator, reply); strings.Join(tr.Prompts, "|") != "Fix the tenancy check." || !tr.ByAgent {
		t.Fatalf("got ByAgent=%v prompts=%q", tr.ByAgent, tr.Prompts)
	}
}

func TestAPeerMessageIsNeverCountedAsTyped(t *testing.T) {
	plain := `{"type":"user","cwd":"/w","origin":{"kind":"peer","body":"Rebase onto main"},"message":{"content":"Rebase onto main"}}`
	if tr := claudeLines(t, plain, reply); !tr.ByAgent || strings.Join(tr.Prompts, "|") != "Rebase onto main" {
		t.Fatalf("got ByAgent=%v prompts=%q", tr.ByAgent, tr.Prompts)
	}
}

func TestAnUnansweredRequestFromAnotherSessionIsTheLastEvent(t *testing.T) {
	if tr := claudeLines(t, typed, reply, peer); tr.Last != Prompted {
		t.Fatalf("last = %v", tr.Last)
	}
}

func TestACoordinatorMessageCountsAsAnAgentPrompt(t *testing.T) {
	coordinator := strings.Replace(peer, `"kind":"peer"`, `"kind":"coordinator"`, 1)
	if tr := claudeLines(t, coordinator, reply); !tr.ByAgent || len(tr.Prompts) != 1 {
		t.Fatalf("got ByAgent=%v prompts=%q", tr.ByAgent, tr.Prompts)
	}
}

func TestATaskNotificationIsNotAPrompt(t *testing.T) {
	note := strings.Replace(peer, `"kind":"peer"`, `"kind":"task-notification"`, 1)
	if tr := claudeLines(t, note, reply); tr.ByAgent || len(tr.Prompts) != 0 {
		t.Fatalf("got ByAgent=%v prompts=%q", tr.ByAgent, tr.Prompts)
	}
}

func TestACodexExecRunIsStartedByAnAgent(t *testing.T) {
	meta := func(source string) string {
		return `{"type":"session_meta","payload":{"cwd":"/w","source":` + source + `}}`
	}
	ask := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"review the diff"}]}}`
	dir := t.TempDir()
	for source, want := range map[string]bool{`"exec"`: true, `"cli"`: false, `"vscode"`: false, `{"other":{}}`: false} {
		path := filepath.Join(dir, "sessions/2026/10/10/rollout-2026-10-10T10-00-00-01990001-a751-7860-aac9-738072c82895.jsonl")
		if tr := readLines(t, Named("codex"), path, meta(source), ask); tr.ByAgent != want || len(tr.Prompts) != 1 {
			t.Errorf("source %s: ByAgent=%v prompts=%q", source, tr.ByAgent, tr.Prompts)
		}
	}
}

func TestAHeadlessGrokSessionIsStartedByAnAgent(t *testing.T) {
	for kind, want := range map[string]bool{"headless": true, "": false} {
		chat := filepath.Join(t.TempDir(), "sessions", "%2Fw", "s1", "chat_history.jsonl")
		if err := os.MkdirAll(filepath.Dir(chat), 0o755); err != nil {
			t.Fatal(err)
		}
		summary := `{"info":{"cwd":"/w"},"session_kind":"` + kind + `"}`
		if err := os.WriteFile(filepath.Join(filepath.Dir(chat), "summary.json"), []byte(summary), 0o644); err != nil {
			t.Fatal(err)
		}
		tr := readLines(t, Named("grok"), chat, `{"type":"user","content":[{"type":"text","text":"<user_query>why slow</user_query>"}]}`)
		if tr.ByAgent != want || len(tr.Prompts) != 1 {
			t.Errorf("session_kind %q: ByAgent=%v prompts=%q", kind, tr.ByAgent, tr.Prompts)
		}
	}
}
