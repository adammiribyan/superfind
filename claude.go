package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

var slashCommand = regexp.MustCompile(`(?s)<command-name>(/?[^<]*)</command-name>.*?(?:<command-args>(.*?)</command-args>|$)`)

var machinePrefixes = []string{"<", "Caveat:", "[Request interrupted", "Another Claude session sent a message", "[SYSTEM NOTIFICATION"}

func promptText(text string) string {
	if m := slashCommand.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1] + " " + m[2])
	}
	for _, p := range machinePrefixes {
		if strings.HasPrefix(text, p) {
			return ""
		}
	}
	return text
}

type contentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Content json.RawMessage `json:"content"`
}

type logLine struct {
	Type         string `json:"type"`
	Cwd          string `json:"cwd"`
	GitBranch    string `json:"gitBranch"`
	IsMeta       bool   `json:"isMeta"`
	CustomTitle  string `json:"customTitle"`
	AITitle      string `json:"aiTitle"`
	LastPrompt   string `json:"lastPrompt"`
	RelocatedCwd string `json:"relocatedCwd"`
	Message      struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type content struct {
	text      string
	toolUses  int
	results   []string
	hasResult bool
}

func decodeContent(raw json.RawMessage) content {
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return content{text: plain}
	}
	var blocks []contentBlock
	json.Unmarshal(raw, &blocks)
	var c content
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "tool_use":
			c.toolUses++
		case "tool_result":
			c.hasResult = true
			c.results = append(c.results, decodeContent(b.Content).text)
		}
	}
	c.text = strings.Join(texts, " ")
	return c
}

const interruptMark = "[Request interrupted"

type parser struct {
	t         transcript
	relocated string
}

func newParser() *parser { return &parser{t: transcript{last: replied}} }

func (p *parser) feed(raw []byte) bool {
	t := &p.t
	var l logLine
	if json.Unmarshal(raw, &l) != nil {
		return true
	}
	if t.cwd == "" {
		t.cwd = l.Cwd
	}
	t.branch = firstNonEmpty(l.GitBranch, t.branch)
	switch l.Type {
	case "user":
		c := decodeContent(l.Message.Content)
		text := strings.TrimSpace(c.text)
		wasInterrupted := strings.HasPrefix(text, interruptMark)
		for _, r := range c.results {
			wasInterrupted = wasInterrupted || strings.HasPrefix(strings.TrimSpace(r), interruptMark)
		}
		prompt := promptText(text)
		switch {
		case wasInterrupted:
			t.last = interrupted
		case c.hasResult:
			t.last = calledTool
		case prompt != "" && !l.IsMeta:
			t.last = prompted
			t.prompts = append(t.prompts, prompt)
			t.say("user", prompt)
		}
	case "assistant":
		c := decodeContent(l.Message.Content)
		t.toolCalls += c.toolUses
		t.last = replied
		if c.toolUses > 0 {
			t.last = calledTool
		}
		if text := strings.TrimSpace(c.text); text != "" && !l.IsMeta {
			t.say("assistant", text)
		}
	case "custom-title":
		t.customTitle = firstNonEmpty(l.CustomTitle, t.customTitle)
	case "ai-title":
		t.aiTitle = firstNonEmpty(l.AITitle, t.aiTitle)
	case "last-prompt":
		t.lastPrompt = firstNonEmpty(l.LastPrompt, t.lastPrompt)
	case "relocated":
		p.relocated = firstNonEmpty(l.RelocatedCwd, p.relocated)
	}
	return true
}

func (p *parser) done() transcript {
	if p.relocated != "" {
		p.t.cwd = p.relocated
	}
	return p.t
}
