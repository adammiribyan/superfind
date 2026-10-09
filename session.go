package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	version          = "0.7.0"
	cacheVersion     = 10
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
	Session   Session
	ConfigDir string
	Path      string
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func cacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "hop")
	}
	return filepath.Join(home(), ".cache", "hop")
}

func cachePath(name string) string { return filepath.Join(cacheDir(), name) }

func resolve(path string) string {
	if strings.HasPrefix(path, "~/") {
		path = filepath.Join(home(), path[2:])
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

func configDirs() []string {
	var candidates []string
	if c := os.Getenv("CLAUDE_CONFIG_DIR"); c != "" {
		candidates = append(candidates, c)
	}
	candidates = append(candidates, filepath.Join(home(), ".claude"))
	extras, _ := filepath.Glob(filepath.Join(home(), ".claude-*"))
	sort.Strings(extras)
	candidates = append(candidates, extras...)
	for _, p := range strings.Split(os.Getenv("HOP_CLAUDE_DIRS"), ":") {
		if p != "" {
			candidates = append(candidates, p)
		}
	}
	seen := map[string]bool{}
	var dirs []string
	for _, c := range candidates {
		d := resolve(c)
		if info, err := os.Stat(filepath.Join(d, "projects")); err == nil && info.IsDir() && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	return dirs
}

func configDirOf(path string, dirs []string) string {
	for _, d := range dirs {
		if strings.HasPrefix(path, d+"/") {
			return d
		}
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(path)))
}

func stamp(info os.FileInfo) float64 {
	return float64(info.ModTime().UnixNano()) / 1e9
}

func sameFile(mtime float64, size int64, info os.FileInfo) bool {
	const pythonFloatRounding = 1e-6
	return info != nil && size == info.Size() && math.Abs(mtime-stamp(info)) < pythonFloatRounding
}

func oneLine(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= width {
		return text
	}
	r := []rune(text)
	return string(r[:max(width-1, 0)]) + "…"
}

func tilde(path string) string {
	h := home()
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

func survivingDir(dir string) string {
	for {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

func folderExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func metaParts(s Session, configDir string, showAccount bool, status string) []string {
	parts := []string{s.Project()}
	if status != "" {
		parts = append(parts, status)
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
	if showAccount {
		parts = append(parts, filepath.Base(configDir))
	}
	return parts
}
