package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
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
	ID         string  `json:"session_id"`
	Cwd        string  `json:"cwd"`
	Title      string  `json:"title"`
	LastPrompt string  `json:"last_prompt"`
	Branch     string  `json:"branch"`
	Prompts    int     `json:"prompts"`
	Words      int     `json:"words"`
	ToolCalls  int     `json:"tool_calls"`
	Mtime      float64 `json:"mtime"`
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
}

func Annotate(rows []Row) []Row {
	known := statuses()
	out := make([]Row, len(rows))
	for i, r := range rows {
		r.Status = statusLabel(r, known)
		r.FolderGone = !folderExists(r.Session.Cwd)
		out[i] = r
	}
	return out
}

func Now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func CacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "superfind")
	}
	return filepath.Join(agent.Home(), ".cache", "superfind")
}

func CachePath(name string) string { return filepath.Join(CacheDir(), name) }

func isHere(s Session) bool { return s.Cwd == os.Getenv("PWD") }

func RecentFirst(rows []Row) []Row {
	out := append([]Row(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Session, out[j].Session
		if a.Throwaway() != b.Throwaway() {
			return !a.Throwaway()
		}
		if isHere(a) != isHere(b) {
			return isHere(a)
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
