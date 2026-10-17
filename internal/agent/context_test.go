package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func claudeReply(model string, input, cacheRead, output int) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"model":%q,"content":[{"type":"text","text":"done"}],`+
		`"usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
		model, input, cacheRead, output)
}

func left(t *testing.T, tr Transcript) string {
	t.Helper()
	if n, known := ContextLeft(tr.ContextUsed, tr.ContextWindow); known {
		return fmt.Sprintf("%d%%", n)
	}
	return "unknown"
}

func TestClaudeContextCountsTheLastReplysInputLikeClaudeCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"input and cache reads, not output", []string{typed, claudeReply("claude-opus-5-5", 10, 99_990, 50_000)}, "90%"},
		{"the last reply counts", []string{typed, claudeReply("claude-opus-5-5", 0, 900_000, 1), claudeReply("claude-opus-5-5", 0, 250_000, 1)}, "75%"},
		{"a subagent's reply doesn't", []string{typed, claudeReply("claude-opus-5-5", 0, 500_000, 1),
			`{"type":"assistant","isSidechain":true,"message":{"model":"claude-haiku-5-5","usage":{"input_tokens":10}}}`}, "50%"},
		{"a synthetic reply doesn't", []string{typed, claudeReply("claude-opus-5-5", 0, 500_000, 1), claudeReply("<synthetic>", 0, 0, 0)}, "50%"},
		{"an older model has 200K", []string{typed, claudeReply("claude-haiku-4-5-20251001", 0, 150_000, 1)}, "25%"},
		{"an older model past 200K had 1M", []string{typed, claudeReply("claude-sonnet-4-5", 0, 400_000, 1)}, "60%"},
		{"compaction leaves its summary", []string{typed, claudeReply("claude-opus-5-5", 0, 960_000, 1),
			`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","preTokens":960000,"postTokens":30000}}`}, "97%"},
		{"auto compaction early means a 200K window", []string{typed, claudeReply("claude-opus-5-5", 0, 167_000, 1),
			`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"auto","preTokens":167000,"postTokens":20000}}`,
			claudeReply("claude-opus-5-5", 0, 100_000, 1)}, "50%"},
		{"an early compaction under another model says nothing about this one", []string{typed, claudeReply("claude-haiku-4-5", 0, 167_000, 1),
			`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"auto","preTokens":167000,"postTokens":20000}}`,
			claudeReply("claude-opus-5-5", 0, 180_000, 1)}, "82%"},
		{"an older model seen past 200K keeps 1M through compaction", []string{typed, claudeReply("claude-sonnet-4-5", 0, 400_000, 1),
			`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","preTokens":400000,"postTokens":30000}}`}, "97%"},
		{"a subagent's compaction doesn't", []string{typed, claudeReply("claude-opus-5-5", 0, 880_000, 1),
			`{"type":"system","subtype":"compact_boundary","isSidechain":true,"compactMetadata":{"trigger":"auto","preTokens":167000,"postTokens":20000}}`}, "12%"},
		{"an early auto compaction before the first reply still counts", []string{typed,
			`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"auto","preTokens":167000,"postTokens":20000}}`,
			claudeReply("claude-opus-5-5", 0, 100_000, 1)}, "50%"},
		{"a compaction before any reply", []string{typed,
			`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","preTokens":1000,"postTokens":500}}`}, "unknown"},
		{"no reply", []string{typed}, "unknown"},
	}
	for _, c := range cases {
		if got := left(t, readLines(t, Named("claude"), path, c.lines...)); got != c.want {
			t.Errorf("%s: %s left, want %s", c.name, got, c.want)
		}
	}
}

func TestCodexContextMatchesCodexsOwnPercentage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-2026-10-09T10-00-00-0199a751-7860-aac9-7380-72c82895aaaa.jsonl")
	meta := `{"type":"session_meta","payload":{"cwd":"/w"}}`
	prompt := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix it"}]}}`
	count := func(total int) string {
		return fmt.Sprintf(`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":%d},"model_context_window":272000}}}`, total)
	}
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"after the first prompt", []string{meta, prompt, count(12_000)}, "100%"},
		{"the last count, less Codex's 12K baseline", []string{meta, prompt, count(200_000), count(142_000)}, "50%"},
		{"compacted with no count since", []string{meta, prompt, count(200_000), `{"type":"compacted","payload":{"message":""}}`}, "unknown"},
		{"a count without info", []string{meta, prompt, `{"type":"event_msg","payload":{"type":"token_count","info":null}}`}, "unknown"},
	}
	for _, c := range cases {
		if got := left(t, readLines(t, Named("codex"), path, c.lines...)); got != c.want {
			t.Errorf("%s: %s left, want %s", c.name, got, c.want)
		}
	}
}

func TestGrokContextComesFromItsSignals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "%2Fw", "0199a751", "chat_history.jsonl")
	line := `{"type":"user","content":"<user_query>fix it</user_query>"}`
	if got := left(t, readLines(t, Named("grok"), path, line)); got != "unknown" {
		t.Errorf("without signals.json: %s left", got)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "signals.json"), []byte(`{"contextTokensUsed":95190,"contextWindowTokens":256000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := left(t, readLines(t, Named("grok"), path, line)); got != "63%" {
		t.Errorf("with signals.json: %s left, want 63%%", got)
	}
	signals := filepath.Join(filepath.Dir(path), "signals.json")
	if err := os.WriteFile(signals, []byte(`{"contextTokensUsed":200001,"contextWindowTokens":200000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := left(t, readLines(t, Named("grok"), path, line)); got != "0%" {
		t.Errorf("past its window: %s left, want 0%%", got)
	}
	if err := os.WriteFile(signals, []byte(`{"contextTokens`), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := readLines(t, Named("grok"), path, line)
	if got := left(t, tr); got != "unknown" || tr.Incomplete {
		t.Errorf("a half-written signals.json: %s left, incomplete %v; want unknown and the session still read whole", got, tr.Incomplete)
	}
}

func TestContextLeftNeverRaisesTheWindowItIsGiven(t *testing.T) {
	cases := []struct{ used, window, left int }{
		{200_000, 200_000, 0}, {200_001, 200_000, 0}, {1, 200_000, 100}, {100_000, 200_000, 50}, {995, 1000, 1}, {994, 1000, 1}, {996, 1000, 0},
	}
	for _, c := range cases {
		if got, known := ContextLeft(c.used, c.window); !known || got != c.left {
			t.Errorf("ContextLeft(%d, %d) = %d, want %d", c.used, c.window, got, c.left)
		}
	}
}

func TestASignalsChangeCountsAsAChangeToTheGrokSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "%2Fw", "0199a751", "chat_history.jsonl")
	readLines(t, Named("grok"), path, `{"type":"user","content":"hi"}`)
	before, _, err := Named("grok").Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "signals.json"), []byte(`{"contextTokensUsed":1,"contextWindowTokens":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if after, _, _ := Named("grok").Stat(path); after == before {
		t.Error("writing signals.json left the session's file id unchanged")
	}
}
