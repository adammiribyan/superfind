package session

import (
	"testing"

	"github.com/adammiribyan/superfind/internal/agent"
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
