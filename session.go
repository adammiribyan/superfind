package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adammiribyan/hop/internal/agent"
)

const (
	version          = "0.9.0"
	cacheVersion     = 12
	activeSeconds    = 120
	throwawayWords   = 150
	statusShownAt    = 0.85
	labelDays        = 14
	indexingNoticeAt = 20
	worktreeMark     = "/.claude/worktrees/"
)

const (
	ansiAccent = "\033[34m"
	ansiDim    = "\033[2m"
	ansiBold   = "\033[1m"
	ansiReset  = "\033[0m"
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

func (s Session) Active() bool { return now()-s.Mtime < activeSeconds }

type Row struct {
	Session    Session
	Agent      *agent.Agent
	Home       string
	Path       string
	Status     string
	FolderGone bool
}

func annotate(rows []Row) []Row {
	known := statuses()
	out := make([]Row, len(rows))
	for i, r := range rows {
		r.Status = statusLabel(r, known)
		r.FolderGone = !folderExists(r.Session.Cwd)
		out[i] = r
	}
	return out
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func cacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "hop")
	}
	return filepath.Join(agent.Home(), ".cache", "hop")
}

func cachePath(name string) string { return filepath.Join(cacheDir(), name) }

func oneLine(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if displayWidth(text) <= width {
		return text
	}
	head, _ := cutToWidth(text, max(width-1, 0))
	return head + "…"
}

func tilde(path string) string {
	h := agent.Home()
	if path == h || strings.HasPrefix(path, h+"/") {
		return "~" + path[len(h):]
	}
	return path
}

func ago(mtime float64) string {
	seconds := now() - mtime
	switch {
	case seconds < 60:
		return "just now"
	case seconds < 3600:
		return fmt.Sprintf("%dm ago", int(seconds/60))
	case seconds < 86400:
		return fmt.Sprintf("%dh ago", int(seconds/3600))
	case seconds < 7*86400:
		return fmt.Sprintf("%dd ago", int(seconds/86400))
	}
	t := time.Unix(int64(mtime), 0)
	if t.Year() == time.Now().Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}

func when(s Session) string {
	if s.Active() {
		return "active now"
	}
	return ago(s.Mtime)
}

func isHere(s Session) bool { return s.Cwd == os.Getenv("PWD") }

func recentFirst(rows []Row) []Row {
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

func inOrder(rows []Row, paths []string) []Row {
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

type rowText struct {
	title, waiting string
	context        []string
}

func rowTextOf(r Row, shown tags) rowText {
	t := rowText{title: strings.Join(strings.Fields(r.Session.Title), " ")}
	if r.Status == waitingLabel {
		t.waiting = r.Status
	} else if r.Status != "" {
		t.context = append(t.context, r.Status)
	}
	t.context = append(t.context, when(r.Session), r.Session.Project())
	if r.FolderGone {
		t.context = append(t.context, "folder removed")
	}
	t.context = append(t.context, shown.of(r)...)
	return t
}

func metaParts(r Row, shown tags) []string {
	s := r.Session
	parts := []string{s.Project()}
	if r.Status != "" {
		parts = append(parts, r.Status)
	}
	parts = append(parts, when(s))
	if s.Branch != "" && s.Branch != "HEAD" {
		parts = append(parts, s.Branch)
	}
	noun := "prompts"
	if s.Prompts == 1 {
		noun = "prompt"
	}
	parts = append(parts, fmt.Sprintf("%d %s", s.Prompts, noun))
	return append(parts, shown.of(r)...)
}
