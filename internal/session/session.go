package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/open"
	"github.com/adammiribyan/superfind/internal/typesafe"
)

const (
	activeSeconds    = 120
	throwawayWords   = 150
	statusShownAt    = 0.85
	labelDays        = 14
	indexingNoticeAt = 20
	worktreeMark     = "/.claude/worktrees/"
)

type Session struct {
	ID         string      `json:"session_id"`
	Cwd        string      `json:"cwd"`
	Title      string      `json:"title"`
	LastPrompt string      `json:"last_prompt"`
	Branch     string      `json:"branch"`
	Prompts    int         `json:"prompts"`
	Words      int         `json:"words"`
	ToolCalls  int         `json:"tool_calls"`
	Mtime      float64     `json:"mtime"`
	ByAgent    bool        `json:"by_agent,omitempty"`
	Ending     agent.Event `json:"ending,omitempty"`
}

func (s Session) Project() string {
	root := strings.SplitN(s.Cwd, worktreeMark, 2)[0]
	if name := filepath.Base(root); name != "" && name != "/" && name != "." {
		return name
	}
	return root
}

func (s Session) Throwaway() bool {
	return s.Prompts <= 2 && s.Words < throwawayWords && s.ToolCalls == 0
}

func (s Session) Active() bool { return Now()-s.Mtime < activeSeconds }

type Row struct {
	Session    Session
	Agent      *agent.Agent
	Home       string
	Path       string
	Status     string
	FolderGone bool
	Open       *open.State
	Commits    int
	Threads    []string
}

func Annotate(rows []Row, held map[open.Key]open.State) []Row {
	known, keyless := statuses(), typesafe.Key() == ""
	out := make([]Row, len(rows))
	for i, r := range rows {
		r.Status = statusLabel(r, known, keyless)
		r.FolderGone = !folderExists(r.Session.Cwd)
		r.Open = nil
		out[i] = r
	}
	for i, r := range out {
		if state, ok := held[open.KeyOf(r.Agent.ID, r.Session.ID)]; ok {
			out[i].Open = &state
		}
	}
	return out
}

func Now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func IsHere(s Session) bool {
	if s.Cwd == "" {
		return false
	}
	cwd, pwd := filepath.Clean(s.Cwd), filepath.Clean(os.Getenv("PWD"))
	if cwd == pwd {
		return true
	}
	return cwd != filepath.Clean(agent.Home()) && strings.HasPrefix(pwd, cwd+"/")
}

func RecentFirst(rows []Row) []Row {
	out := append([]Row(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Session, out[j].Session
		if a.Throwaway() != b.Throwaway() {
			return !a.Throwaway()
		}
		if IsHere(a) != IsHere(b) {
			return IsHere(a)
		}
		if a.Mtime != b.Mtime {
			return a.Mtime > b.Mtime
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func InOrder(rows []Row, paths []string) []Row {
	byPath := map[string]Row{}
	for _, r := range rows {
		byPath[r.Path] = r
	}
	var out []Row
	for _, p := range paths {
		if r, ok := byPath[p]; ok {
			out = append(out, r)
		}
	}
	return out
}

func folderExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}
