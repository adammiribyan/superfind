package show

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/blame"
	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
)

const DefaultMaxBytes = 60000

type Turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
	Cut  bool   `json:"cut,omitempty"`
}

type Options struct {
	Tail     int
	MaxBytes int
}

type Conversation struct {
	Turns     []Turn
	Total     int
	Omitted   int
	Truncated bool
}

func Clean(s string) string { return Mask(agent.Printable(s)) }

func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) || agent.Bidi(r) {
			return '?'
		}
		return r
	}, agent.Printable(s))
}

const (
	bodyBytes   = 2000
	bodiesBytes = 12000
)

func Bodies(bodies []string, limited bool) []string {
	out, left := make([]string, len(bodies)), bodiesBytes
	for i := len(bodies) - 1; i >= 0; i-- {
		most := bodyBytes
		if limited {
			most = max(min(most, left), 0)
		}
		out[i] = body(bodies[i], most)
		left -= len(out[i])
	}
	return out
}

func body(s string, most int) string {
	s = strings.TrimSpace(Clean(strings.ToValidUTF8(s, "\uFFFD")))
	if len(s) <= most {
		return s
	}
	cut := most
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if open := strings.LastIndex(s[:cut], "[masked "); open >= 0 && !strings.Contains(s[open:cut], "]") {
		cut = open
	}
	return strings.TrimSpace(s[:cut]) + "…"
}

func lastBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return s[cut:]
}

func speaker(role string, r session.Record) string {
	if role == "user" {
		return "user"
	}
	return r.Agent
}

func short(sha string) string { return sha[:min(len(sha), 7)] }

func outcomeLine(o blame.Outcome) string {
	head := "made " + short(o.Commit) + " " + oneLine(o.Subject)
	switch {
	case o.Kind == blame.PullRequest:
		head = fmt.Sprintf("opened #%d", o.PR)
	case o.Commit == "":
		head = "made " + oneLine(o.Subject)
	}
	if fate := fateOf(o); fate != "" {
		return head + " · " + fate
	}
	return head
}

func fateOf(o blame.Outcome) string {
	known := o.Branch != ""
	switch {
	case o.Stage == blame.Committed:
		return "committed, not pushed"
	case o.Stage == blame.Pushed && known:
		return "pushed, not on " + o.Branch
	case o.Stage == blame.Pushed:
		return "pushed"
	case o.Stage == blame.Opened && known:
		return "not merged into " + o.Branch
	case o.Stage == blame.Opened:
		return ""
	}
	fate := "merged into " + o.Branch
	switch {
	case o.Kind == blame.PullRequest || (o.PR == 0 && o.Landed != o.Commit):
		fate += " as " + short(o.Landed)
	case o.Landed != o.Commit:
		fate = fmt.Sprintf("squashed into %s as %s (#%d)", o.Branch, short(o.Landed), o.PR)
	case o.PR > 0:
		fate += fmt.Sprintf(" via #%d", o.PR)
	}
	if o.Stage == blame.Reverted {
		return fate + ", then reverted by " + short(o.RevertedBy)
	}
	if at, err := time.Parse(time.RFC3339, o.LandedAt); err == nil {
		fate += " " + render.Ago(float64(at.Unix()))
	}
	return fate
}

func WriteText(w io.Writer, r session.Record, c Conversation, outcomes []blame.Outcome) error {
	fmt.Fprintf(w, "# %s\n%s · %s · %s\n", oneLine(r.Title), oneLine(r.Agent), oneLine(r.Cwd), oneLine(r.ID))
	for _, o := range outcomes {
		fmt.Fprintln(w, outcomeLine(o))
	}
	if c.Omitted > 0 {
		noun := "turns"
		if c.Omitted == 1 {
			noun = "turn"
		}
		fmt.Fprintf(w, "\n… %d earlier %s not shown\n", c.Omitted, noun)
	}
	for _, t := range c.Turns {
		if _, err := fmt.Fprintf(w, "\n## %s\n\n%s\n", speaker(t.Role, r), t.Text); err != nil {
			return err
		}
	}
	return nil
}

func WriteRecords(w io.Writer, rows []session.Row, db *sql.DB, query string, anyWord bool) error {
	words, _ := session.Narrow(query, nil)
	passages := session.Passages(db, words)
	if passages == nil {
		passages = map[string]string{}
	}
	if anyWord && slices.ContainsFunc(rows, func(r session.Row) bool { return passages[r.Path] == "" }) {
		some, _ := session.PassagesOfAny(db, session.MeaningfulWords(words), session.PassageTokens)
		for path, text := range some {
			if passages[path] == "" {
				passages[path] = text
			}
		}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		record := session.RecordOf(r, Clean)
		record.Match = Clean(passages[r.Path])
		if err := enc.Encode(record); err != nil {
			return err
		}
	}
	return nil
}

func WriteJSON(w io.Writer, r session.Record, c Conversation, outcomes []blame.Outcome) error {
	turns := c.Turns
	if turns == nil {
		turns = []Turn{}
	}
	if outcomes == nil {
		outcomes = []blame.Outcome{}
	}
	out := struct {
		session.Record
		Outcomes   []blame.Outcome `json:"outcomes"`
		TurnsTotal int             `json:"total_turns"`
		Omitted    int             `json:"omitted_turns"`
		Truncated  bool            `json:"truncated"`
		Turns      []Turn          `json:"turns"`
	}{r, outcomes, c.Total, c.Omitted, c.Truncated, turns}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}
