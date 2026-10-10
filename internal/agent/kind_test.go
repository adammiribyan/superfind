package agent

import (
	"os"
	"path/filepath"
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
	tr := claudeLines(t, peer, reply, typed, reply)
	if tr.ByAgent || strings.Join(tr.Prompts, "|") != "fix the login bug" {
		t.Fatalf("got ByAgent=%v prompts=%q", tr.ByAgent, tr.Prompts)
	}
	if len(tr.Turns) != 3 || tr.Turns[1] != (Turn{"user", "fix the login bug"}) {
		t.Fatalf("turns = %+v", tr.Turns)
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
