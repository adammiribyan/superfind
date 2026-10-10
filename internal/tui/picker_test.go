package tui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/open"
	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
)

func testPicker(t *testing.T) picker {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", "")
	rows := []session.Row{
		{Path: "a", Agent: agent.Agents[0], Session: session.Session{Title: "Ledger migration", Cwd: "/tmp", Mtime: 2, Prompts: 5}},
		{Path: "b", Agent: agent.Agents[0], Session: session.Session{Title: "Invoice layout", Cwd: "/tmp", Mtime: 1, Prompts: 5}},
	}
	m := newPicker(rows, nil, "")
	m.input.SetValue("ledger")
	return m
}

func searchDB(t *testing.T, titles map[string]string) *sql.DB {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	db, err := session.OpenSearch()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for path, title := range titles {
		if _, err := db.Exec("INSERT INTO docs (path, title, body) VALUES (?, ?, '')", path, title); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func press(m picker, code rune) (picker, tea.Msg) {
	next, cmd := m.Update(tea.KeyPressMsg{Code: code})
	var msg tea.Msg
	if cmd != nil {
		msg = cmd()
	}
	return next.(picker), msg
}

func TestEscapeStopsAnAskAndReturnsToTheKeywordSearch(t *testing.T) {
	for _, askedFor := range []string{"", "ledger"} {
		m := testPicker(t)
		m.db = searchDB(t, map[string]string{"a": "Ledger migration", "b": "Invoice layout"})
		ctx, cancel := context.WithCancel(context.Background())
		m.asks.ctx, m.asks.cancel = ctx, cancel
		m.asking, m.askedFor = askedFor == "", askedFor
		m.shown = m.all[1:]
		m, msg := press(m, tea.KeyEscape)
		if _, quit := msg.(tea.QuitMsg); quit || m.quitting {
			t.Fatalf("asking=%v askedFor=%q: escape quit the picker", askedFor == "", askedFor)
		}
		if shown := pathsOf(m.shown); m.phase() != searching || m.input.Value() != "ledger" || !slices.Equal(shown, []string{"a"}) || ctx.Err() == nil {
			t.Fatalf("after escape: phase %d, search %q, shown %v, ask stopped %v; want the keyword search for ledger showing [a] and the ask stopped",
				m.phase(), m.input.Value(), shown, ctx.Err() != nil)
		}
		if _, msg := press(m, tea.KeyEscape); msg != (tea.QuitMsg{}) {
			t.Fatalf("a second escape sent %v, want it to quit", msg)
		}
	}
}

func TestCtrlRWithNoSessionsDoesNotAsk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newPicker(nil, searchDB(t, nil), "")
	m.input.SetValue("ledger")
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m := next.(picker); m.phase() != searching || cmd != nil {
		t.Fatalf("ctrl-r with no sessions left the picker in phase %d with a command %v, want it still searching with nothing started", m.phase(), cmd != nil)
	}
}

func TestTheFooterHintsFollowTheState(t *testing.T) {
	m := testPicker(t)
	cases := []struct {
		name   string
		change func(*picker)
		want   []string
	}{
		{"searching", func(*picker) {}, []string{"enter resume", "tab view", "ctrl-r ask"}},
		{"no matches", func(m *picker) { m.shown = nil }, []string{"ctrl-r ask", "ctrl-u clear search"}},
		{"asking", func(m *picker) { m.asking, m.shown = true, nil }, []string{"esc stop asking"}},
		{"asked", func(m *picker) { m.askedFor = "ledger" }, []string{"enter resume", "tab view", "esc back to search"}},
		{"asked nothing", func(m *picker) { m.askedFor, m.shown = "ledger", nil }, []string{"esc back to search"}},
		{"no sessions", func(m *picker) { m.all, m.shown = nil, nil }, []string{"esc quit"}},
		{"asking with no sessions", func(m *picker) { m.all, m.shown, m.asking = nil, nil, true }, []string{"esc stop asking"}},
		{"asked with no sessions", func(m *picker) { m.all, m.shown, m.askedFor = nil, nil, "ledger" }, []string{"esc back to search"}},
	}
	for _, c := range cases {
		state := m
		c.change(&state)
		var got []string
		for _, h := range state.hints() {
			got = append(got, h[0]+" "+h[1])
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: hints %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTheSelectionTintIsAQuarterBlueOnDarkAndLightGrounds(t *testing.T) {
	cases := []struct {
		bg, want color.RGBA
	}{
		{color.RGBA{0x1e, 0x1e, 0x1e, 255}, color.RGBA{46, 63, 82, 255}},
		{color.RGBA{0xff, 0xff, 0xff, 255}, color.RGBA{200, 217, 241, 255}},
	}
	for _, c := range cases {
		th := theme{bg: c.bg, fg: color.RGBA{0x80, 0x80, 0x80, 255}}
		th.settle()
		if th.tint != c.want {
			t.Errorf("on %v the tint is %v, want %v", c.bg, th.tint, c.want)
		}
	}
}

func TestAStatusFollowsTheTitleUntilTheTitleWouldGetUnderEightCells(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", "")
	row := session.Row{Path: "b", Agent: agent.Agents[0], Status: session.WaitingLabel,
		Session: session.Session{Title: "Invoice layout", Cwd: "/tmp", Mtime: session.Now(), Prompts: 5}}
	m := newPicker([]session.Row{row}, nil, "")
	cases := map[int]string{
		50: " Invoice layout  waiting on you                1m ",
		36: " Invoice…  waiting on you        1m ",
		35: " Invoice layout                 1m ",
	}
	for width, want := range cases {
		if got := ansi.Strip(m.rowLine(row, false, width, render.Columns{}, nil)); got != want {
			t.Errorf("at width %d the row reads %q, want %q", width, got, want)
		}
	}
}

func TestARunningPickerMarksASessionThatOpensWithoutMovingFocus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	rows := []session.Row{
		{Path: "a", Agent: agent.Named("claude"), Session: session.Session{ID: "id-a", Title: "Ledger migration", Cwd: "/tmp", Mtime: 2, Prompts: 5}},
		{Path: "b", Agent: agent.Named("claude"), Session: session.Session{ID: "id-b", Title: "Invoice layout", Cwd: "/tmp", Mtime: 1, Prompts: 5}},
	}
	m := newPicker(rows, nil, "")
	m.cursor = 1
	openOf := func(m picker, path string) *open.State {
		i := slices.IndexFunc(m.shown, func(r session.Row) bool { return r.Path == path })
		return m.shown[i].Open
	}
	held := func() heldMsg {
		return heldMsg{open.KeyOf("claude", "id-a"): {PID: 4242, Status: "busy", Tmux: &open.Pane{ID: "%3", Session: "work", Window: 1}}}
	}
	next, _ := m.Update(held())
	m = next.(picker)
	want := open.State{PID: 4242, Status: "busy", Tmux: &open.Pane{ID: "%3", Session: "work", Window: 1}}
	if got := openOf(m, "a"); got == nil || got.PID != want.PID || got.Status != want.Status || *got.Tmux != *want.Tmux {
		t.Fatalf("row a open = %+v, want %+v", got, want)
	}
	if got := openOf(m, "b"); got != nil {
		t.Fatalf("row b open = %+v, want nil", got)
	}
	if m.focused().Path != "b" {
		t.Fatalf("focus moved to %s", m.focused().Path)
	}
	before := openOf(m, "a")
	next, _ = m.Update(held())
	if got := openOf(next.(picker), "a"); got != before {
		t.Fatalf("an unchanged lookup rebuilt the rows: open %p, was %p", got, before)
	}
	next, _ = next.(picker).Update(heldMsg{})
	if got := openOf(next.(picker), "a"); got != nil {
		t.Fatalf("after the process exited: row a open = %+v", got)
	}
}

func openedByThisProcess(t *testing.T, id string) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("open sessions are found on macOS and Linux only")
	}
	home := t.TempDir()
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "SUPERFIND_CLAUDE_DIRS", "CODEX_HOME", "SUPERFIND_CODEX_DIRS", "GROK_HOME", "SUPERFIND_GROK_DIRS"} {
		t.Setenv(name, "")
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	sessions := filepath.Join(home, ".claude", "sessions")
	for _, dir := range []string{filepath.Join(home, ".claude", "projects", "-tmp"), sessions} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": id, "startedAt": time.Now().UnixMilli(), "status": "idle"})
	if err := os.WriteFile(filepath.Join(sessions, fmt.Sprintf("%d.json", os.Getpid())), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func openPicker(t *testing.T) picker {
	t.Helper()
	rows := []session.Row{
		{Path: "a", Agent: agent.Named("claude"), Session: session.Session{ID: "id-a", Title: "Ledger migration", Cwd: "/tmp", Mtime: 2, Prompts: 5}},
		{Path: "b", Agent: agent.Named("claude"), Session: session.Session{ID: "id-b", Title: "Invoice layout", Cwd: "/tmp", Mtime: 1, Prompts: 5}},
	}
	m := newPicker(rows, nil, "")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	m = next.(picker)
	m.started = true
	for i := range m.shown {
		m.shown[i].Open = &open.State{PID: 4242, Tmux: &open.Pane{ID: "%3", Session: "work", Window: 1}}
	}
	return m
}

func TestEnterOnASessionStillOpenOutsideAPaneSaysSoAndKeepsThePickerOpen(t *testing.T) {
	openedByThisProcess(t, "id-a")
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1,0")
	m := openPicker(t)
	m, msg := press(m, tea.KeyEnter)
	if _, quit := msg.(tea.QuitMsg); quit || m.quitting || m.chosen != nil {
		t.Fatalf("enter on a session open outside tmux quit=%v chosen=%v, want the picker still open with nothing chosen", quit || m.quitting, m.chosen)
	}
	if screen := ansi.Strip(m.View().Content); !strings.Contains(screen, "Already open in another terminal window.") {
		t.Fatalf("after enter the screen reads\n%s\nwant it to say the session is already open in another terminal window", screen)
	}
	m, _ = press(m, tea.KeyDown)
	if screen := ansi.Strip(m.View().Content); strings.Contains(screen, "Already open") {
		t.Fatalf("after moving down the screen still reads\n%s", screen)
	}
}

func TestEnterOnASessionThatClosedSinceTheLastLookResumesIt(t *testing.T) {
	openedByThisProcess(t, "id-a")
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1,0")
	m := openPicker(t)
	m.cursor = 1
	m, msg := press(m, tea.KeyEnter)
	if msg != (tea.QuitMsg{}) || m.chosen == nil || m.chosen.Path != "b" || m.chosen.Open != nil {
		t.Fatalf("enter on a closed session sent %v and chose %+v, want it to quit choosing b with nothing open", msg, m.chosen)
	}
}

func TestEnterGoesToTheTmuxPaneOnlyWhenTheSessionIsOpenInOneAndSuperfindRunsInTmux(t *testing.T) {
	pane := &open.Pane{ID: "%3", Session: "work", Window: 1}
	cases := []struct {
		name   string
		fresh  *open.State
		inTmux bool
		want   enterAction
	}{
		{"closed", nil, true, resumeIt},
		{"closed outside tmux", nil, false, resumeIt},
		{"open in a pane", &open.State{PID: 4242, Tmux: pane}, true, goToPane},
		{"open in a pane outside tmux", &open.State{PID: 4242, Tmux: pane}, false, sayWhere},
		{"open without a pane", &open.State{PID: 4242}, true, sayWhere},
	}
	for _, c := range cases {
		if got := onEnter(c.fresh, c.inTmux); got != c.want {
			t.Errorf("%s: enter does %d, want %d", c.name, got, c.want)
		}
	}
}

func TestTheEnterHintSaysGoToItForASessionOpenInATmuxPane(t *testing.T) {
	for _, tmux := range []string{"/private/tmp/tmux-501/default,1,0", ""} {
		t.Setenv("TMUX", tmux)
		m := testPicker(t)
		m.shown[0].Open = &open.State{PID: 4242, Tmux: &open.Pane{ID: "%3", Session: "work", Window: 1}}
		want := "go to it"
		if tmux == "" {
			want = "resume"
		}
		if got := m.hints()[0]; got != [2]string{"enter", want} {
			t.Errorf("TMUX=%q: first hint %q, want enter %s", tmux, got, want)
		}
		m.shown[0].Open = &open.State{PID: 4242}
		if got := m.hints()[0]; got != [2]string{"enter", "resume"} {
			t.Errorf("TMUX=%q, open without a pane: first hint %q, want enter resume", tmux, got)
		}
	}
}
