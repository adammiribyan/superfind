package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type agent struct {
	id       string
	label    string
	homeVar  string
	dotDir   string
	extraVar string
	glob     string
	sidecar  string
	resume   string
	idOf     func(path string) string
	names    func(home string) map[string]string
	reader   func(path string) (reader, error)
}

var agents = []*agent{
	{id: "claude", label: "Claude", homeVar: "CLAUDE_CONFIG_DIR", dotDir: ".claude", extraVar: "HOP_CLAUDE_DIRS",
		glob: "projects/*/*.jsonl", resume: "claude --resume", idOf: stem,
		reader: func(string) (reader, error) { return newParser(), nil }},
}

func stem(path string) string { return strings.TrimSuffix(filepath.Base(path), ".jsonl") }

func (a *agent) defaultHome() string {
	if h := os.Getenv(a.homeVar); h != "" {
		return h
	}
	return filepath.Join(home(), a.dotDir)
}

func (a *agent) homes() []string {
	var candidates []string
	if h := os.Getenv(a.homeVar); h != "" {
		candidates = append(candidates, h)
	}
	candidates = append(candidates, filepath.Join(home(), a.dotDir))
	extras, _ := filepath.Glob(filepath.Join(home(), a.dotDir+"-*"))
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
		d := resolve(c)
		if info, err := os.Stat(filepath.Join(d, marker)); err == nil && info.IsDir() && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	return dirs
}

func (a *agent) sessions(home string) []string {
	matches, _ := filepath.Glob(filepath.Join(home, a.glob))
	return matches
}

func (a *agent) threadNames(home string) map[string]string {
	if a.names == nil {
		return nil
	}
	return a.names(home)
}

func (a *agent) stat(path string) (fileID, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileID{}, err
	}
	id := fileID{info.ModTime().UnixNano(), info.Size()}
	if a.sidecar != "" {
		if side, err := os.Stat(filepath.Join(filepath.Dir(path), a.sidecar)); err == nil {
			id.ModNanos = max(id.ModNanos, side.ModTime().UnixNano())
			id.Size += side.Size()
		}
	}
	return id, nil
}

func (a *agent) phrase(s string) string { return strings.ReplaceAll(s, "{agent}", a.label) }

type place struct {
	agent *agent
	home  string
}

func places() []place {
	var out []place
	for _, a := range agents {
		for _, h := range a.homes() {
			out = append(out, place{a, h})
		}
	}
	return out
}

func locate(path string, in []place) (place, bool) {
	var best place
	found := false
	for _, p := range in {
		if strings.HasPrefix(path, p.home+"/") && (!found || len(p.home) > len(best.home)) {
			best, found = p, true
		}
	}
	return best, found
}

func resumeCommand(r Row) string {
	a := r.Agent
	env := ""
	if resolve(r.Home) != resolve(a.defaultHome()) {
		env = a.homeVar + "=" + shellQuote(r.Home) + " "
	}
	return "cd " + shellQuote(survivingDir(r.Session.Cwd)) + " && " + env + a.resume + " " + shellQuote(r.Session.ID)
}

type tags struct {
	agents bool
	homes  map[*agent]bool
}

func tagsFor(rows []Row) tags {
	seen := map[*agent]map[string]bool{}
	for _, r := range rows {
		if seen[r.Agent] == nil {
			seen[r.Agent] = map[string]bool{}
		}
		seen[r.Agent][r.Home] = true
	}
	t := tags{agents: len(seen) > 1, homes: map[*agent]bool{}}
	for a, homes := range seen {
		t.homes[a] = len(homes) > 1
	}
	return t
}

func (t tags) of(r Row) []string {
	var out []string
	if t.agents {
		out = append(out, r.Agent.id)
	}
	if t.homes[r.Agent] {
		out = append(out, filepath.Base(r.Home))
	}
	return out
}

func pickerLabel(rows []Row, in []place) string {
	present := map[*agent]bool{}
	for _, r := range rows {
		present[r.Agent] = true
	}
	if len(rows) == 0 {
		for _, p := range in {
			present[p.agent] = true
		}
	}
	if len(present) == 1 {
		for a := range present {
			return a.label + " sessions"
		}
	}
	return "Sessions"
}
