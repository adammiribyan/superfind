package agent

import (
	"bytes"
	"cmp"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

type Step struct {
	Message  int
	Edit     string
	Error    string
	Subagent bool
}

func (p *parser) said() int      { return len(p.t.Messages) }
func (c *codexReader) said() int { return len(c.t.Messages) }
func (g *grokReader) said() int  { return len(g.t.Messages) }

func ReadSteps(a *Agent, path string) (Transcript, []Step, error) {
	r, err := a.reader(path, Transcript{Last: Replied})
	if err != nil {
		return Transcript{}, nil, err
	}
	var trail Trail
	follow, failed := a.trail(&trail), failures[a.ID]
	var steps []Step
	err = eachLine(path, func(raw []byte) bool {
		if !r.feed(raw) {
			return false
		}
		edits := len(trail.Edits)
		follow(raw)
		at := r.said()
		for _, e := range trail.Edits[edits:] {
			steps = append(steps, Step{Message: at, Edit: e.Path})
		}
		if failed != nil {
			for _, output := range failed(raw) {
				steps = append(steps, Step{Message: at, Error: output})
			}
		}
		if a.ID == "claude" {
			for _, sub := range claudeSubagent(raw, path) {
				var t Trail
				eachLine(sub, a.trail(&t))
				for _, e := range t.Edits {
					steps = append(steps, Step{Message: at, Edit: e.Path, Subagent: true})
				}
			}
		}
		return true
	})
	t := r.done()
	if t.PeerDriven {
		steps = nil
	}
	return t, steps, err
}

var agentID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func claudeSubagent(raw []byte, path string) []string {
	if !bytes.Contains(raw, []byte(`"agentId"`)) {
		return nil
	}
	var l struct {
		ToolUseResult struct {
			AgentID string `json:"agentId"`
		} `json:"toolUseResult"`
	}
	if json.Unmarshal(raw, &l) != nil || !agentID.MatchString(l.ToolUseResult.AgentID) {
		return nil
	}
	return []string{filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "agent-"+l.ToolUseResult.AgentID+".jsonl")}
}

var failures = map[string]func(raw []byte) []string{"claude": claudeFailures, "codex": codexFailures}

func claudeFailures(raw []byte) []string {
	if !bytes.Contains(raw, []byte(`"is_error":true`)) {
		return nil
	}
	var l claudeTrailLine
	if json.Unmarshal(raw, &l) != nil {
		return nil
	}
	var blocks []claudeTrailBlock
	json.Unmarshal(l.Message.Content, &blocks)
	var found []string
	for _, b := range blocks {
		if b.Type == "tool_result" && b.IsError {
			found = append(found, cmp.Or(strings.TrimSpace(decodeContent(b.Content).text), "the tool call failed"))
		}
	}
	return found
}

func codexFailures(raw []byte) []string {
	head := raw[:min(len(raw), 320)]
	output := bytes.Contains(head, []byte(`_call_output"`))
	completed := bytes.Contains(head, []byte(`"item_completed"`))
	if !output && !completed {
		return nil
	}
	var l codexTrailLine
	if json.Unmarshal(raw, &l) != nil {
		return nil
	}
	p := l.Payload
	switch {
	case completed && p.Item.Type == "CommandExecution" && (p.Item.Status == "failed" || (p.Item.ExitCode != nil && *p.Item.ExitCode != 0)):
		return []string{cmp.Or(strings.TrimSpace(p.Item.Output), "the command failed")}
	case completed && p.Item.Type == "FileChange" && p.Item.Status == "failed":
		return []string{"the edit failed"}
	case output:
		text := plainText(p.Output)
		if text == "" {
			text = string(p.Output)
		}
		if code := exitCode.FindStringSubmatch(text); (code != nil && code[1] != "0") || strings.HasPrefix(text, "apply_patch verification failed") {
			return []string{strings.TrimSpace(text)}
		}
	}
	return nil
}
