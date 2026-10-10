package agent

import (
	"cmp"
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type codexItem struct {
	Type           string          `json:"type"`
	Role           string          `json:"role"`
	Content        json.RawMessage `json:"content"`
	Cwd            string          `json:"cwd"`
	ParentThreadID string          `json:"parent_thread_id"`
	Source         json.RawMessage `json:"source"`
	Git            *struct {
		Branch string `json:"branch"`
	} `json:"git"`
}

type codexLine struct {
	codexItem
	Payload json.RawMessage `json:"payload"`
}

type codexBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

const (
	codexRequest = "## My request for Codex:"
	answersOpen  = "<send_user_message_question_reply>"
	answersClose = "</send_user_message_question_reply>"
)

var (
	codexContextHeadings = []string{"# Context from my IDE setup", "# Files mentioned by the user", "# Files pasted by the user"}
	cwdTag               = regexp.MustCompile(`(?s)<cwd>(.*?)</cwd>`)
)

func codexID(path string) string {
	s := stem(path)
	return s[max(len(s)-36, 0):]
}

func codexNames(home string) map[string]string {
	names := map[string]string{}
	eachLine(filepath.Join(home, "session_index.jsonl"), func(raw []byte) bool {
		var entry struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(raw, &entry) == nil && entry.ID != "" && entry.Name != "" {
			names[entry.ID] = entry.Name
		}
		return true
	})
	return names
}

func codexAnswers(text string) string {
	inner, _, _ := strings.Cut(strings.TrimPrefix(text, answersOpen), answersClose)
	var replies []struct {
		Answer string `json:"answer"`
	}
	json.Unmarshal([]byte(inner), &replies)
	var answers []string
	for _, r := range replies {
		if a := strings.TrimSpace(r.Answer); a != "" {
			answers = append(answers, a)
		}
	}
	return strings.Join(answers, " ")
}

func codexBlockPrompt(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, answersOpen) {
		return codexAnswers(text)
	}
	if wholeElement(text) || bareTag.MatchString(text) {
		return ""
	}
	text = withoutWrappers(text)
	switch {
	case strings.HasPrefix(text, codexRequest), slices.ContainsFunc(codexContextHeadings, func(h string) bool { return strings.HasPrefix(text, h) }):
		_, request, _ := strings.Cut(text, codexRequest)
		return strings.TrimSpace(request)
	case strings.HasPrefix(text, "# AGENTS.md instructions for"):
		return ""
	}
	return text
}

func codexPrompt(blocks []codexBlock) string {
	var kept []string
	for _, b := range blocks {
		if b.Type != "input_text" {
			continue
		}
		if text := codexBlockPrompt(b.Text); text != "" {
			kept = append(kept, text)
		}
	}
	return strings.Join(kept, " ")
}

type codexReader struct{ t Transcript }

func newCodexReader(_ string, start Transcript) (reader, error) { return &codexReader{t: start}, nil }

func (c *codexReader) feed(raw []byte) bool {
	t := &c.t
	var l codexLine
	if json.Unmarshal(raw, &l) != nil {
		return true
	}
	item := l.codexItem
	if len(l.Payload) > 0 {
		item = codexItem{}
		if json.Unmarshal(l.Payload, &item) != nil {
			return true
		}
	}
	switch {
	case l.Type == "session_meta":
		t.Cwd = item.Cwd
		if item.Git != nil {
			t.Branch = item.Git.Branch
		}
		t.Hidden = item.ParentThreadID != ""
		t.ByAgent = string(item.Source) == `"exec"`
		return !t.Hidden
	case l.Type == "turn_context":
		t.Cwd = cmp.Or(item.Cwd, t.Cwd)
	case l.Type == "event_msg":
		if item.Type == "turn_aborted" {
			t.Last = Interrupted
		}
	case l.Type == "response_item", len(l.Payload) == 0 && l.Type != "":
		c.item(item)
	case l.Type == "" && l.Git != nil:
		t.Branch = l.Git.Branch
	}
	return true
}

func (c *codexReader) item(it codexItem) {
	t := &c.t
	switch it.Type {
	case "message":
		var blocks []codexBlock
		json.Unmarshal(it.Content, &blocks)
		switch it.Role {
		case "user":
			if t.Cwd == "" {
				for _, b := range blocks {
					if m := cwdTag.FindStringSubmatch(b.Text); m != nil {
						t.Cwd = strings.TrimSpace(m[1])
						break
					}
				}
			}
			if prompt := codexPrompt(blocks); prompt != "" {
				t.Last = Prompted
				t.ask(prompt)
			}
		case "assistant":
			var texts []string
			for _, b := range blocks {
				if b.Type == "output_text" {
					texts = append(texts, b.Text)
				}
			}
			if text := strings.TrimSpace(strings.Join(texts, " ")); text != "" {
				t.say("assistant", text)
			}
			t.Last = Replied
		}
	case "function_call", "custom_tool_call", "local_shell_call", "web_search_call", "tool_search_call":
		t.ToolCalls++
		t.Last = CalledTool
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
		t.Last = CalledTool
	}
}

func (c *codexReader) done() Transcript { return c.t }
