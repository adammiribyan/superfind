package agent

import (
	"cmp"
	"encoding/json"
	"regexp"
	"strings"
)

var slashCommand = regexp.MustCompile(`(?s)<command-name>(/?[^<]*)</command-name>.*?(?:<command-args>(.*?)</command-args>|$)`)

var machinePrefixes = []string{"Caveat:", "[Request interrupted", "Another Claude session sent a message", "[SYSTEM NOTIFICATION"}

func promptText(text string) string {
	if m := slashCommand.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1] + " " + m[2])
	}
	text = withoutWrappers(text)
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
	Subtype      string `json:"subtype"`
	IsSidechain  bool   `json:"isSidechain"`
	Cwd          string `json:"cwd"`
	GitBranch    string `json:"gitBranch"`
	IsMeta       bool   `json:"isMeta"`
	CustomTitle  string `json:"customTitle"`
	AITitle      string `json:"aiTitle"`
	LastPrompt   string `json:"lastPrompt"`
	RelocatedCwd string `json:"relocatedCwd"`
	Entrypoint   string `json:"entrypoint"`
	Origin       struct {
		Kind string `json:"kind"`
		Body string `json:"body"`
	} `json:"origin"`
	Message struct {
		Content json.RawMessage `json:"content"`
		Model   string          `json:"model"`
		Usage   *claudeUsage    `json:"usage"`
	} `json:"message"`
	CompactMetadata struct {
		Trigger    string `json:"trigger"`
		PreTokens  int    `json:"preTokens"`
		PostTokens int    `json:"postTokens"`
	} `json:"compactMetadata"`
}

type claudeUsage struct {
	Input       int `json:"input_tokens"`
	CacheCreate int `json:"cache_creation_input_tokens"`
	CacheRead   int `json:"cache_read_input_tokens"`
	Output      int `json:"output_tokens"`
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
	t          Transcript
	relocated  string
	entrypoint string
	typed      bool
	peers      *Transcript
	before     Event

	autoCompactedAt int
}

var (
	agentMessageLine = regexp.MustCompile(`^(?:Another Claude session|The coordinator) sent a message[^\n]*:\n`)
	wrappedMessage   = regexp.MustCompile(`(?s)^<[a-z][\w-]*[^<>]*>\n?(.*?)\n?</[a-z][\w-]*>`)
)

const coordinatorClosing = "Address this before completing your current task."

func agentPrompt(l logLine, text string) (string, bool) {
	if l.Origin.Kind != "peer" && l.Origin.Kind != "coordinator" {
		return "", false
	}
	if body := strings.TrimSpace(l.Origin.Body); body != "" {
		return body, true
	}
	text = agentMessageLine.ReplaceAllString(strings.TrimSpace(text), "")
	if m := wrappedMessage.FindStringSubmatch(text); m != nil {
		text = m[1]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), coordinatorClosing)), true
}

func (p *parser) feed(raw []byte) bool {
	t := &p.t
	var l logLine
	if json.Unmarshal(raw, &l) != nil {
		return true
	}
	if t.Cwd == "" {
		t.Cwd = l.Cwd
	}
	t.Branch = cmp.Or(l.GitBranch, t.Branch)
	if p.entrypoint == "" && l.Entrypoint != "" {
		p.entrypoint = l.Entrypoint
		t.ByAgent = strings.HasPrefix(l.Entrypoint, "sdk-")
	}
	switch l.Type {
	case "user":
		before := p.before
		p.before = ""
		c := decodeContent(l.Message.Content)
		text := strings.TrimSpace(c.text)
		wasInterrupted := strings.HasPrefix(text, interruptMark)
		for _, r := range c.results {
			wasInterrupted = wasInterrupted || strings.HasPrefix(strings.TrimSpace(r), interruptMark)
		}
		prompt := promptText(text)
		if asked, fromAgent := agentPrompt(l, text); fromAgent {
			if asked != "" {
				t.Last = Prompted
				if !p.typed {
					if p.peers == nil {
						p.peers = &Transcript{hear: t.hear, peer: true}
					}
					p.peers.ask(asked)
				}
			}
			return true
		}
		switch {
		case wasInterrupted:
			t.Last = Interrupted
		case c.hasResult:
			t.Last = CalledTool
		case strings.HasPrefix(text, "<local-command-std"):
			t.Last = cmp.Or(before, t.Last)
		case prompt != "" && !l.IsMeta:
			if slashCommand.MatchString(text) {
				p.before = t.Last
			}
			t.Last = Prompted
			t.ask(prompt)
			p.typed, p.peers = true, nil
		}
	case "assistant":
		if u := l.Message.Usage; u != nil && !l.IsSidechain {
			if window := claudeWindow(l.Message.Model); window > 0 {
				t.ContextUsed, t.ContextWindow = u.Input+u.CacheCreate+u.CacheRead, window
			}
		}
		c := decodeContent(l.Message.Content)
		t.ToolCalls += c.toolUses
		t.Last, p.before = Replied, ""
		if c.toolUses > 0 {
			t.Last = CalledTool
		}
		if text := strings.TrimSpace(c.text); text != "" && !l.IsMeta {
			t.say("assistant", text)
			if p.peers != nil {
				p.peers.say("assistant", text)
			}
		}
	case "system":
		if l.Subtype == "compact_boundary" {
			if m := l.CompactMetadata; m.Trigger == "auto" && m.PreTokens > 0 {
				p.autoCompactedAt = m.PreTokens
			}
			if t.ContextWindow > 0 {
				t.ContextUsed = l.CompactMetadata.PostTokens
			}
		}
	case "custom-title":
		t.CustomTitle = cmp.Or(l.CustomTitle, t.CustomTitle)
	case "ai-title":
		t.AITitle = cmp.Or(l.AITitle, t.AITitle)
	case "last-prompt":
		t.LastPrompt = cmp.Or(l.LastPrompt, t.LastPrompt)
	case "relocated":
		p.relocated = cmp.Or(l.RelocatedCwd, p.relocated)
	}
	return true
}

func (p *parser) done() Transcript {
	if p.autoCompactedAt > 0 && p.autoCompactedAt < smallWindowCompaction && p.t.ContextWindow == millionTokens {
		p.t.ContextWindow = olderWindow
	}
	if p.relocated != "" {
		p.t.Cwd = p.relocated
	}
	p.t.join()
	if !p.typed && p.peers != nil {
		p.peers.join()
		p.t.Prompts, p.t.Turns, p.t.Messages = p.peers.Prompts, p.peers.Turns, p.peers.Messages
		p.t.ByAgent, p.t.PeerDriven = true, true
	}
	return p.t
}
