package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type grokSummary struct {
	Info struct {
		Cwd string `json:"cwd"`
	} `json:"info"`
	SessionSummary string `json:"session_summary"`
	GeneratedTitle string `json:"generated_title"`
	TitleIsManual  bool   `json:"title_is_manual"`
	HeadBranch     string `json:"head_branch"`
	SessionKind    string `json:"session_kind"`
}

type grokLine struct {
	Type            string            `json:"type"`
	Content         json.RawMessage   `json:"content"`
	SyntheticReason string            `json:"synthetic_reason"`
	ToolCalls       []json.RawMessage `json:"tool_calls"`
}

var userQuery = regexp.MustCompile(`(?s)<user_query>\s*(.*?)\s*</user_query>`)

func grokID(path string) string { return filepath.Base(filepath.Dir(path)) }

type grokReader struct{ t transcript }

func newGrokReader(path string) (reader, error) {
	g := &grokReader{t: transcript{last: replied}}
	dir := filepath.Dir(path)
	folder, _ := url.PathUnescape(filepath.Base(filepath.Dir(dir)))
	g.t.cwd = folder
	raw, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return g, nil
	}
	if err != nil {
		return nil, err
	}
	var s grokSummary
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	g.t.cwd = firstNonEmpty(s.Info.Cwd, folder)
	g.t.branch = s.HeadBranch
	g.t.hidden = s.SessionKind == "subagent"
	title := firstNonEmpty(s.SessionSummary, s.GeneratedTitle)
	if s.TitleIsManual {
		g.t.customTitle = title
	} else {
		g.t.aiTitle = title
	}
	return g, nil
}

func grokText(raw json.RawMessage) string {
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return plain
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &blocks)
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func (g *grokReader) feed(raw []byte) bool {
	t := &g.t
	var l grokLine
	if json.Unmarshal(raw, &l) != nil {
		return true
	}
	switch l.Type {
	case "user":
		m := userQuery.FindStringSubmatch(grokText(l.Content))
		if l.SyntheticReason != "" || m == nil || m[1] == "" {
			return true
		}
		t.last = prompted
		t.prompts = append(t.prompts, m[1])
		t.say("user", m[1])
	case "assistant":
		t.toolCalls += len(l.ToolCalls)
		t.last = replied
		if len(l.ToolCalls) > 0 {
			t.last = calledTool
		}
		if text := strings.TrimSpace(grokText(l.Content)); text != "" {
			t.say("assistant", text)
		}
	case "tool_result":
		t.last = calledTool
	}
	return true
}

func (g *grokReader) done() transcript { return g.t }
