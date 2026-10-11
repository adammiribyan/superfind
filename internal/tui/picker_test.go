package tui

import (
	"context"
	"database/sql"
	"image/color"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
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

func TestATerminalThatAnswersWithoutItsColorsGetsThePickerWithoutWaiting(t *testing.T) {
	m := testPicker(t)
	next, load := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	for load != nil {
		next, load = next.Update(load())
	}
	if view := next.View().Content; view != "" {
		t.Fatalf("the picker appeared before the terminal answered:\n%s", view)
	}
	next, _ = next.Update(uv.PrimaryDeviceAttributesEvent{62, 22})
	if view := next.View().Content; !strings.Contains(view, "Ledger migration") {
		t.Fatalf("after the terminal's device attributes, the picker showed %q, want the list", view)
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
