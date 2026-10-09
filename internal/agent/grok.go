package agent

import (
	"cmp"
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

var userQuery = regexp.MustCompile(`(?s)<user_query>(.*)</user_query>`)

func grokID(path string) string { return filepath.Base(filepath.Dir(path)) }

type grokReader struct{ t Transcript }

func newGrokReader(path string) (reader, error) {
	g := &grokReader{t: Transcript{Last: Replied}}
	dir := filepath.Dir(path)
	folder, _ := url.PathUnescape(filepath.Base(filepath.Dir(dir)))
	g.t.Cwd = folder
	raw, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return g, nil
	}
	if err != nil {
		return nil, err
	}
	var s grokSummary
	if json.Unmarshal(raw, &s) != nil {
		g.t.Incomplete = true
		return g, nil
	}
	g.t.Cwd = cmp.Or(s.Info.Cwd, folder)
	g.t.Branch = s.HeadBranch
	g.t.Hidden = s.SessionKind == "subagent"
	title := cmp.Or(s.SessionSummary, s.GeneratedTitle)
	if s.TitleIsManual {
		g.t.CustomTitle = title
	} else {
		g.t.AITitle = title
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
		if l.SyntheticReason != "" || m == nil {
			return true
		}
		prompt := strings.TrimSpace(m[1])
		if prompt == "" {
			return true
		}
		t.Last = Prompted
		t.Prompts = append(t.Prompts, prompt)
		t.say("user", prompt)
	case "assistant":
		t.ToolCalls += len(l.ToolCalls)
		t.Last = Replied
		if len(l.ToolCalls) > 0 {
			t.Last = CalledTool
		}
		if text := strings.TrimSpace(grokText(l.Content)); text != "" {
			t.say("assistant", text)
		}
	case "tool_result":
		t.Last = CalledTool
	}
	return true
}

func (g *grokReader) done() Transcript { return g.t }
