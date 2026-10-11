package agent

import (
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
)

type Agent struct {
	ID       string
	Label    string
	homeVar  string
	dotDir   string
	extraVar string
	glob     string
	sidecar  string
	resume   string
	IDOf     func(path string) string
	Names    func(home string) map[string]string
	reader   func(path string, start Transcript) (reader, error)
	trail    func(t *Trail) func(raw []byte) bool
	parts    func(path string) []string
	parent   func(path string) string
}

var Agents = []*Agent{
	{ID: "claude", Label: "Claude", homeVar: "CLAUDE_CONFIG_DIR", dotDir: ".claude", extraVar: "SUPERFIND_CLAUDE_DIRS",
		glob: "projects/*/*.jsonl", resume: "claude --resume", IDOf: stem,
		reader: func(_ string, start Transcript) (reader, error) { return &parser{t: start}, nil }, trail: claudeTrail, parts: claudeParts},
	{ID: "codex", Label: "Codex", homeVar: "CODEX_HOME", dotDir: ".codex", extraVar: "SUPERFIND_CODEX_DIRS",
		glob: "sessions/*/*/*/rollout-*.jsonl", resume: "codex resume", IDOf: codexID, Names: codexNames,
		reader: newCodexReader, trail: codexTrail, parent: codexParent},
	{ID: "grok", Label: "Grok", homeVar: "GROK_HOME", dotDir: ".grok", extraVar: "SUPERFIND_GROK_DIRS",
		glob: "sessions/*/*/chat_history.jsonl", sidecar: "summary.json", resume: "grok --resume", IDOf: grokID,
		reader: newGrokReader, trail: grokTrail},
}

func stem(path string) string { return strings.TrimSuffix(filepath.Base(path), ".jsonl") }

func (a *Agent) DefaultHome() string {
	if h := os.Getenv(a.homeVar); h != "" {
		return h
	}
	return filepath.Join(Home(), a.dotDir)
}

func (a *Agent) homes() []string {
	var candidates []string
	if h := os.Getenv(a.homeVar); h != "" {
		candidates = append(candidates, h)
	}
	candidates = append(candidates, filepath.Join(Home(), a.dotDir))
	extras, _ := filepath.Glob(filepath.Join(globMeta.Replace(Home()), a.dotDir+"-*"))
	sort.Strings(extras)
	candidates = append(candidates, extras...)
	for _, p := range strings.Split(os.Getenv(a.extraVar), ":") {
		if p != "" {
			candidates = append(candidates, p)
		}
	}
	marker := strings.SplitN(a.glob, "/", 2)[0]
	seen := map[string]bool{}
	var dirs []string
	for _, c := range candidates {
		d := Resolve(c)
		sessions := filepath.Join(d, marker)
		info, err := os.Stat(sessions)
		exists := (err == nil && info.IsDir()) || (err != nil && !errors.Is(err, fs.ErrNotExist) && dirExists(d))
		if key := Resolve(sessions); exists && !seen[key] {
			seen[key] = true
			dirs = append(dirs, d)
		}
	}
	return dirs
}

var globMeta = strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`)

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (a *Agent) Sessions(home string) ([]string, []error) {
	var paths []string
	var unreadable []error
	var walk func(dir string, parts []string)
	walk = func(dir string, parts []string) {
		var names []string
		if strings.ContainsAny(parts[0], "*?[") {
			entries, err := os.ReadDir(dir)
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					unreadable = append(unreadable, err)
				}
				return
			}
			for _, e := range entries {
				if matched, _ := filepath.Match(parts[0], e.Name()); matched {
					names = append(names, e.Name())
				}
			}
		} else {
			names = []string{parts[0]}
		}
		for _, name := range names {
			path := filepath.Join(dir, name)
			if len(parts) == 1 {
				if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
					paths = append(paths, path)
				}
			} else if info, err := os.Stat(path); err == nil && info.IsDir() {
				walk(path, parts[1:])
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				unreadable = append(unreadable, err)
			}
		}
	}
	walk(home, strings.Split(a.glob, "/"))
	return paths, unreadable
}

func (a *Agent) Stat(path string) (FileID, float64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileID{}, 0, err
	}
	id := FileID{info.ModTime().UnixNano(), info.Size()}
	if a.sidecar != "" {
		if side, err := os.Stat(filepath.Join(filepath.Dir(path), a.sidecar)); err == nil {
			id.ModNanos += side.ModTime().UnixNano()
			id.Size += side.Size()
		}
	}
	return id, float64(info.ModTime().UnixNano()) / 1e9, nil
}

func (a *Agent) Phrase(s string) string { return strings.ReplaceAll(s, "{agent}", a.Label) }

func Named(id string) *Agent {
	for _, a := range Agents {
		if a.ID == id {
			return a
		}
	}
	return nil
}

type Place struct {
	Agent *Agent
	Home  string
}

func Places() []Place {
	var out []Place
	for _, a := range Agents {
		for _, h := range a.homes() {
			out = append(out, Place{a, h})
		}
	}
	return out
}

func Locate(path string, in []Place) (Place, bool) {
	var best Place
	found := false
	for _, p := range in {
		rel, under := strings.CutPrefix(path, p.Home+"/")
		owned, _ := filepath.Match(p.Agent.glob, rel)
		if under && owned && (!found || len(p.Home) > len(best.Home)) {
			best, found = p, true
		}
	}
	return best, found
}

func Home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return ""
}

func Resolve(path string) string {
	if strings.HasPrefix(path, "~/") {
		path = filepath.Join(Home(), path[2:])
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

type FileID struct {
	ModNanos int64 `json:"mtime_ns"`
	Size     int64 `json:"size"`
}
