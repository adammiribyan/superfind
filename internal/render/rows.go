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

const GoneMark = "transcript deleted"

const (
	LowContext   = 30
	NearlyFull   = 15
	GaugeCells   = 4
	gaugeColumns = GaugeCells + 2
)

type Gauge struct {
	Filled          int
	Low, NearlyFull bool
}

func GaugeOf(s session.Session) (Gauge, bool) {
	left, known := s.ContextLeft()
	if !known {
		return Gauge{}, false
	}
	used := 100 - left
	filled := min((used*GaugeCells+50)/100, GaugeCells)
	if used > 0 {
		filled = max(filled, 1)
	}
	return Gauge{Filled: filled, Low: left < LowContext, NearlyFull: left < NearlyFull}, true
}

func contextNote(s session.Session) string {
	left, known := s.ContextLeft()
	if !known {
		return ""
	}
	return fmt.Sprintf("%d%% context left", left)
}

func lowContextNote(s session.Session) string {
	if left, known := s.ContextLeft(); known && left < LowContext {
		return contextNote(s)
	}
	return ""
}

type RowText struct {
	Title, Waiting string
	Context        []string
}

func RowTextOf(r session.Row, shown Tags) RowText {
	t := RowText{Title: Flat(r.Session.Title)}
	if r.Status == session.WaitingLabel {
		t.Waiting = r.Status
	} else if r.Status != "" && r.Open == nil {
		t.Context = append(t.Context, r.Status)
	}
	t.Context = append(t.Context, when(r), Flat(r.Session.Project()))
	if r.FolderGone {
		t.Context = append(t.Context, "folder removed")
	}
	if agent.TranscriptGone(r.Path) {
		t.Context = append(t.Context, GoneMark)
	}
	if note := lowContextNote(r.Session); note != "" {
		t.Context = append(t.Context, note)
	}
	t.Context = append(t.Context, shown.of(r)...)
	return t
}

func metaParts(r session.Row, shown Tags) []string {
	s := r.Session
	parts := []string{Flat(s.Project())}
	if r.Status == session.WaitingLabel || r.Status != "" && r.Open == nil {
		parts = append(parts, r.Status)
	}
	parts = append(parts, when(r))
	if note := contextNote(s); note != "" {
		parts = append(parts, note)
	}
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

const (
	AgeWidth        = 8
	MinTitle        = 24
	minLabeledTitle = 16
	columnCap       = 14
)

type Cells struct {
	Title, Status, Project, Agent, Age string
	Waiting                            bool
}

func CellsOf(r session.Row, t Tags) Cells {
	c := Cells{Title: Flat(r.Session.Title), Project: Flat(r.Session.Project()), Agent: strings.Join(t.of(r), " "),
		Age: Age(r.Session.Mtime), Waiting: r.Status == session.WaitingLabel}
	switch {
	case r.Status == session.WaitingLabel:
		c.Status = r.Status
	case r.Open != nil:
		c.Status = "open"
	case agent.TranscriptGone(r.Path):
		c.Status = GoneMark
	case r.Status != "":
		c.Status = r.Status
	case r.Session.Active():
		c.Status = "active now"
	case r.FolderGone:
		c.Status = "folder removed"
	}
	if session.IsHere(r.Session) {
		c.Project = ""
	}
	return c
}

type Widths struct{ Project, Agent, Gauge int }

func Measure(rows []session.Row, t Tags) Widths {
	var w Widths
	for _, r := range rows {
		c := CellsOf(r, t)
		w.Project = max(w.Project, displayWidth(c.Project))
		w.Agent = max(w.Agent, displayWidth(c.Agent))
		if _, known := GaugeOf(r.Session); known {
			w.Gauge = gaugeColumns
		}
	}
	w.Project, w.Agent = min(w.Project, columnCap), min(w.Agent, columnCap)
	return w
}

type Columns struct{ Project, Agent, Gauge int }

func (c Columns) titleRoom(width int) int {
	room := width - 3 - AgeWidth - 1 - c.Gauge
	for _, w := range []int{c.Project, c.Agent} {
		if w > 0 {
			room -= w + 2
		}
	}
	return room
}

func (w Widths) Fit(width int) Columns {
	c := Columns(w)
	for _, drop := range []*int{&c.Agent, &c.Project} {
		if c.titleRoom(width) >= MinTitle {
			break
		}
		*drop = 0
	}
	return c
}

func (c Columns) For(cells Cells, width int) Columns {
	if cells.Project == "" {
		c.Project = 0
	}
	if cells.Status == "" {
		return c
	}
	word := 2 + displayWidth(cells.Status)
	for _, drop := range []*int{&c.Agent, &c.Project} {
		if c.titleRoom(width)-word >= minLabeledTitle {
			break
		}
		*drop = 0
	}
	return c
}
