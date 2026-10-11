package session

import (
	"maps"
	"strings"
	"testing"
	"time"
)

func TestTheReportWindowReadsDatesDaysAndSpansBackFromNow(t *testing.T) {
	zone := time.FixedZone("UTC+2", 2*3600)
	now := time.Date(2026, 10, 11, 9, 30, 0, 0, zone)
	cases := []struct {
		query, rest   string
		after, before time.Time
	}{
		{"", "", time.Date(2026, 10, 10, 9, 30, 0, 0, zone), time.Time{}},
		{"after:2026-10-09", "", time.Date(2026, 10, 9, 0, 0, 0, 0, zone), time.Time{}},
		{"after:today is:waiting", "is:waiting", time.Date(2026, 10, 11, 0, 0, 0, 0, zone), time.Time{}},
		{"after:Yesterday before:today", "", time.Date(2026, 10, 10, 0, 0, 0, 0, zone), time.Date(2026, 10, 11, 0, 0, 0, 0, zone)},
		{"agent:codex after:12h", "agent:codex", time.Date(2026, 10, 10, 21, 30, 0, 0, zone), time.Time{}},
		{"after:3d", "", time.Date(2026, 10, 8, 9, 30, 0, 0, zone), time.Time{}},
		{"after:2w before:1d", "", time.Date(2026, 9, 27, 9, 30, 0, 0, zone), time.Date(2026, 10, 10, 9, 30, 0, 0, zone)},
		{"before:yesterday", "", time.Time{}, time.Date(2026, 10, 10, 0, 0, 0, 0, zone)},
		{"IS:Waiting AFTER:3d Agent:Codex is:AGENT", "is:waiting agent:codex is:agent", time.Date(2026, 10, 8, 9, 30, 0, 0, zone), time.Time{}},
		{"after before", "after before", time.Date(2026, 10, 10, 9, 30, 0, 0, zone), time.Time{}},
	}
	for _, c := range cases {
		after, before, rest, err := ParseReport(c.query, now)
		if err != nil || !after.Equal(c.after) || !before.Equal(c.before) || rest != c.rest {
			t.Errorf("%q gave after %v, before %v, rest %q, error %v; want %v, %v, %q", c.query, after, before, rest, err, c.after, c.before, c.rest)
		}
	}
	for _, bad := range []string{"after:", "after:soon", "before:2026-13-01", "after:-3d", "after:3m", "after:1234567h", "after:12345h", "before:0001-01-01", "after:1969-12-31", "after:2026-10-09T18:00", "after:today before:today"} {
		if _, _, _, err := ParseReport(bad, now); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	for query, said := range map[string]string{
		"is:bogus":                     "is:bogus isn't a filter; is: takes waiting, unfinished, or agent",
		"agent:Bogus":                  "agent:Bogus isn't a filter; agent: takes claude, codex, or grok",
		"after:today before:yesterday": "after:today is not earlier than before:yesterday, so no session could match",
	} {
		if _, _, _, err := ParseReport(query, now); err == nil || err.Error() != said {
			t.Errorf("%q gave error %v; want %q", query, err, said)
		}
	}
}

func TestAReportIDIsTheShortestPrefixNoOtherSessionShares(t *testing.T) {
	rows := []Row{
		{Path: "a", Session: Session{ID: "01990007-aaaa-7000-8000-000000000001"}},
		{Path: "b", Session: Session{ID: "01990007-abbb-7000-8000-000000000002"}},
		{Path: "c", Session: Session{ID: "0199ffff-cccc-7000-8000-000000000003"}},
		{Path: "d", Session: Session{ID: "c3"}},
		{Path: "e", Session: Session{ID: "01990007-abbb-7000-8000-000000000002"}},
		{Path: "f", Session: Session{ID: "1234567é-rest"}},
	}
	want := map[string]string{"a": "01990007-aa", "b": "01990007-ab", "c": "0199ffff", "d": "c3", "e": "01990007-ab", "f": "1234567é"}
	if got := ShortIDs(rows); !maps.Equal(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestARequestIsClippedOnlyAtTheIndexLimitInCharactersAsShown(t *testing.T) {
	decomposed := strings.Repeat("e\u0301", 300) + " ok…"
	clipped := clip(strings.Repeat("word ", 100), promptRunes)
	for prompt, want := range map[string]bool{decomposed: false, clipped: true, "finish the thing…": false} {
		if got := (Session{LastPrompt: prompt}).RequestClipped(); got != want {
			t.Errorf("RequestClipped(%d chars) = %v, want %v", len(prompt), got, want)
		}
	}
}

func TestAWindowEndingFarInTheFutureKeepsTodaysSessions(t *testing.T) {
	now := time.Now()
	rows := []Row{{Path: "today", Session: Session{Mtime: float64(now.Unix()), Prompts: 5, Words: 900, ToolCalls: 2}}}
	if got := Changed(rows, now.Add(-time.Hour), time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC)); len(got) != 1 {
		t.Fatalf("a window ending in 2500 kept %d sessions, want 1", len(got))
	}
}
