package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

type event string

const (
	replied     event = "assistant_text"
	calledTool  event = "tool_call"
	interrupted event = "interrupted"
	prompted    event = "user_prompt"
)

type turn struct {
	role string
	text string
}

type transcript struct {
	cwd, branch                      string
	customTitle, aiTitle, lastPrompt string
	prompts, messages                []string
	turns                            []turn
	toolCalls                        int
	last                             event
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

func eachLine(path string, fn func(raw []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 1<<20)
	for {
		raw, err := reader.ReadBytes('\n')
		if len(raw) > 0 {
			fn(raw)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (t *transcript) say(role, text string) {
	if n := len(t.turns); n > 0 && t.turns[n-1].role == role {
		t.turns[n-1].text += "\n\n" + text
	} else {
		t.turns = append(t.turns, turn{role, text})
	}
	t.messages = append(t.messages, text)
}

type parser struct {
	t         transcript
	relocated string
}

func newParser() *parser { return &parser{t: transcript{last: replied}} }

func (p *parser) feed(raw []byte) {
	t := &p.t
	var l logLine
	if json.Unmarshal(raw, &l) != nil {
		return
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
}

func (p *parser) done() transcript {
	if p.relocated != "" {
		p.t.cwd = p.relocated
	}
	return p.t
}

func readTranscript(path string) (transcript, error) {
	p := newParser()
	err := eachLine(path, p.feed)
	return p.done(), err
}

const firstEndingWindow = 1 << 20

func readEnding(path string, enough func([]turn) bool) ([]turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	for window := int64(firstEndingWindow); ; window *= 4 {
		start := max(size-window, 0)
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if start > 0 {
			buf = buf[bytes.IndexByte(buf, '\n')+1:]
		}
		p := newParser()
		for len(buf) > 0 {
			end := bytes.IndexByte(buf, '\n') + 1
			if end == 0 {
				end = len(buf)
			}
			p.feed(buf[:end])
			buf = buf[end:]
		}
		turns := p.done().turns
		if start > 0 && len(turns) > 0 {
			turns = turns[1:]
		}
		if start == 0 || enough(turns) {
			return turns, nil
		}
	}
}
