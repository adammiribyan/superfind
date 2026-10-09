package main

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

type codexItem struct {
	Type           string          `json:"type"`
	Role           string          `json:"role"`
	Content        json.RawMessage `json:"content"`
	Cwd            string          `json:"cwd"`
	ParentThreadID string          `json:"parent_thread_id"`
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

const codexRequest = "## My request for Codex:"

var (
	codexMachinePrefixes = []string{"<", "# AGENTS.md instructions for", "# Files mentioned by the user",
		"# Files pasted by the user", "# Context from my IDE setup"}
	cwdTag = regexp.MustCompile(`(?s)<cwd>(.*?)</cwd>`)
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

func codexPrompt(blocks []codexBlock) string {
	var kept []string
	for _, b := range blocks {
		if b.Type != "input_text" {
			continue
		}
		text := strings.TrimSpace(b.Text)
		if _, request, ok := strings.Cut(text, codexRequest); ok {
			text = strings.TrimSpace(request)
		} else {
			for _, p := range codexMachinePrefixes {
				if strings.HasPrefix(text, p) {
					text = ""
					break
				}
			}
		}
		if text != "" {
			kept = append(kept, text)
		}
	}
	return strings.Join(kept, " ")
}

type codexReader struct{ t transcript }

func newCodexReader(string) (reader, error) { return &codexReader{t: transcript{last: replied}}, nil }

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
		t.cwd = item.Cwd
		if item.Git != nil {
			t.branch = item.Git.Branch
		}
		t.hidden = item.ParentThreadID != ""
		return !t.hidden
	case l.Type == "turn_context":
		t.cwd = firstNonEmpty(t.cwd, item.Cwd)
	case l.Type == "event_msg":
		if item.Type == "turn_aborted" {
			t.last = interrupted
		}
	case l.Type == "response_item", len(l.Payload) == 0 && l.Type != "":
		c.item(item)
	case l.Type == "" && l.Git != nil:
		t.branch = l.Git.Branch
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
			if t.cwd == "" {
				for _, b := range blocks {
					if m := cwdTag.FindStringSubmatch(b.Text); m != nil {
						t.cwd = strings.TrimSpace(m[1])
						break
					}
				}
			}
			if prompt := codexPrompt(blocks); prompt != "" {
				t.last = prompted
				t.prompts = append(t.prompts, prompt)
				t.say("user", prompt)
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
			t.last = replied
		}
	case "function_call", "custom_tool_call", "local_shell_call", "web_search_call":
		t.toolCalls++
		t.last = calledTool
	case "function_call_output", "custom_tool_call_output":
		t.last = calledTool
	}
}

func (c *codexReader) done() transcript { return c.t }
