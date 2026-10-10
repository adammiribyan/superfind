package render

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/session"
)

const (
	Dim   = "\033[2m"
	Bold  = "\033[1m"
	Reset = "\033[0m"
)

var NoColor = os.Getenv("NO_COLOR") != ""

var Accent = "\033[34m"

func init() {
	if NoColor {
		Accent = Bold
	}
}

type Tags struct {
	agents bool
	homes  map[*agent.Agent]bool
}

func TagsFor(rows []session.Row) Tags {
	seen := map[*agent.Agent]map[string]bool{}
	for _, r := range rows {
		if seen[r.Agent] == nil {
			seen[r.Agent] = map[string]bool{}
		}
		seen[r.Agent][r.Home] = true
	}
	t := Tags{agents: len(seen) > 1, homes: map[*agent.Agent]bool{}}
	for a, homes := range seen {
		t.homes[a] = len(homes) > 1
	}
	return t
}

func (t Tags) of(r session.Row) []string {
	var out []string
	if t.agents {
		out = append(out, r.Agent.ID)
	}
	if t.homes[r.Agent] {
		out = append(out, filepath.Base(r.Home))
	}
	return out
}

func PickerLabel(rows []session.Row, in []agent.Place) string {
	present := map[*agent.Agent]bool{}
	for _, r := range rows {
		present[r.Agent] = true
	}
	if len(rows) == 0 {
		for _, p := range in {
			if paths, _ := p.Agent.Sessions(p.Home); len(paths) > 0 {
				present[p.Agent] = true
			}
		}
	}
	if len(present) == 1 {
		for a := range present {
			return a.Label + " sessions"
		}
	}
	return "Sessions"
}

func NoSessions() string {
	homes := make([]string, len(agent.Agents))
	for i, a := range agent.Agents {
		homes[i] = tilde(a.DefaultHome())
	}
	return "No sessions yet in " + strings.Join(homes[:len(homes)-1], ", ") + ", or " + homes[len(homes)-1] + "."
}

type RowText struct {
	Title, Waiting string
	Context        []string
}

func RowTextOf(r session.Row, shown Tags) RowText {
	t := RowText{Title: Flat(r.Session.Title)}
	if r.Status == session.WaitingLabel {
		t.Waiting = r.Status
	} else if r.Status != "" {
		t.Context = append(t.Context, r.Status)
	}
	t.Context = append(t.Context, when(r.Session), Flat(r.Session.Project()))
	if r.FolderGone {
		t.Context = append(t.Context, "folder removed")
	}
	t.Context = append(t.Context, shown.of(r)...)
	return t
}

func metaParts(r session.Row, shown Tags) []string {
	s := r.Session
	parts := []string{Flat(s.Project())}
	if r.Status != "" {
		parts = append(parts, r.Status)
	}
	parts = append(parts, when(s))
	if s.Branch != "" && s.Branch != "HEAD" {
		parts = append(parts, Flat(s.Branch))
	}
	noun := "prompts"
	if s.Prompts == 1 {
		noun = "prompt"
	}
	parts = append(parts, fmt.Sprintf("%d %s", s.Prompts, noun))
	return append(parts, shown.of(r)...)
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func SecondaryFromEnv() string {
	hx := os.Getenv("SUPERFIND_SECONDARY")
	if NoColor || !hexColor.MatchString(hx) {
		return Dim
	}
	r, _ := strconv.ParseUint(hx[1:3], 16, 8)
	g, _ := strconv.ParseUint(hx[3:5], 16, 8)
	b, _ := strconv.ParseUint(hx[5:7], 16, 8)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

func RowContext(t RowText, secondary string) string {
	flag := ""
	if t.Waiting != "" {
		flag = Accent + t.Waiting + Reset + secondary + " · "
	}
	return flag + strings.Join(t.Context, " · ")
}
