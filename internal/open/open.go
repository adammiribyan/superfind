package open

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/mask"
	"github.com/adammiribyan/superfind/internal/proc"
	"github.com/adammiribyan/superfind/internal/rex"
)

type Pane struct {
	ID      string `json:"pane"`
	Session string `json:"session"`
	Window  int    `json:"window"`
}

type State struct {
	PID    int       `json:"pid"`
	Status string    `json:"status"`
	Tmux   *Pane     `json:"tmux"`
	Rex    *rex.Pane `json:"rex,omitempty"`
	Need   string    `json:"need,omitempty"`
	Doing  string    `json:"doing,omitempty"`
	TTY    proc.TTY  `json:"-"`
}

func (s State) Equal(t State) bool {
	return s.PID == t.PID && s.Status == t.Status && s.Need == t.Need && s.Doing == t.Doing && s.TTY == t.TTY &&
		samePane(s.Tmux, t.Tmux) && samePane(s.Rex, t.Rex)
}

func samePane[P comparable](a, b *P) bool { return a == b || a != nil && b != nil && *a == *b }

type Reach int

const (
	NotOpen Reach = iota
	InPane
	Focused
	Elsewhere
)

func Reaching(s *State, inTmux bool, focus func(State) bool) Reach {
	switch {
	case s == nil:
		return NotOpen
	case s.Tmux != nil && inTmux:
		return InPane
	case focus(*s):
		return Focused
	}
	return Elsewhere
}

type Key struct{ Agent, ID string }

func KeyOf(agent, id string) Key { return Key{agent, strings.ToLower(id)} }

type claim struct {
	key   Key
	state State
	tty   proc.TTY
}

type evidence struct {
	registry func(home string) []claim
	resumed  func(args []string) string
}

var evidenceFor = map[string]evidence{
	"claude": {registry: claudeClaims, resumed: flagResumed},
	"codex":  {resumed: codexResumed},
	"grok":   {registry: grokClaims, resumed: grokResumed},
}

const (
	clockSlack    = time.Second
	tmuxTimeout   = 500 * time.Millisecond
	registryLimit = 1 << 20
)

func Find(places []agent.Place) map[Key]State {
	if len(places) == 0 {
		return map[Key]State{}
	}
	listed := make(chan map[proc.TTY]Pane, 1)
	go func() { listed <- tmuxPanes() }()
	snapped := make(chan map[int]rex.Held, 1)
	go func() { snapped <- rex.Snapshot() }()
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
			if id := resumedID(name, proc.Args(p.PID)); id != "" {
				claims = append(claims, claim{KeyOf(name, id), State{PID: p.PID}, p.TTY})
			}
		}
	}
	if len(claims) > 0 {
		held := <-snapped
		for i := range claims {
			claims[i].state = inRex(claims[i].state, held)
		}
	}
	return withPanes(strongest(claims), listed)
}

var rexStatus = map[string]string{"working": "busy", "blocked": "waiting", "idle": "idle", "done": "idle", "error": "idle"}

func inRex(s State, held map[int]rex.Held) State {
	h, ok := held[s.PID]
	if !ok {
		return s
	}
	s.Rex = &h.Pane
	if a := h.Activity; a != nil {
		if status, known := rexStatus[a.State]; known {
			s.Status, s.Need = status, a.Need
		}
		s.Doing = mask.Secrets(strings.Join(strings.Fields(agent.Printable(a.Doing)), " "))
	}
	return s
}

func claudeClaims(home string) []claim {
	dir := filepath.Join(home, "sessions")
	entries, _ := os.ReadDir(dir)
	var out []claim
	for _, e := range entries {
		pid, ok := pidFileName(e.Name())
		if !ok {
			continue
		}
		raw, ok := readRegistry(filepath.Join(dir, e.Name()))
		if !ok {
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
	return claim{KeyOf("claude", f.SessionID), State{PID: f.PID, Status: statusOf(f.Status)}, p.TTY}, true
}

func readRegistry(path string) ([]byte, bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(f, registryLimit+1))
	return raw, err == nil && len(raw) <= registryLimit
}

func grokClaims(home string) []claim {
	raw, ok := readRegistry(filepath.Join(home, "active_sessions.json"))
	if !ok {
		return nil
	}
	var out []claim
	for _, a := range grokEntries(raw) {
		if p, ok := running(a.pid, a.opened); ok {
			out = append(out, claim{KeyOf("grok", a.id), State{PID: a.pid}, p.TTY})
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

var codexValueFlags = map[string]bool{
	"-c": true, "--config": true, "-m": true, "--model": true, "-p": true, "--profile": true,
	"--enable": true, "--disable": true, "-i": true, "--image": true, "-C": true, "--cd": true,
	"-s": true, "--sandbox": true, "-a": true, "--ask-for-approval": true,
}

func resumedID(name string, args []string) string {
	if len(args) == 0 || slices.Contains(args, "--fork-session") || slices.Contains(args, "fork") {
		return ""
	}
	return strings.ToLower(evidenceFor[name].resumed(args[1:]))
}

func flagResumed(args []string) string {
	for i, a := range args {
		if a == "--" {
			return ""
		}
		for _, flag := range []string{"--resume", "-r"} {
			if value, ok := strings.CutPrefix(a, flag+"="); ok {
				return idOf(value)
			}
			if a == flag && i+1 < len(args) {
				return idOf(args[i+1])
			}
		}
	}
	return ""
}

func grokResumed(args []string) string {
	if id := flagResumed(args); id != "" {
		return id
	}
	if i := slices.Index(args, "resume"); i >= 0 && i+1 < len(args) {
		return idOf(args[i+1])
	}
	return ""
}

func codexResumed(args []string) string {
	var positional []string
	for i := 0; i < len(args) && args[i] != "--"; i++ {
		switch {
		case codexValueFlags[args[i]]:
			i++
		case !strings.HasPrefix(args[i], "-"):
			positional = append(positional, args[i])
		}
	}
	if len(positional) == 0 || positional[0] != "resume" {
		return ""
	}
	if i := slices.IndexFunc(positional[1:], uuidShape.MatchString); i >= 0 {
		return positional[1+i]
	}
	return ""
}

func idOf(arg string) string {
	if uuidShape.MatchString(arg) {
		return arg
	}
	return ""
}

func strongest(claims []claim) map[Key]claim {
	rank := map[string]int{"waiting": 3, "busy": 2, "idle": 1}
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

func withPanes(claims map[Key]claim, listed <-chan map[proc.TTY]Pane) map[Key]State {
	var panes map[proc.TTY]Pane
	for _, c := range claims {
		if c.tty != (proc.TTY{}) {
			panes = <-listed
			break
		}
	}
	out := make(map[Key]State, len(claims))
	for key, c := range claims {
		if pane, ok := panes[c.tty]; ok {
			c.state.Tmux = &pane
		}
		c.state.TTY = c.tty
		out[key] = c.state
	}
	return out
}

func tmuxPanes() map[proc.TTY]Pane {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "-u", "list-panes", "-a", "-F",
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
