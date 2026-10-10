package session

import (
	"database/sql"
	"slices"
	"strings"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
)

const (
	RecordSchema     = 1
	ShortestIDPrefix = 8
	AgentFilter      = "is:agent"
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
	Kind       string `json:"kind"`
	Match      string `json:"match"`
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
		Project:    agent.Printable(s.Project()),
		Branch:     agent.Printable(s.Branch),
		Updated:    time.Unix(int64(s.Mtime), 0).UTC().Format(time.RFC3339),
		Prompts:    s.Prompts,
		Status:     StatusName(r.Status),
		Active:     s.Active(),
		FolderGone: r.FolderGone,
		Resume:     agent.ResumeCommand(r.Agent, r.Home, s.Cwd, s.ID),
		Kind:       KindOf(s),
	}
}

func AsksForAgents(query string) bool {
	return slices.ContainsFunc(strings.Fields(query), func(w string) bool { return strings.EqualFold(w, AgentFilter) })
}

func Passages(db *sql.DB, query string) map[string]string {
	match := ftsQuery(query)
	if match == "" || db == nil {
		return nil
	}
	rows, err := db.Query("SELECT path, snippet(docs, 2, char(1), char(2), '…', 10) FROM docs WHERE docs MATCH ?", match)
	if err != nil {
		return nil
	}
	defer rows.Close()
	found := map[string]string{}
	for rows.Next() {
		var path, text string
		if rows.Scan(&path, &text) == nil && strings.Contains(text, "\x01") {
			found[path] = strings.Join(strings.Fields(strings.NewReplacer("\x01", "", "\x02", "").Replace(text)), " ")
		}
	}
	return found
}

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
