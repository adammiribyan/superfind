package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/open"
)

func idRows(ids ...string) []Row {
	out := make([]Row, len(ids))
	for i, id := range ids {
		out[i] = Row{Session: Session{ID: id}, Path: "/p/" + id + "/" + string(rune('a'+i))}
	}
	return out
}

func TestByIDPrefersAnExactMatchOverLongerIDsThatStartWithIt(t *testing.T) {
	m, exact := ByID(idRows("01990001-aaaa", "01990001"), "01990001")
	if len(m) != 1 || !exact || m[0].Session.ID != "01990001" {
		t.Fatalf("got %v exact=%v", m, exact)
	}
}

func TestByIDMatchesAUniquePrefix(t *testing.T) {
	m, exact := ByID(idRows("01990001-aaaa", "01990002-bbbb"), "01990002")
	if len(m) != 1 || exact || m[0].Session.ID != "01990002-bbbb" {
		t.Fatalf("got %v exact=%v", m, exact)
	}
}

func TestByIDReturnsEveryMatchForAnAmbiguousPrefix(t *testing.T) {
	if m, _ := ByID(idRows("019a3100-aaaa", "019a3100-bbbb", "019a3200-cccc"), "019a3100"); len(m) != 2 {
		t.Fatalf("got %v", m)
	}
}

func TestByIDReturnsEveryCopyOfAnExactID(t *testing.T) {
	m, exact := ByID(idRows("01990001-aaaa", "01990001-aaaa"), "01990001-aaaa")
	if len(m) != 2 || !exact {
		t.Fatalf("got %v exact=%v", m, exact)
	}
}

func TestByIDIgnoresLetterCase(t *testing.T) {
	if m, exact := ByID(idRows("0f6cab12-aaaa"), "0F6CAB12-AAAA"); len(m) != 1 || !exact {
		t.Fatalf("exact: got %v", m)
	}
	if m, _ := ByID(idRows("0f6cab12-aaaa"), "0F6CAB12"); len(m) != 1 {
		t.Fatalf("prefix: got %v", m)
	}
}

func TestByIDIgnoresPrefixesShorterThanTheMinimum(t *testing.T) {
	if m, _ := ByID(idRows("01990001-aaaa"), "0199000"); len(m) != 0 {
		t.Fatalf("a %d-character prefix matched %v", len("0199000"), m)
	}
	if m, _ := ByID(idRows("01990001-aaaa"), "01990001"); len(m) != 1 {
		t.Fatal("an 8-character prefix did not match")
	}
}

func TestStatusNameMapsEachShownLabel(t *testing.T) {
	cases := map[string]string{"": "", WaitingLabel: "waiting", "unfinished": "unfinished", "something new": ""}
	for label, want := range cases {
		if got := StatusName(label); got != want {
			t.Errorf("StatusName(%q) = %q, want %q", label, got, want)
		}
	}
}

func TestRecordOfCleansTextFieldsButKeepsPathsExact(t *testing.T) {
	r := Row{
		Session: Session{ID: "id1", Cwd: "/work/a b", Title: "Title\x1b[31m", Branch: "main", Mtime: 0, Prompts: 3},
		Agent:   agent.Named("claude"), Home: "/h/.claude", Path: "/h/.claude/projects/x/id1.jsonl", Status: WaitingLabel,
	}
	rec := RecordOf(r, func(s string) string { return agent.Printable(s) })
	if rec.Project != "a b" || rec.Branch != "main" {
		t.Fatalf("got %+v", rec)
	}
	if rec.Title != "Title" || rec.Cwd != "/work/a b" || rec.Path != r.Path || rec.Status != "waiting" {
		t.Fatalf("got %+v", rec)
	}
	if rec.Schema != RecordSchema || rec.Agent != "claude" || rec.Updated != "1970-01-01T00:00:00Z" || rec.Prompts != 3 {
		t.Fatalf("got %+v", rec)
	}
	if want := agent.ResumeCommand(r.Agent, r.Home, r.Session.Cwd, "id1"); rec.Resume != want {
		t.Fatalf("resume = %q, want %q", rec.Resume, want)
	}
}

func kindRows() []Row {
	return []Row{
		{Session: Session{ID: "typed", Title: "typed"}, Agent: agent.Named("claude"), Path: "/a"},
		{Session: Session{ID: "exec", Title: "exec", ByAgent: true}, Agent: agent.Named("codex"), Path: "/b", Status: WaitingLabel},
	}
}

func ids(rows []Row) string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Session.ID)
	}
	return strings.Join(out, ",")
}

func TestNarrowHidesSessionsAnAgentStartedUnlessAskedFor(t *testing.T) {
	cases := []struct {
		query string
		every bool
		want  string
		words string
	}{
		{"", false, "typed", ""},
		{"login bug", false, "typed", "login bug"},
		{"is:agent", false, "exec", ""},
		{"IS:AGENT review", false, "exec", "review"},
		{"is:agent agent:claude", false, "", ""},
		{"", true, "typed,exec", ""},
		{"is:agent", true, "exec", ""},
		{"agent:codex", true, "exec", ""},
		{"is:waiting is:agent", false, "exec", ""},
		{"is:waiting", false, "", ""},
		{"is:waiting", true, "exec", ""},
	}
	for _, c := range cases {
		narrow := Narrow
		if c.every {
			narrow = NarrowEvery
		}
		words, rows := narrow(c.query, kindRows())
		if ids(rows) != c.want || words != c.words {
			t.Errorf("query %q every=%v: rows %q words %q, want rows %q words %q", c.query, c.every, ids(rows), words, c.want, c.words)
		}
	}
}

func TestRecordKindNamesWhoStartedTheSession(t *testing.T) {
	rows := kindRows()
	if a, b := RecordOf(rows[0], agent.Printable).Kind, RecordOf(rows[1], agent.Printable).Kind; a != "interactive" || b != "agent" {
		t.Fatalf("kinds %q %q", a, b)
	}
}

func TestARecordSaysWhichProcessHoldsTheSession(t *testing.T) {
	r := Row{Session: Session{ID: "id1", Cwd: "/w"}, Agent: agent.Named("claude"), Path: "/p"}
	field := func(r Row) string {
		raw, _ := json.Marshal(RecordOf(r, agent.Printable))
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		return string(fields["open"])
	}
	if got := field(r); got != "null" {
		t.Errorf("closed: open = %s", got)
	}
	r.Open = &open.State{PID: 4242, Status: "busy", Tmux: &open.Pane{ID: "%3", Session: "work", Window: 1}}
	if got, want := field(r), `{"pid":4242,"status":"busy","tmux":{"pane":"%3","session":"work","window":1}}`; got != want {
		t.Errorf("open: open = %s, want %s", got, want)
	}
	r.Open = &open.State{PID: 7}
	if got, want := field(r), `{"pid":7,"status":"","tmux":null}`; got != want {
		t.Errorf("open outside tmux: open = %s, want %s", got, want)
	}
}

func TestIsOpenKeepsOnlySessionsAnAgentHolds(t *testing.T) {
	rows := []Row{
		{Session: Session{ID: "held", Title: "held"}, Agent: agent.Named("claude"), Path: "/a", Open: &open.State{PID: 1}},
		{Session: Session{ID: "closed", Title: "closed"}, Agent: agent.Named("codex"), Path: "/b"},
		{Session: Session{ID: "script", Title: "script", ByAgent: true}, Agent: agent.Named("codex"), Path: "/c", Open: &open.State{PID: 2}},
	}
	cases := []struct {
		query string
		every bool
		want  string
		words string
	}{
		{"is:open", false, "held", ""},
		{"IS:OPEN ledger", false, "held", "ledger"},
		{"is:open", true, "held,script", ""},
		{"is:open is:agent", false, "script", ""},
		{"is:open agent:codex", false, "", ""},
		{"", true, "held,closed,script", ""},
	}
	for _, c := range cases {
		narrow := Narrow
		if c.every {
			narrow = NarrowEvery
		}
		words, kept := narrow(c.query, rows)
		if ids(kept) != c.want || words != c.words {
			t.Errorf("query %q every=%v: rows %q words %q, want rows %q words %q", c.query, c.every, ids(kept), words, c.want, c.words)
		}
	}
}

func TestAnnotatingAgainForgetsAProcessThatExited(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	rows := []Row{{Session: Session{ID: "id1", Cwd: "/w"}, Agent: agent.Named("claude"), Path: "/p"}}
	held := map[open.Key]open.State{{Agent: "claude", ID: "id1"}: {PID: 4242, Status: "idle"}}
	rows = Annotate(rows, held)
	if rows[0].Open == nil || *rows[0].Open != (open.State{PID: 4242, Status: "idle"}) {
		t.Fatalf("first annotate: open = %+v", rows[0].Open)
	}
	if rows = Annotate(rows, map[open.Key]open.State{}); rows[0].Open != nil {
		t.Fatalf("after the process exited: open = %+v", rows[0].Open)
	}
}
