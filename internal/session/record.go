package session

import (
	"strings"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
)

const (
	RecordSchema     = 1
	ShortestIDPrefix = 8
)

type Record struct {
	Schema     int    `json:"schema"`
	Agent      string `json:"agent"`
	ID         string `json:"id"`
	Path       string `json:"path"`
	Cwd        string `json:"cwd"`
	Home       string `json:"home"`
	Title      string `json:"title"`
	Project    string `json:"project"`
	Branch     string `json:"branch"`
	Updated    string `json:"updated"`
	Prompts    int    `json:"prompts"`
	Status     string `json:"status"`
	Active     bool   `json:"active"`
	FolderGone bool   `json:"folder_gone"`
	Resume     string `json:"resume"`
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
	return Record{
		Schema:     RecordSchema,
		Agent:      r.Agent.ID,
		ID:         s.ID,
		Path:       r.Path,
		Cwd:        s.Cwd,
		Home:       r.Home,
		Title:      clean(s.Title),
		Project:    clean(s.Project()),
		Branch:     clean(s.Branch),
		Updated:    time.Unix(int64(s.Mtime), 0).UTC().Format(time.RFC3339),
		Prompts:    s.Prompts,
		Status:     StatusName(r.Status),
		Active:     s.Active(),
		FolderGone: r.FolderGone,
		Resume:     agent.ResumeCommand(r.Agent, r.Home, s.Cwd, s.ID),
	}
}

func ByID(rows []Row, id string) (matches []Row, exact bool) {
	for _, r := range rows {
		if r.Session.ID == id {
			matches = append(matches, r)
		}
	}
	if len(matches) > 0 || len(id) < ShortestIDPrefix {
		return matches, len(matches) > 0
	}
	for _, r := range rows {
		if strings.HasPrefix(r.Session.ID, id) {
			matches = append(matches, r)
		}
	}
	return matches, false
}
