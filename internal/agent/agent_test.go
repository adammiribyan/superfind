package agent

import (
	"strings"
	"testing"
)

func FuzzPrintableLeavesNothingATerminalWouldAct(f *testing.F) {
	for _, seed := range []string{"plain", "\x1b[31mred\x1b[0m", "\x1b]0;title\x07rest", "\x1b]8;;unterminated words", "bell\x07",
		"\u202eevil", "c1\u009b31m", "\x1bP dcs \x1b\\ after"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Printable(s)
		if i := strings.IndexFunc(out, dropped); i >= 0 {
			t.Fatalf("Printable(%q) = %q still holds %q", s, out, out[i:i+1])
		}
	})
}

func FuzzReadersNeverPanic(f *testing.F) {
	for _, seed := range []string{`{"type":"user","cwd":"/tmp","message":{"content":"hi"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"},{"type":"tool_use"}]}}`,
		`{"type":"session_meta","payload":{"cwd":"/tmp","parent_thread_id":"x"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>x</environment_context>"}]}}`,
		`{"type":"user","content":[{"type":"text","text":"<user_query>q</user_query>"}]}`, `{"type":`, "\x00\xff"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		for _, a := range Agents {
			r, err := a.reader("/nowhere/x/y/chat_history.jsonl", Transcript{Last: Replied})
			if err != nil {
				t.Fatal(err)
			}
			r.feed(line)
			r.done()
		}
		withoutWrappers(string(line))
		promptText(string(line))
		codexBlockPrompt(string(line))
	})
}
