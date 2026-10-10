package tui

import (
	"context"
	"database/sql"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/adammiribyan/superfind/internal/agent"
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

func transcriptPicker(t *testing.T, count int) picker {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", "")
	var steps []string
	for i := 1; i <= 30; i++ {
		steps = append(steps, fmt.Sprintf("Step %d is done.", i))
	}
	transcript := `{"type":"user","cwd":"/tmp","entrypoint":"cli","message":{"content":"walk the ledger migration through every step"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"` + strings.Join(steps, `\n\n`) + `"}]}}` + "\n"
	titles := []string{"Ledger migration", "Invoice layout", "Billing retries", "Webhook signatures", "Tax rounding", "Refund queue", "Payout report", "Card vault"}
	var rows []session.Row
	for i := range count {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, session.Row{Path: path, Agent: agent.Agents[0],
			Session: session.Session{Title: titles[i%len(titles)], Cwd: "/tmp", Mtime: session.Now() - float64(i*3600+60), Prompts: 5}})
	}
	return newPicker(rows, nil, "")
}

func settle(m picker, msgs ...tea.Msg) picker {
	for len(msgs) > 0 {
		msg := msgs[0]
		msgs = msgs[1:]
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, cmd := range batch {
				if cmd != nil {
					msgs = append(msgs, cmd())
				}
			}
			continue
		}
		next, cmd := m.Update(msg)
		m = next.(picker)
		if cmd != nil {
			msgs = append(msgs, cmd())
		}
	}
	return m
}

func frame(m picker, width, height int) []string {
	m = settle(m, tea.WindowSizeMsg{Width: width, Height: height}, colorsSettled{})
	return strings.Split(ansi.Strip(m.View().Content), "\n")
}

func TestThePickerAtEachSplitSize(t *testing.T) {
	cases := []struct {
		width, height, sessions int
		want                    map[int]string
	}{
		{140, 36, 8, map[int]string{
			1:  " ╭─ Claude sessions ──────────────────────────────────────────────────────╮ │ Ledger migration",
			2:  " │ Search sessions and conversations                                  8/8 │ │ tmp · active now · 5 prompts",
			6:  "  Invoice layout                                             tmp        1h  │ you",
			35: "  enter resume · tab view · ctrl-r ask                                      │",
		}},
		{100, 30, 8, map[int]string{
			1:  " ╭─ Claude sessions ──────────────────────────────────────────────────────────────────────────────╮",
			2:  " │ Search sessions and conversations                                                          8/8 │",
			16: " ──────────────────────────────────────────────────────────────────────────────────────────────────",
			17: " Ledger migration",
			22: " you",
			29: "  enter resume · tab view · ctrl-r ask",
		}},
		{80, 24, 8, map[int]string{
			2:  " │ Search sessions and conversations                                      8/8 │",
			9:  "  Tax rounding                                                   tmp        4h",
			10: " ──────────────────────────────────────────────────────────────────────────────",
			16: " you",
			23: "  enter resume · tab view · ctrl-r ask",
		}},
		{60, 16, 12, map[int]string{
			1:  "  Claude sessions  Search sessions and conversations 12/12",
			2:  "  Ledger migration  active now               tmp        1m",
			13: "  Webhook signatures                         tmp       11h",
			14: "",
			15: "  enter resume · tab view · ctrl-r ask",
		}},
		{40, 10, 8, map[int]string{
			1: "  Claude sessions  Search sessions 8/8",
			2: "  Ledger migrat…  active now        1m",
			7: "  Refund queue                      5h",
			8: "",
			9: "  enter resume · tab view · ctrl-r ask",
		}},
	}
	for _, c := range cases {
		lines := frame(transcriptPicker(t, c.sessions), c.width, c.height)
		if len(lines) != c.height {
			t.Errorf("at %dx%d the frame is %d lines, want %d", c.width, c.height, len(lines), c.height)
			continue
		}
		for i, want := range c.want {
			if got := strings.TrimRight(lines[i], " "); got != want {
				t.Errorf("at %dx%d line %d reads\n%q\nwant\n%q", c.width, c.height, i, got, want)
			}
		}
	}
}

func TestShiftUpPagesTheRecentViewLikeAltUp(t *testing.T) {
	m := settle(transcriptPicker(t, 2), tea.WindowSizeMsg{Width: 80, Height: 24}, colorsSettled{}, tea.KeyPressMsg{Code: tea.KeyTab})
	bottom := m.body.YOffset()
	altUp := settle(m, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
	shift := settle(m, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	alt := altUp.body.YOffset()
	if alt >= bottom || shift.body.YOffset() != alt {
		t.Fatalf("from offset %d alt-up went to %d and shift-up to %d, want both a page up", bottom, alt, shift.body.YOffset())
	}
	down := settle(shift, tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	if down.body.YOffset() != bottom {
		t.Fatalf("shift-down went to offset %d, want back to %d", down.body.YOffset(), bottom)
	}
}

func TestANarrowFooterDropsTabBeforeAsk(t *testing.T) {
	m := testPicker(t)
	m.colors.settle()
	if got := ansi.Strip(m.footer(30)); got != " enter resume · ctrl-r ask" {
		t.Fatalf("the footer at 30 columns reads %q", got)
	}
}

func TestANarrowNoMatchMessageKeepsItsFirstSentence(t *testing.T) {
	m := transcriptPicker(t, 2)
	m.db = searchDB(t, map[string]string{m.rows[0].Path: "Ledger migration"})
	m.input.SetValue("zebra")
	m.filter()
	if got := strings.TrimRight(frame(m, 40, 10)[2], " "); got != "  No session contains these words." {
		t.Fatalf("the message line at 40 columns reads %q", got)
	}
}
