package render

import (
	"database/sql"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/session"
)

func speaker(role string, a *agent.Agent) string {
	if role == "user" {
		return Accent + Bold + "you" + Reset
	}
	return Bold + a.ID + Reset
}

func excerpt(lines []string, keep int, fromEnd bool, note, secondary string) []string {
	if len(lines) <= keep {
		return lines
	}
	marker := "  " + secondary + "… " + note + Reset
	if fromEnd {
		return append([]string{marker}, lines[len(lines)-keep:]...)
	}
	return append(append([]string{}, lines[:keep]...), marker)
}

type PreviewRequest struct {
	Agent     *agent.Agent
	Path      string
	Width     int
	Height    int
	Recent    bool
	Query     string
	Explained string
	Secondary string
}

func PreviewHeader(req PreviewRequest, r session.Row, shown Tags, db *sql.DB) []string {
	width := req.Width - 2
	s := r.Session
	where := tilde(s.Cwd)
	if survivor := agent.SurvivingDir(s.Cwd); survivor != s.Cwd {
		where = "folder removed · resumes from " + tilde(survivor)
	}
	found := OneLine(plainPassage(req.Explained), width)
	if found == "" && db != nil {
		words, _ := session.Narrow(req.Query, nil)
		found = session.Snippet(db, req.Path, words, Bold+Accent, Reset)
	}
	header := []string{
		Bold + OneLine(s.Title, width) + Reset,
		req.Secondary + OneLine(strings.Join(metaParts(r, shown), " · "), width) + Reset,
		req.Secondary + OneLine(where, width) + Reset,
		"",
	}
	if found != "" {
		header[3] = req.Secondary + found + Reset
	}
	return header
}

const recentTurns = 8

func substantialAsk(t agent.Turn) bool { return t.Role == "user" && len(strings.Fields(t.Text)) > 3 }

func PreviewBody(req PreviewRequest) ([]string, error) {
	width := req.Width - 2
	enough := func(turns []agent.Turn) bool { return slices.ContainsFunc(turns, substantialAsk) }
	if req.Recent {
		enough = func(turns []agent.Turn) bool { return len(turns) >= recentTurns }
	}
	turns, err := agent.ReadEnding(req.Agent, req.Path, enough)
	if err != nil {
		return []string{req.Secondary + clipText("  Couldn't read this session: "+err.Error(), width) + Reset}, err
	}
	var body []string
	if req.Recent {
		for _, t := range turns[max(len(turns)-recentTurns, 0):] {
			body = append(body, speaker(t.Role, req.Agent))
			body = append(body, render(t.Text, width)...)
			body = append(body, "")
		}
		return body, nil
	}
	lastAny, lastSubstantial := -1, -1
	for i, t := range turns {
		if t.Role == "user" {
			lastAny = i
		}
		if substantialAsk(t) {
			lastSubstantial = i
		}
	}
	lastAsk := lastSubstantial
	if lastAsk < 0 {
		lastAsk = lastAny
	}
	reply := ""
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "assistant" {
			reply = turns[i].Text
			break
		}
	}
	var asked, replied []string
	if lastAsk >= 0 {
		asked = render(turns[lastAsk].Text, width)
	}
	answered := reply != "" && (lastAsk < 0 || turns[len(turns)-1].Role == "assistant")
	if answered {
		replied = render(reply, width)
	}
	build := func(askLines []string, keepReply int) []string {
		var body []string
		if lastAsk >= 0 {
			body = append(body, speaker("user", req.Agent))
			body = append(body, askLines...)
		}
		switch {
		case answered:
			body = append(body, "", speaker("assistant", req.Agent))
			body = append(body, excerpt(replied, keepReply, true, "earlier in this reply", req.Secondary)...)
		case lastAsk >= 0:
			body = append(body, "", req.Secondary+"  No reply yet. "+req.Agent.Label+" stopped before answering."+Reset)
		}
		return body
	}
	body = build(excerpt(asked, 10, false, "rest of your message", req.Secondary), 18)
	if req.Height <= 0 || len(body) <= req.Height {
		return body, nil
	}
	speakers := 3
	if !answered {
		speakers = 2
	}
	room := req.Height - speakers
	askLines := asked[:min(len(asked), 10, max(room/3, 1))]
	if len(askLines) < len(asked) {
		last := len(askLines) - 1
		askLines = append(append([]string{}, askLines[:last]...), ansi.Truncate(askLines[last], width-1, "")+req.Secondary+"…"+Reset)
	}
	return build(askLines, max(min(18, within(replied, room-len(askLines))), 0)), nil
}

func within(lines []string, rows int) int {
	if len(lines) <= rows {
		return rows
	}
	return rows - 1
}

var markdownMarks = regexp.MustCompile("[*`#]+")

func plainPassage(passage string) string {
	return strings.Join(strings.Fields(markdownMarks.ReplaceAllString(passage, "")), " ")
}
