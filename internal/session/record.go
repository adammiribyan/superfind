package session

import (
	"strings"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/open"
)

const (
	RecordSchema     = 1
	ShortestIDPrefix = 8
	AgentFilter      = "is:agent"
	OpenFilter       = "is:open"
)

type Record struct {
	Schema     int         `json:"schema"`
	Agent      string      `json:"agent"`
	ID         string      `json:"id"`
	Path       string      `json:"path"`
	Cwd        string      `json:"cwd"`
	Home       string      `json:"home"`
	Title      string      `json:"title"`
	Project    string      `json:"project"`
	Branch     string      `json:"branch"`
	Updated    string      `json:"updated"`
	Prompts    int         `json:"prompts"`
	Status     string      `json:"status"`
	Active     bool        `json:"active"`
	Open       *open.State `json:"open"`
	FolderGone bool        `json:"folder_gone"`
	Resume     string      `json:"resume"`
	Kind       string      `json:"kind"`
	Match      string      `json:"match"`
}

func StatusName(label string) string {
	if label == "" {
		return ""
	}
	for _, k := range statusKinds {
		if k.label == label {
			return k.name
		}
	}
	return ""
}

func RecordOf(r Row, clean func(string) string) Record {
	s := r.Session
	resume, _ := agent.ResumeCommand(r.Agent, r.Home, r.Path, s.Cwd)
	return Record{
		Schema:     RecordSchema,
		Agent:      r.Agent.ID,
		ID:         s.ID,
		Path:       r.Path,
		Cwd:        s.Cwd,
		Home:       r.Home,
		Title:      clean(s.Title),
		Project:    agent.Printable(s.Project()),
		Branch:     agent.Printable(s.Branch),
		Updated:    time.Unix(int64(s.Mtime), 0).UTC().Format(time.RFC3339),
		Prompts:    s.Prompts,
		Status:     StatusName(r.Status),
		Active:     s.Active(),
		Open:       r.Open,
		FolderGone: r.FolderGone,
		Resume:     resume,
		Kind:       KindOf(s),
	}
}

func AsksForAgents(query string) bool { return Parse(query).Agents }

func SameKind(rows []Row, byAgent bool) []Row {
	kind := ""
	if byAgent {
		kind = AgentFilter
	}
	_, same := Narrow(kind, rows)
	return same
}

func KindOf(s Session) string {
	if s.ByAgent {
		return "agent"
	}
	return "interactive"
}

func ByID(rows []Row, id string) (matches []Row, exact bool) {
	for _, r := range rows {
		if strings.EqualFold(r.Session.ID, id) {
			matches = append(matches, r)
		}
	}
	if len(matches) > 0 || len(id) < ShortestIDPrefix {
		return matches, len(matches) > 0
	}
	for _, r := range rows {
		if strings.HasPrefix(strings.ToLower(r.Session.ID), strings.ToLower(id)) {
			matches = append(matches, r)
		}
	}
	return matches, false
}
