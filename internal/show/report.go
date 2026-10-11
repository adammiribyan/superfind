package show

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
)

const requestWidth = 200

var endings = map[agent.Event]struct{ name, words string }{
	agent.Replied:     {"replied", "with a reply"},
	agent.Interrupted: {"interrupted", "on an interruption"},
	agent.CalledTool:  {"mid_command", "mid-command"},
	agent.Prompted:    {"unanswered", "on an unanswered request"},
}

var labelSources = map[string]string{
	"jev":    "judged by Jev",
	"ending": "from how it ended",
}

type inferred struct {
	Label *string `json:"label"`
	By    *string `json:"by"`
}

type reportRecord struct {
	session.Record
	Ending      *string  `json:"ending"`
	ToolCalls   int      `json:"tool_calls"`
	LastRequest string   `json:"last_request"`
	Inferred    inferred `json:"inferred"`
}

var markdownSpecial = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "<", `\<`, ">", `\>`, "#", `\#`, "|", `\|`, "`", "\\`", "&", `\&`, "~", `\~`)

func lastRequest(s session.Session) string {
	request := s.LastPrompt
	if s.RequestClipped() {
		request = "…"
		if i := strings.LastIndexByte(s.LastPrompt, ' '); i > 0 {
			request = s.LastPrompt[:i] + " …"
		}
	}
	return Clean(request)
}

func orNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func WriteReport(w io.Writer, rows []session.Row) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		err := enc.Encode(reportRecord{
			Record:      session.RecordOf(r, Clean),
			Ending:      orNull(endings[r.Session.Ending].name),
			ToolCalls:   r.Session.ToolCalls,
			LastRequest: lastRequest(r.Session),
			Inferred:    inferred{orNull(session.StatusName(r.Status)), orNull(r.StatusBy)},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func reportTime(t time.Time) string { return t.Local().Format("Monday, 2 January 2006, 15:04") }

func groupName(rows []session.Row) func(session.Session) string {
	rootOf, shared := map[string]string{}, map[string]bool{}
	for _, r := range rows {
		name, root := r.Session.Project(), cleanRoot(r.Session)
		if seen, ok := rootOf[name]; ok && seen != root {
			shared[name] = true
		}
		rootOf[name] = root
	}
	return func(s session.Session) string {
		if shared[s.Project()] {
			return oneLine(cleanRoot(s))
		}
		return oneLine(s.Project())
	}
}

func cleanRoot(s session.Session) string {
	if root := s.Root(); root != "" {
		return filepath.Clean(root)
	}
	return ""
}

func WriteReportText(w io.Writer, rows []session.Row, ids map[string]string, after, before time.Time) error {
	span := "since " + reportTime(after)
	switch {
	case after.IsZero():
		span = "before " + reportTime(before)
	case !before.IsZero():
		span = "between " + reportTime(after) + " and " + reportTime(before)
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintf(w, "No sessions changed %s.\n", span)
		return err
	}
	nameOf := groupName(rows)
	var groups []string
	byGroup := map[string][]session.Row{}
	for _, r := range rows {
		g := nameOf(r.Session)
		if _, seen := byGroup[g]; !seen {
			groups = append(groups, g)
		}
		byGroup[g] = append(byGroup[g], r)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Sessions changed %s\n", span)
	for _, g := range groups {
		fmt.Fprintf(&b, "\n## %s\n\n", markdownSpecial.Replace(g))
		for _, r := range byGroup[g] {
			s := r.Session
			parts := []string{r.Agent.Label}
			if s.Branch != "" {
				parts = append(parts, markdownSpecial.Replace(oneLine(s.Branch)))
			}
			if e, ok := endings[s.Ending]; ok {
				parts = append(parts, "ended "+e.words)
			}
			parts = append(parts, render.Age(s.Mtime), "id "+markdownSpecial.Replace(oneLine(ids[r.Path])))
			fmt.Fprintf(&b, "- **%s**: %s\n", markdownSpecial.Replace(render.Flat(Clean(s.Title))), strings.Join(parts, " · "))
			if request := render.OneLine(lastRequest(s), requestWidth); request != "" {
				fmt.Fprintf(&b, "  - Last request: %s\n", markdownSpecial.Replace(request))
			}
			if by, ok := labelSources[r.StatusBy]; ok {
				fmt.Fprintf(&b, "  - Inferred: %s, %s\n", r.Status, by)
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
