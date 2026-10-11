package show

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/session"
)

func reportedRequest(t *testing.T, lastPrompt string) string {
	t.Helper()
	row := session.Row{Agent: agent.Named("claude"), Session: session.Session{ID: "a1", Title: "t", LastPrompt: lastPrompt}}
	var out bytes.Buffer
	if err := WriteReport(&out, []session.Row{row}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		LastRequest string `json:"last_request"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.LastRequest
}

func TestAClippedLastRequestNeverShowsPartOfASecret(t *testing.T) {
	cases := map[string]string{
		strings.Repeat("x", 380) + " AKIA12345678901234…": strings.Repeat("x", 380) + " …",
		strings.Repeat("y", 399) + "…":                    "…",
		"should I keep going…":                            "should I keep going…",
	}
	for prompt, want := range cases {
		if got := reportedRequest(t, prompt); got != want {
			t.Errorf("last_request for %q = %q, want %q", prompt, got, want)
		}
	}
}

func TestTheMarkdownReportShowsTitlesAndProjectsAsText(t *testing.T) {
	row := session.Row{Agent: agent.Named("claude"), Session: session.Session{
		ID: "0199a1b2-c3d4-7000-8000-000000000000", Title: "fix [docs](x) *now* &copy; ~~prod~~", Cwd: "/work/ops #2", Branch: "fix_it",
		Mtime: session.Now() - 3600, Prompts: 5, Words: 900, ToolCalls: 2, LastPrompt: "use <b> tags",
	}}
	var out bytes.Buffer
	if err := WriteReportText(&out, []session.Row{row}, session.ShortIDs([]session.Row{row}), time.Now().Add(-24*time.Hour), time.Time{}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	want := []string{"## ops \\#2", "- **fix \\[docs\\](x) \\*now\\* \\&copy; \\~\\~prod\\~\\~**: Claude · fix\\_it · 1h · id 0199a1b2", "  - Last request: use \\<b\\> tags"}
	if len(lines) < 7 || lines[2] != want[0] || lines[4] != want[1] || lines[5] != want[2] {
		t.Fatalf("got\n%s", out.String())
	}
}

func TestProjectsThatShareANameAreHeadedByTheirFolders(t *testing.T) {
	at := func(id, cwd string) session.Row {
		return session.Row{Agent: agent.Named("claude"), Session: session.Session{ID: id, Title: id, Cwd: cwd, Mtime: session.Now() - 7200}}
	}
	rows := []session.Row{at("s1", "/work/a/web"), at("s2", "/work/api"), at("s3", "/work/b/web/.claude/worktrees/fix"), at("s4", "/work/a/web/")}
	var out bytes.Buffer
	if err := WriteReportText(&out, rows, session.ShortIDs(rows), time.Now().Add(-24*time.Hour), time.Time{}); err != nil {
		t.Fatal(err)
	}
	var headings []string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "## ") {
			headings = append(headings, l)
		}
	}
	if want := []string{"## /work/a/web", "## api", "## /work/b/web"}; strings.Join(headings, "|") != strings.Join(want, "|") {
		t.Fatalf("headings %q, want %q\n%s", headings, want, out.String())
	}
}

func TestTheReportHeaderNamesTheDayDateYearAndTime(t *testing.T) {
	from := time.Date(2026, 10, 10, 18, 0, 0, 0, time.Local)
	until := time.Date(2026, 10, 11, 0, 0, 0, 0, time.Local)
	cases := []struct {
		after, before time.Time
		want          string
	}{
		{from, time.Time{}, "No sessions changed since Saturday, 10 October 2026, 18:00.\n"},
		{from, until, "No sessions changed between Saturday, 10 October 2026, 18:00 and Sunday, 11 October 2026, 00:00.\n"},
		{time.Time{}, until, "No sessions changed before Sunday, 11 October 2026, 00:00.\n"},
	}
	for _, c := range cases {
		var out bytes.Buffer
		if err := WriteReportText(&out, nil, nil, c.after, c.before); err != nil {
			t.Fatal(err)
		}
		if out.String() != c.want {
			t.Errorf("got %q, want %q", out.String(), c.want)
		}
	}
}
