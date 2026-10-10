package open

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/proc"
)

type Pane struct {
	ID      string `json:"pane"`
	Session string `json:"session"`
	Window  int    `json:"window"`
}

type State struct {
	PID    int    `json:"pid"`
	Status string `json:"status"`
	Tmux   *Pane  `json:"tmux"`
}

type Key struct{ Agent, ID string }

type claim struct {
	key   Key
	state State
	tty   proc.TTY
}

type evidence struct {
	registry func(home string) []claim
	resume   []string
}

var evidenceFor = map[string]evidence{
	"claude": {registry: claudeClaims, resume: []string{"--resume", "-r"}},
	"codex":  {resume: []string{"resume"}},
	"grok":   {registry: grokClaims, resume: []string{"--resume", "-r", "resume"}},
}

const (
	clockSlack  = time.Second
	tmuxTimeout = 500 * time.Millisecond
)

func Find(places []agent.Place) map[Key]State {
	var claims []claim
	present := map[string]bool{}
	for _, p := range places {
		present[p.Agent.ID] = true
		if e := evidenceFor[p.Agent.ID]; e.registry != nil {
			claims = append(claims, e.registry(p.Home)...)
		}
	}
	registered := map[int]bool{}
	for _, c := range claims {
		registered[c.state.PID] = true
	}
	var names []string
	for _, a := range agent.Agents {
		if present[a.ID] {
			names = append(names, a.ID)
		}
	}
	for _, p := range proc.Named(names...) {
		if registered[p.PID] {
			continue
		}
		for _, name := range names {
			if !strings.HasPrefix(p.Name, name) {
				continue
			}
			if id := resumedID(proc.Args(p.PID), evidenceFor[name].resume); id != "" {
				claims = append(claims, claim{Key{name, id}, State{PID: p.PID}, p.TTY})
			}
		}
	}
	return withPanes(strongest(claims))
}

func claudeClaims(home string) []claim {
	dir := filepath.Join(home, "sessions")
	entries, _ := os.ReadDir(dir)
	var out []claim
	for _, e := range entries {
		pid, ok := pidFileName(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if c, ok := claudeClaim(raw, pid); ok {
			out = append(out, c)
		}
	}
	return out
}

var pidFile = regexp.MustCompile(`^([0-9]+)\.json$`)

func pidFileName(name string) (int, bool) {
	m := pidFile.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	pid, err := strconv.Atoi(m[1])
	return pid, err == nil
}

func claudeClaim(raw []byte, pid int) (claim, bool) {
	var f struct {
		PID       int    `json:"pid"`
		SessionID string `json:"sessionId"`
		StartedAt int64  `json:"startedAt"`
		Status    string `json:"status"`
	}
	if json.Unmarshal(raw, &f) != nil || f.PID <= 0 || f.PID != pid || f.SessionID == "" || f.StartedAt == 0 {
		return claim{}, false
	}
	p, ok := running(f.PID, time.UnixMilli(f.StartedAt))
	if !ok {
		return claim{}, false
	}
	return claim{Key{"claude", f.SessionID}, State{PID: f.PID, Status: statusOf(f.Status)}, p.TTY}, true
}

func grokClaims(home string) []claim {
	path := filepath.Join(home, "active_sessions.json")
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []claim
	for _, a := range grokEntries(raw) {
		if p, ok := running(a.pid, a.opened); ok {
			out = append(out, claim{Key{"grok", a.id}, State{PID: a.pid}, p.TTY})
		}
	}
	return out
}

type grokEntry struct {
	id     string
	pid    int
	opened time.Time
}

func grokEntries(raw []byte) []grokEntry {
	var list []struct {
		SessionID string          `json:"session_id"`
		PID       int             `json:"pid"`
		OpenedAt  json.RawMessage `json:"opened_at"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	var out []grokEntry
	for _, e := range list {
		opened, ok := timeOf(e.OpenedAt)
		if ok && e.SessionID != "" && e.PID > 0 {
			out = append(out, grokEntry{e.SessionID, e.PID, opened})
		}
	}
	return out
}

func timeOf(raw json.RawMessage) (time.Time, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		t, err := time.Parse(time.RFC3339Nano, text)
		return t, err == nil
	}
	var n float64
	if json.Unmarshal(raw, &n) != nil || n <= 0 {
		return time.Time{}, false
	}
	if n > 1e12 {
		return time.UnixMilli(int64(n)), true
	}
	return time.Unix(int64(n), 0), true
}

func running(pid int, written time.Time) (proc.Process, bool) {
	p, ok := proc.Lookup(pid)
	return p, ok && !p.Start.After(written.Add(clockSlack))
}

func statusOf(s string) string {
	if s == "busy" || s == "idle" {
		return s
	}
	return ""
}

var uuidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func resumedID(args []string, resume []string) string {
	if slices.Contains(args, "--fork-session") || slices.Contains(args, "fork") {
		return ""
	}
	for i, a := range args {
		for _, word := range resume {
			if value, ok := strings.CutPrefix(a, word+"="); ok && strings.HasPrefix(word, "-") {
				return firstID(append([]string{value}, args[i+1:]...))
			}
			if a == word {
				return firstID(args[i+1:])
			}
		}
	}
	return ""
}

func firstID(args []string) string {
	if i := slices.IndexFunc(args, uuidShape.MatchString); i >= 0 {
		return args[i]
	}
	return ""
}

func strongest(claims []claim) map[Key]claim {
	rank := map[string]int{"busy": 2, "idle": 1}
	best := map[Key]claim{}
	for _, c := range claims {
		held, seen := best[c.key]
		stronger := rank[c.state.Status] > rank[held.state.Status] ||
			rank[c.state.Status] == rank[held.state.Status] && c.state.PID < held.state.PID
		if !seen || stronger {
			best[c.key] = c
		}
	}
	return best
}

func withPanes(claims map[Key]claim) map[Key]State {
	var panes map[proc.TTY]Pane
	for _, c := range claims {
		if c.tty != (proc.TTY{}) {
			panes = tmuxPanes()
			break
		}
	}
	out := make(map[Key]State, len(claims))
	for key, c := range claims {
		if pane, ok := panes[c.tty]; ok {
			c.state.Tmux = &pane
		}
		out[key] = c.state
	}
	return out
}

func tmuxPanes() map[proc.TTY]Pane {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F",
		"#{pane_tty}\t#{pane_id}\t#{window_index}\t#{session_name}").Output()
	if err != nil {
		return map[proc.TTY]Pane{}
	}
	return parsePanes(out, proc.TerminalOf)
}

func parsePanes(out []byte, terminalOf func(string) (proc.TTY, bool)) map[proc.TTY]Pane {
	panes := map[proc.TTY]Pane{}
	for _, line := range bytes.Split(out, []byte("\n")) {
		f := strings.SplitN(string(line), "\t", 4)
		if len(f) < 4 {
			continue
		}
		window, err := strconv.Atoi(f[2])
		tty, ok := terminalOf(f[0])
		if err == nil && ok {
			panes[tty] = Pane{ID: f[1], Session: f[3], Window: window}
		}
	}
	return panes
}
