package agent

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Edit struct {
	Path string
	At   float64
}

type Commit struct {
	Command string
	SHA     string
	At      float64
	Dir     string
}

type PR struct {
	Repo   string
	Number int
}

type Trail struct {
	Edits   []Edit
	Commits []Commit
	PRs     []PR
}

var (
	gitCommit    = regexp.MustCompile(`(?:^|[\n;&|({\x60]|\bthen\b|\bdo\b|\b(?:ba|z)?sh\s+-l?c\s+["'\x60]?|cmd["']?\s*:\s*["'\x60])\s*(?:\w+=\S*\s+)*git(?:\s+-[cC]\s+\S+)*\s+commit\b`)
	committedSHA = regexp.MustCompile(`\[[^\]\n]*? ([0-9a-f]{7,40})\]`)
	pullURL      = regexp.MustCompile(`github\.com/([\w.-]+/[\w.-]+)/pull/(\d+)`)
	patchedFile  = regexp.MustCompile(`\*\*\* (?:Add|Update|Delete) File: ([^\n]+)|\*\*\* Move to: ([^\n]+)`)
	exitCode     = regexp.MustCompile(`Process exited with code (-?\d+)`)
	stillRunning = regexp.MustCompile(`Process running with session ID (\d+)`)
)

type pending struct {
	command string
	edits   []Edit
	process string
}

func unquoted(s string) string {
	b, open := []byte(s), byte(0)
	for i, c := range []byte(s) {
		switch {
		case open != 0 && c == open && (open == '\'' || s[i-1] != '\\'):
			open = 0
		case open != 0:
			b[i] = ' '
		case c == '"' || c == '\'':
			open = c
		}
	}
	return string(b)
}

func commits(command string) bool {
	at := gitCommit.FindStringIndex(command)
	if at == nil {
		return false
	}
	rest := unquoted(command[at[1]:])
	if end := strings.IndexAny(rest, ";|&\n"); end >= 0 {
		rest = rest[:end]
	}
	return !strings.Contains(rest, "--dry-run")
}

func chained(command string) bool {
	at := gitCommit.FindStringIndex(command)
	return at != nil && strings.ContainsAny(unquoted(command[at[1]:]), ";|&\n")
}

func (t *Trail) ran(command, dir string, at float64) bool {
	if commits(command) {
		t.Commits = append(t.Commits, Commit{Command: command, At: at, Dir: dir})
		return true
	}
	return strings.Contains(command, "gh pr create")
}

func (t *Trail) saw(command, sha string) {
	for i := len(t.Commits) - 1; i >= 0 && sha != ""; i-- {
		if t.Commits[i].Command == command && t.Commits[i].SHA == "" {
			t.Commits[i].SHA = sha
			return
		}
	}
}

func (t *Trail) answered(p pending, output, sha string, failed bool) {
	if all := committedSHA.FindAllStringSubmatch(output, -1); len(all) > 0 && sha == "" {
		sha = all[len(all)-1][1]
	}
	if commits(p.command) && sha != "" {
		t.saw(p.command, sha)
	} else if commits(p.command) && failed && !chained(p.command) {
		for i := len(t.Commits) - 1; i >= 0; i-- {
			if t.Commits[i].Command == p.command && t.Commits[i].SHA == "" {
				t.Commits = append(t.Commits[:i], t.Commits[i+1:]...)
				break
			}
		}
	}
	if failed {
		return
	}
	t.Edits = append(t.Edits, p.edits...)
	if strings.Contains(p.command, "gh pr create") {
		for _, m := range pullURL.FindAllStringSubmatch(output, -1) {
			t.opened(m[1], m[2])
		}
	}
}

func (t *Trail) opened(repo, number string) {
	if n, err := strconv.Atoi(number); err == nil && repo != "" {
		t.PRs = append(t.PRs, PR{repo, n})
	}
}

func stamp(s string) float64 {
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return float64(at.UnixNano()) / 1e9
}

func ReadTrail(a *Agent, path string, needles [][]byte, threads ...string) (Trail, error) {
	var t Trail
	paths := []string{path}
	if a.parts != nil {
		paths = append(paths, a.parts(path)...)
	}
	paths = append(paths, threads...)
	var problems []error
	for _, p := range paths {
		found, err := mentions(p, needles)
		if err == nil && found {
			err = eachLine(p, a.trail(&t))
		}
		problems = append(problems, err)
	}
	return t, errors.Join(problems...)
}

func mentions(path string, needles [][]byte) (bool, error) {
	if needles == nil {
		return true, nil
	}
	f, err := openFile(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	overlap := 0
	for _, n := range needles {
		overlap = max(overlap, len(n)-1)
	}
	buf, carried := make([]byte, 1<<20+overlap), 0
	for {
		n, err := io.ReadFull(f, buf[carried:])
		window := buf[:carried+n]
		for _, needle := range needles {
			if len(needle) > 0 && bytes.Contains(window, needle) {
				return true, nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return false, nil
			}
			return false, err
		}
		carried = copy(buf, window[len(window)-min(overlap, len(window)):])
	}
}

func ParentOf(a *Agent, path string) string {
	if a.parent == nil {
		return ""
	}
	return a.parent(path)
}

func codexParent(path string) string {
	parent := ""
	eachLine(path, func(raw []byte) bool {
		var l codexLine
		var item codexItem
		if json.Unmarshal(raw, &l) == nil && l.Type == "session_meta" && json.Unmarshal(l.Payload, &item) == nil {
			parent = item.ParentThreadID
		}
		return false
	})
	return parent
}

func claudeParts(path string) []string {
	found, _ := filepath.Glob(filepath.Join(globMeta.Replace(strings.TrimSuffix(path, ".jsonl")), "subagents", "*.jsonl"))
	sort.Strings(found)
	return found
}

type claudeTrailLine struct {
	Type          string `json:"type"`
	Timestamp     string `json:"timestamp"`
	PRRepository  string `json:"prRepository"`
	PRNumber      int    `json:"prNumber"`
	ToolUseResult struct {
		GitOperation struct {
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		} `json:"gitOperation"`
	} `json:"toolUseResult"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeTrailBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
	Input     struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Command      string `json:"command"`
	} `json:"input"`
}

var claudeEditors = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

var editMarks = [][]byte{[]byte(`"Edit"`), []byte(`"Write"`), []byte(`"MultiEdit"`), []byte(`"NotebookEdit"`), []byte(`"FileChange"`),
	[]byte(`"search_replace"`), []byte(`"write"`), []byte("*** ")}

func mayEdit(raw []byte) bool {
	for _, m := range editMarks {
		if bytes.Contains(raw, m) {
			return true
		}
	}
	return false
}

func mayCommit(raw []byte) bool {
	return bytes.Contains(raw, []byte("commit")) || bytes.Contains(raw, []byte("pr create"))
}

func awaited(raw []byte, waiting map[string]pending) bool {
	for id := range waiting {
		if bytes.Contains(raw, []byte(id)) {
			return true
		}
	}
	return false
}

func claudeTrail(t *Trail) func([]byte) bool {
	waiting := map[string]pending{}
	return func(raw []byte) bool {
		tool := bytes.Contains(raw, []byte(`"tool_use"`)) && (mayEdit(raw) || mayCommit(raw))
		link := len(raw) < 4096 && bytes.Contains(raw, []byte(`"pr-link"`))
		if !tool && !link && !awaited(raw, waiting) {
			return true
		}
		var l claudeTrailLine
		if json.Unmarshal(raw, &l) != nil {
			return true
		}
		at := stamp(l.Timestamp)
		if l.Type == "pr-link" {
			t.opened(l.PRRepository, strconv.Itoa(l.PRNumber))
			return true
		}
		var blocks []claudeTrailBlock
		json.Unmarshal(l.Message.Content, &blocks)
		for _, b := range blocks {
			switch {
			case b.Type == "tool_use" && claudeEditors[b.Name]:
				if p := b.Input.FilePath + b.Input.NotebookPath; p != "" {
					waiting[b.ID] = pending{edits: []Edit{{p, at}}}
				}
			case b.Type == "tool_use" && b.Name == "Bash":
				if t.ran(b.Input.Command, "", at) {
					waiting[b.ID] = pending{command: b.Input.Command}
				}
			case b.Type == "tool_result":
				if p, ok := waiting[b.ToolUseID]; ok {
					delete(waiting, b.ToolUseID)
					t.answered(p, decodeContent(b.Content).text, l.ToolUseResult.GitOperation.Commit.SHA, b.IsError)
				}
			}
		}
		return true
	}
}

func plainText(raw json.RawMessage) string {
	var s string
	json.Unmarshal(raw, &s)
	return s
}

type codexTrailItem struct {
	Type      string          `json:"type"`
	Cwd       string          `json:"cwd"`
	Name      string          `json:"name"`
	CallID    string          `json:"call_id"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	Output    json.RawMessage `json:"output"`
	Item      struct {
		Type     string                     `json:"type"`
		ID       string                     `json:"id"`
		Status   string                     `json:"status"`
		Changes  map[string]json.RawMessage `json:"changes"`
		Command  json.RawMessage            `json:"command"`
		Output   string                     `json:"aggregated_output"`
		ExitCode *int                       `json:"exit_code"`
	} `json:"item"`
}

type codexTrailLine struct {
	Timestamp string         `json:"timestamp"`
	Type      string         `json:"type"`
	Payload   codexTrailItem `json:"payload"`
}

type codexArguments struct {
	Cmd       string          `json:"cmd"`
	Command   json.RawMessage `json:"command"`
	Input     string          `json:"input"`
	SessionID json.Number     `json:"session_id"`
	Workdir   string          `json:"workdir"`
}

func codexTrail(t *Trail) func([]byte) bool {
	waiting, running := map[string]pending{}, map[string]pending{}
	cwd := ""
	return func(raw []byte) bool {
		if !codexMatters(raw, waiting, len(running) > 0) {
			return true
		}
		var l codexTrailLine
		if json.Unmarshal(raw, &l) != nil {
			return true
		}
		at, p := stamp(l.Timestamp), l.Payload
		switch {
		case l.Type == "session_meta" || l.Type == "turn_context":
			if p.Cwd != "" {
				cwd = p.Cwd
			}
		case l.Type == "event_msg" && p.Type == "item_completed" && p.Item.Type == "FileChange":
			if w, ok := waiting[p.Item.ID]; ok {
				w.edits = nil
				waiting[p.Item.ID] = w
			}
			for path := range p.Item.Changes {
				if p.Item.Status != "failed" {
					t.Edits = append(t.Edits, Edit{path, at})
				}
			}
		case l.Type == "event_msg" && p.Type == "item_completed" && p.Item.Type == "CommandExecution":
			command := commandText(p.Item.Command)
			if t.ran(command, cwd, at) {
				t.answered(pending{command: command}, p.Item.Output, "", p.Item.ExitCode != nil && *p.Item.ExitCode != 0)
			}
		case l.Type == "response_item" && (p.Type == "function_call" || p.Type == "custom_tool_call"):
			var args codexArguments
			input := p.Input
			if json.Unmarshal([]byte(p.Arguments), &args) == nil {
				input = strings.Join(slices.DeleteFunc([]string{args.Cmd, commandText(args.Command), args.Input}, func(s string) bool { return s == "" }), "\n")
			} else if p.Arguments != "" {
				input = p.Arguments
			}
			if w, ok := running[args.SessionID.String()]; ok && p.Name == "write_stdin" {
				waiting[p.CallID] = w
				break
			}
			var w pending
			base := cmp.Or(args.Workdir, cwd)
			if p.Name != "exec" && (p.Name == "apply_patch" || strings.Contains(input, "apply_patch")) {
				for _, m := range patchedFile.FindAllStringSubmatch(input, -1) {
					if path := strings.TrimSpace(m[1] + m[2]); path != "" {
						if !filepath.IsAbs(path) {
							path = filepath.Join(base, path)
						}
						w.edits = append(w.edits, Edit{path, at})
					}
				}
			}
			if p.Name != "apply_patch" && t.ran(input, base, at) {
				w.command = input
			}
			if w.command != "" || len(w.edits) > 0 {
				waiting[p.CallID] = w
			}
		case l.Type == "response_item" && (p.Type == "function_call_output" || p.Type == "custom_tool_call_output"):
			w, ok := waiting[p.CallID]
			if !ok {
				break
			}
			delete(waiting, p.CallID)
			output := plainText(p.Output)
			if output == "" {
				output = string(p.Output)
			}
			if m := stillRunning.FindStringSubmatch(output); m != nil {
				w.process = m[1]
				running[m[1]] = w
				break
			}
			delete(running, w.process)
			code := exitCode.FindStringSubmatch(output)
			t.answered(w, output, "", (code != nil && code[1] != "0") || strings.HasPrefix(output, "apply_patch verification failed"))
		}
		return true
	}
}

func codexMatters(raw []byte, waiting map[string]pending, polling bool) bool {
	head := raw[:min(len(raw), 320)]
	has := func(b []byte, s string) bool { return bytes.Contains(b, []byte(s)) }
	switch {
	case has(head, `"session_meta"`) || has(head, `"turn_context"`):
		return true
	case has(head, `_call_output"`):
		return awaited(raw, waiting)
	case has(head, `"function_call"`) || has(head, `"custom_tool_call"`):
		return mayCommit(raw) || has(raw, "apply_patch") || (polling && has(head, "write_stdin"))
	case has(head, `"item_completed"`):
		return has(head, `"FileChange"`) || (has(head, `"CommandExecution"`) && mayCommit(raw))
	}
	return false
}

func commandText(raw json.RawMessage) string {
	var words []string
	if json.Unmarshal(raw, &words) == nil {
		for i, w := range words {
			if strings.ContainsAny(w, " \t\n") {
				words[i] = ShellQuote(w)
			}
		}
		return strings.Join(words, " ")
	}
	return plainText(raw)
}

type grokTrailLine struct {
	Type       string          `json:"type"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCalls  []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"tool_calls"`
}

func grokTrail(t *Trail) func([]byte) bool {
	waiting := map[string]pending{}
	return func(raw []byte) bool {
		if !mayEdit(raw) && !mayCommit(raw) && !awaited(raw, waiting) {
			return true
		}
		var l grokTrailLine
		if json.Unmarshal(raw, &l) != nil {
			return true
		}
		for _, c := range l.ToolCalls {
			var args struct {
				FilePath string `json:"file_path"`
				Command  string `json:"command"`
			}
			json.Unmarshal([]byte(c.Arguments), &args)
			switch c.Name {
			case "search_replace", "write":
				if args.FilePath != "" {
					t.Edits = append(t.Edits, Edit{args.FilePath, 0})
				}
			case "run_terminal_command":
				if t.ran(args.Command, "", 0) {
					waiting[c.ID] = pending{command: args.Command}
				}
			}
		}
		if w, ok := waiting[l.ToolCallID]; ok && l.Type == "tool_result" {
			delete(waiting, l.ToolCallID)
			t.answered(w, grokText(l.Content), "", false)
		}
		return true
	}
}
