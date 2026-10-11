package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

const machineRequest = "machine-made request"

func promptedBy(text string, plain bool) Event {
	trimmed := strings.TrimSpace(text)
	switch {
	case trimmed == "":
		return "keep"
	case !plain || !strings.HasPrefix(trimmed, "<") && !strings.HasPrefix(trimmed, "#"):
		return Prompted
	}
	return ""
}

func (b *built) end(e Event) {
	if e != "keep" {
		b.last = e
	}
}

type built struct {
	lines     []any
	toolCalls int
	hidden    bool
	summary   map[string]any
	last      Event
}

func codexLines(layout []byte, texts [3]string) built {
	var out built
	item := func(payload map[string]any) map[string]any {
		return map[string]any{"type": "response_item", "payload": payload}
	}
	message := func(role string, blocks ...any) map[string]any {
		return item(map[string]any{"type": "message", "role": role, "content": blocks})
	}
	block := func(kind, text string) map[string]any { return map[string]any{"type": kind, "text": text} }
	for _, b := range layout {
		text := texts[int(b>>4)%3]
		switch b % 16 {
		case 0:
			out.lines = append(out.lines, map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": text, "git": map[string]any{"branch": text}, "source": "exec"}})
		case 1:
			out.lines = append(out.lines, map[string]any{"type": "turn_context", "payload": map[string]any{"cwd": text}})
		case 2:
			out.lines = append(out.lines, message("user", block("input_text", text)))
			out.end(promptedBy(text, true))
		case 3:
			out.lines = append(out.lines, message("assistant", block("output_text", text), block("reasoning", text), block("output_text", text)))
			out.last = Replied
		case 4:
			out.lines = append(out.lines, item(map[string]any{"type": "function_call"}), item(map[string]any{"type": "function_call_output", "output": text}))
			out.toolCalls++
			out.last = CalledTool
		case 5:
			out.lines = append(out.lines, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "turn_aborted"}})
			out.last = Interrupted
		case 6:
			out.lines = append(out.lines, map[string]any{"type": "message", "role": "user", "content": []any{block("input_text", text)}})
			out.end(promptedBy(text, true))
		case 7:
			answers, _ := json.Marshal([]any{map[string]any{"answer": text}})
			out.lines = append(out.lines, message("user", block("input_text", answersOpen+string(answers)+answersClose)))
			out.end(promptedBy(text, false))
		case 8:
			out.lines = append(out.lines, message("user", block("input_text", "<environment_context><cwd>"+text+"</cwd></environment_context>"), block("input_image", ""), block("input_text", text)))
			out.end(promptedBy(text, true))
		case 9:
			out.lines = append(out.lines, message("user", block("input_text", "# Context from my IDE setup\n"+codexRequest+"\n"+text)))
			out.end(promptedBy(text, false))
		case 10:
			out.lines = append(out.lines, map[string]any{"git": map[string]any{"branch": text}})
		case 11:
			kind := []string{"custom_tool_call", "local_shell_call", "web_search_call", "tool_search_call"}[int(b>>4)%4]
			out.lines = append(out.lines, item(map[string]any{"type": kind}))
			out.toolCalls++
			out.last = CalledTool
		case 12:
			kind := []string{"custom_tool_call_output", "tool_search_output"}[int(b>>4)%2]
			out.lines = append(out.lines, item(map[string]any{"type": kind, "output": text}))
			out.last = CalledTool
		case 13:
			out.lines = append(out.lines, map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": text, "parent_thread_id": "0199a751"}})
			out.hidden = true
		case 14:
			out.lines = append(out.lines, message("user", block("input_text", "# AGENTS.md instructions for "+text)))
		case 15:
			out.lines = append(out.lines, message("developer", block("input_text", text)))
		}
	}
	return out
}

func grokLines(layout []byte, texts [3]string) built {
	var out built
	query := func(text string) string { return "<user_query>" + text + "</user_query>" }
	for _, b := range layout {
		text := texts[int(b>>4)%3]
		switch b % 8 {
		case 0:
			out.lines = append(out.lines, map[string]any{"type": "user", "content": query(text)})
			out.end(promptedBy(text, false))
		case 1:
			out.lines = append(out.lines, map[string]any{"type": "user", "content": []any{map[string]any{"type": "image"}, map[string]any{"type": "text", "text": query(text)}}})
			out.end(promptedBy(text, false))
		case 2:
			out.lines = append(out.lines, map[string]any{"type": "user", "content": query(machineRequest), "synthetic_reason": "x"})
		case 3:
			out.lines = append(out.lines, map[string]any{"type": "assistant", "content": text})
			out.last = Replied
		case 4:
			calls := int(b>>4)%3 + 1
			out.lines = append(out.lines, map[string]any{"type": "assistant", "content": []any{map[string]any{"type": "text", "text": text}, map[string]any{"type": "text", "text": text}}, "tool_calls": make([]any, calls)})
			out.toolCalls += calls
			out.last = CalledTool
		case 5:
			out.lines = append(out.lines, map[string]any{"type": "tool_result", "content": text})
			out.last = CalledTool
		case 6:
			out.lines = append(out.lines, map[string]any{"type": "user", "content": text})
			if strings.Contains(text, "<user_query>") {
				out.last = ""
			}
		case 7:
			kind := []string{"interactive", "headless", "subagent"}[int(b>>4)%3]
			out.summary = map[string]any{"info": map[string]any{"cwd": text}, "generated_title": text, "session_kind": kind, "title_is_manual": b&0x40 != 0}
			out.hidden = kind == "subagent"
		}
	}
	return out
}

func FuzzTranscriptsReadTheSameWholeAndFromTheEnd(f *testing.F) {
	f.Add([]byte{0, 1, 2, 0x13, 4, 0x23, 0x12, 5, 7, 0x18, 9, 0x1a, 6, 11, 0x1b, 0x2b, 0x3b, 12, 0x1c, 14, 15}, "fix the login bug", "  done  ", "<user_query>x</user_query>")
	f.Add([]byte{0, 3, 1, 4, 0x22, 5, 0x10, 0x20, 13, 2}, "", "\n", "line one\nline two")
	f.Add([]byte{3, 12}, "a", "b", "c")
	f.Add([]byte{3, 0x1c, 3, 5}, "a", "b", "c")
	f.Add([]byte{0x10, 0x21, 2, 3, 0x14, 0x24, 5, 6, 0x17, 0x20}, machineRequest, "  padded  ", "<b>tag</b>")
	f.Fuzz(func(t *testing.T, layout []byte, a, b, c string) {
		if len(layout) > 64 || len(a)+len(b)+len(c) > 4<<20 {
			t.Skip()
		}
		texts := [3]string{a, b, c}
		for _, read := range []struct {
			agent *Agent
			path  string
			built built
		}{
			{Named("codex"), "sessions/2026/10/09/rollout-2026-10-09T10-00-00-0199a751-7860-aac9-7380-72c82895aaaa.jsonl", codexLines(layout, texts)},
			{Named("grok"), "sessions/%2Ftmp%2Fapp/0199a751-7860/chat_history.jsonl", grokLines(layout, texts)},
		} {
			path := filepath.Join(t.TempDir(), read.path)
			var file []byte
			for _, line := range read.built.lines {
				raw, _ := json.Marshal(line)
				file = append(append(file, raw...), '\n')
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, file, 0o600); err != nil {
				t.Fatal(err)
			}
			if read.built.summary != nil {
				raw, _ := json.Marshal(read.built.summary)
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "summary.json"), raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			whole, err := ReadTranscript(read.agent, path)
			if err != nil {
				t.Fatal(err)
			}
			if whole.Hidden != read.built.hidden {
				t.Fatalf("%s hidden = %v, want %v", read.agent.ID, whole.Hidden, read.built.hidden)
			}
			if whole.Hidden {
				continue
			}
			ending, err := ReadEnding(read.agent, path, func([]Turn) bool { return false })
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(whole.Turns, ending) {
				t.Fatalf("%s turns read whole:\n%q\nread from the end:\n%q", read.agent.ID, whole.Turns, ending)
			}
			if read.built.last != "" && whole.Last != read.built.last {
				t.Fatalf("%s ended with %q, want %q", read.agent.ID, whole.Last, read.built.last)
			}
			if whole.ToolCalls != read.built.toolCalls {
				t.Fatalf("%s counted %d tool calls, want %d", read.agent.ID, whole.ToolCalls, read.built.toolCalls)
			}
			for _, turn := range whole.Turns {
				if turn.Text == "" || turn.Text != strings.TrimSpace(turn.Text) {
					t.Fatalf("%s turn %q is blank or padded", read.agent.ID, turn.Text)
				}
			}
			for _, prompt := range whole.Prompts {
				if prompt == "" || prompt != strings.TrimSpace(prompt) {
					t.Fatalf("%s prompt %q is blank or padded", read.agent.ID, prompt)
				}
				if prompt == machineRequest && !strings.Contains(a+b+c, machineRequest) {
					t.Fatalf("%s took a machine-made request for a prompt", read.agent.ID)
				}
			}
		}
	})
}
