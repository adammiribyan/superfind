package session

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func inZone(t *testing.T, name string) *time.Location {
	zone, err := time.LoadLocation(name)
	if err != nil {
		t.Skip("no time zone database: " + err.Error())
	}
	local := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = local })
	return zone
}

func TestParseTakesFiltersOutOfTheWordsAndLeavesOtherColonsAlone(t *testing.T) {
	berlin := inZone(t, "Europe/Berlin")
	now := time.Date(2026, 10, 10, 15, 0, 0, 0, berlin)
	q := parseAt("in:here BRANCH:Main fix After:3d the foo:bar http://a/b build IN:Here", now)
	if q.Words != "fix the foo:bar http://a/b build" || !q.Here || !slices.Equal(q.Branches, []string{"Main"}) ||
		!q.After.Equal(now.Add(-72*time.Hour)) || !q.Before.IsZero() || q.Problem != nil {
		t.Fatalf("got %+v", q)
	}
}

func TestUnknownIsAndAgentValuesAreStillDroppedQuietly(t *testing.T) {
	q := Parse("is:bogus agent:bogus ledger is:Waiting agent:Codex IS:AGENT")
	if q.Words != "ledger" || q.Problem != nil || !q.Agents || len(q.Statuses) != 1 || len(q.From) != 1 || q.From[0].ID != "codex" {
		t.Fatalf("got %+v", q)
	}
}

func TestAFilterThatCannotBeReadSaysWhatItTakesAndIsNotSearched(t *testing.T) {
	inZone(t, "Europe/Berlin")
	when := ": takes a day like 2026-10-09, today, yesterday, 12h, 3d, or 2w."
	cases := map[string]string{
		"in:":              "in: takes here, as in in:here.",
		"in:there":         "in: takes here, as in in:here.",
		"branch:":          "branch: takes a branch name, like branch:main.",
		"after:":           "after" + when,
		"after:3days":      "after" + when,
		"after:0d":         "after" + when,
		"after:10000d":     "after" + when,
		"after:2026-1":     "after" + when,
		"after:2026-02-30": "after" + when,
		"after:2026-10-9":  "after" + when,
		"before:3m":        "before" + when,
		"Before:last-week": "before" + when,
	}
	for token, hint := range cases {
		q := Parse("ledger " + token + " after:2026-1-1")
		if q.Problem == nil || q.Problem.Token != token || q.Problem.Hint != hint || q.Words != "ledger" {
			t.Errorf("%s: got %+v %+v", token, q, q.Problem)
			continue
		}
		if got := q.Problem.Error(); got != "can't read "+token+". "+hint {
			t.Errorf("%s: error %q", token, got)
		}
	}
}

func TestDaysStartAtLocalMidnightAndCountsSlideFromNow(t *testing.T) {
	berlin := inZone(t, "Europe/Berlin")
	cases := []struct {
		now   time.Time
		value string
		want  time.Time
	}{
		{time.Date(2026, 10, 10, 15, 0, 0, 0, berlin), "2026-10-09", time.Date(2026, 10, 9, 0, 0, 0, 0, berlin)},
		{time.Date(2026, 10, 10, 15, 0, 0, 0, berlin), "today", time.Date(2026, 10, 10, 0, 0, 0, 0, berlin)},
		{time.Date(2026, 10, 10, 15, 0, 0, 0, berlin), "YESTERDAY", time.Date(2026, 10, 9, 0, 0, 0, 0, berlin)},
		{time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC), "today", time.Date(2026, 10, 10, 0, 0, 0, 0, berlin)},
		{time.Date(2026, 10, 10, 23, 30, 0, 0, time.UTC), "today", time.Date(2026, 10, 11, 0, 0, 0, 0, berlin)},
		{time.Date(2026, 10, 26, 10, 0, 0, 0, berlin), "yesterday", time.Date(2026, 10, 25, 0, 0, 0, 0, berlin)},
		{time.Date(2026, 10, 25, 15, 0, 0, 0, berlin), "today", time.Date(2026, 10, 25, 0, 0, 0, 0, berlin)},
		{time.Date(2026, 10, 25, 15, 0, 0, 0, berlin), "1d", time.Date(2026, 10, 24, 16, 0, 0, 0, berlin)},
		{time.Date(2026, 10, 10, 15, 0, 0, 0, berlin), "12h", time.Date(2026, 10, 10, 3, 0, 0, 0, berlin)},
		{time.Date(2026, 10, 10, 15, 0, 0, 0, berlin), "2w", time.Date(2026, 9, 26, 15, 0, 0, 0, berlin)},
		{time.Date(2026, 10, 10, 15, 0, 0, 0, berlin), "9999h", time.Date(2026, 10, 10, 15, 0, 0, 0, berlin).Add(-9999 * time.Hour)},
	}
	for _, c := range cases {
		after, before := parseAt("after:"+c.value, c.now), parseAt("before:"+c.value, c.now)
		if !after.After.Equal(c.want) || !before.Before.Equal(c.want) || after.Problem != nil {
			t.Errorf("%s at %s: after %s before %s, want %s", c.value, c.now, after.After, before.Before, c.want)
		}
	}
}

func TestSeveralBoundsAllHold(t *testing.T) {
	berlin := inZone(t, "Europe/Berlin")
	now := time.Date(2026, 10, 10, 15, 0, 0, 0, berlin)
	q := parseAt("after:3d before:2026-10-01 after:1d before:2026-09-01 after:2026-01-01", now)
	if !q.After.Equal(now.Add(-24*time.Hour)) || !q.Before.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, berlin)) {
		t.Fatalf("after %s before %s", q.After, q.Before)
	}
}

func TestAfterAndBeforeKeepOneDayBetweenThem(t *testing.T) {
	berlin := inZone(t, "Europe/Berlin")
	at := func(path string, day, h, m, s int) Row {
		return Row{Path: path, Session: Session{Mtime: float64(time.Date(2026, 10, day, h, m, s, 0, berlin).Unix())}}
	}
	rows := []Row{at("8th-late", 8, 23, 59, 59), at("9th-first", 9, 0, 0, 0), at("9th-late", 9, 23, 59, 59), at("10th-first", 10, 0, 0, 0)}
	now := time.Date(2026, 10, 10, 15, 0, 0, 0, berlin)
	cases := map[string][]string{
		"after:2026-10-09 before:2026-10-10": {"9th-first", "9th-late"},
		"after:2026-10-09":                   {"9th-first", "9th-late", "10th-first"},
		"before:2026-10-09":                  {"8th-late"},
		"after:yesterday before:today":       {"9th-first", "9th-late"},
		"after:today before:yesterday":       nil,
		"after:2026-11-01":                   nil,
	}
	for query, want := range cases {
		if got := pathsOf(parseAt(query, now).Rows(rows, false)); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", query, got, want)
		}
	}
}

func TestBranchKeepsAnyOfTheNamedBranchesIgnoringCase(t *testing.T) {
	rows := []Row{
		{Path: "main", Session: Session{Branch: "main"}},
		{Path: "auth", Session: Session{Branch: "feat/auth"}},
		{Path: "none", Session: Session{}},
		{Path: "mainline", Session: Session{Branch: "mainline"}},
	}
	cases := map[string][]string{
		"branch:MAIN branch:feat/auth": {"main", "auth"},
		"branch:main":                  {"main"},
		"branch:mai":                   nil,
	}
	for query, want := range cases {
		if got := pathsOf(Parse(query).Rows(rows, false)); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", query, got, want)
		}
	}
}

func TestInHereKeepsThisFolderTheFoldersInsideItAndTheFoldersAboveIt(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	for _, dir := range []string{"home/app/sub", "home/application", "elsewhere"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(home, "app"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	rows := []Row{
		{Path: "app", Session: Session{Cwd: filepath.Join(home, "app")}},
		{Path: "sub", Session: Session{Cwd: filepath.Join(home, "app", "sub") + "/"}},
		{Path: "linked-sub", Session: Session{Cwd: filepath.Join(root, "link", "sub")}},
		{Path: "real-app", Session: Session{Cwd: filepath.Join(real, "home", "app")}},
		{Path: "application", Session: Session{Cwd: filepath.Join(home, "application")}},
		{Path: "home", Session: Session{Cwd: home}},
		{Path: "root", Session: Session{Cwd: "/"}},
		{Path: "elsewhere", Session: Session{Cwd: filepath.Join(root, "elsewhere")}},
		{Path: "parent", Session: Session{Cwd: root}},
		{Path: "none", Session: Session{}},
	}
	cases := []struct {
		pwd  string
		want []string
	}{
		{filepath.Join(home, "app"), []string{"app", "sub", "linked-sub", "real-app", "parent"}},
		{filepath.Join(home, "app", "sub"), []string{"app", "sub", "linked-sub", "real-app", "parent"}},
		{filepath.Join(root, "link"), []string{"app", "sub", "linked-sub", "real-app", "parent"}},
		{filepath.Join(real, "link", "sub"), []string{"app", "sub", "linked-sub", "real-app", "parent"}},
		{home, []string{"app", "sub", "linked-sub", "real-app", "application", "home", "parent"}},
		{filepath.Join(root, "elsewhere"), []string{"elsewhere", "parent"}},
		{"/", []string{"app", "sub", "linked-sub", "real-app", "application", "home", "root", "elsewhere", "parent"}},
	}
	for _, c := range cases {
		t.Setenv("PWD", c.pwd)
		if got := pathsOf(Parse("in:here").Rows(rows, false)); !slices.Equal(got, c.want) {
			t.Errorf("from %s: %v, want %v", strings.TrimPrefix(c.pwd, root), got, c.want)
		}
	}
	t.Chdir(filepath.Join(home, "application"))
	for _, pwd := range []string{"", "home/app"} {
		t.Setenv("PWD", pwd)
		if pwd == "" {
			os.Unsetenv("PWD")
		}
		if got := pathsOf(Parse("in:here").Rows(rows, false)); !slices.Equal(got, []string{"application", "parent"}) {
			t.Errorf("with PWD %q, from the process's folder: %v", pwd, got)
		}
	}
}

func TestLastPicksTheNewestRealSessionHere(t *testing.T) {
	t.Setenv("PWD", "/work/app")
	rows := []Row{
		{Path: "elsewhere", Session: Session{Cwd: "/work/other", Mtime: 500, Prompts: 5}},
		{Path: "hi", Session: Session{Cwd: "/work/app", Mtime: 400, Prompts: 1}},
		{Path: "older", Session: Session{Cwd: "/work/app", Mtime: 100, Prompts: 5}},
		{Path: "sub", Session: Session{Cwd: "/work/app/web", Mtime: 300, Prompts: 5}},
		{Path: "exec", Session: Session{Cwd: "/work/app", Mtime: 450, Prompts: 5, ByAgent: true}},
	}
	for query, want := range map[string]string{"": "sub", "is:agent": "exec", "after:2026-01-01": ""} {
		r, ok := Last(Parse(query), rows)
		if r.Path != want || ok != (want != "") {
			t.Errorf("%q: %q %v, want %q", query, r.Path, ok, want)
		}
	}
	if r, _ := Last(Query{}, rows[1:2]); r.Path != "hi" {
		t.Errorf("with only a throwaway here, got %q", r.Path)
	}
}
