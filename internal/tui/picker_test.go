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
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/open"
	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
)

func TestMain(m *testing.M) {
	empty, err := os.MkdirTemp("", "no-osascript")
	if err != nil {
		panic(err)
	}
	os.Setenv("PATH", empty)
	code := m.Run()
	os.Remove(empty)
	os.Exit(code)
}

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
			5:  "  Ledger migration  active now                               tmp        1m  │ you",
			35: "  enter resume · tab view · ctrl-r ask                                      │",
		}},
		{100, 30, 8, map[int]string{
			1:  " ╭─ Claude sessions ──────────────────────────────────────────────────────────────────────────────╮",
			2:  " │ Search sessions and conversations                                                          8/8 │",
			16: " ──────────────────────────────────────────────────────────────────────────────────────────────────",
			17: " Ledger migration",
			21: " you",
			29: "  enter resume · tab view · ctrl-r ask",
		}},
		{80, 24, 8, map[int]string{
			2:  " │ Search sessions and conversations                                      8/8 │",
			9:  "  Tax rounding                                                   tmp        4h",
			10: " ──────────────────────────────────────────────────────────────────────────────",
			15: " you",
			23: "  enter resume · tab view · ctrl-r ask",
		}},
		{80, 20, 12, map[int]string{
			1:  "  Claude sessions  Search sessions and conversations                     12/12",
			2:  "  Ledger migration  active now                                   tmp        1m",
			18: "",
			19: "  enter resume · tab view · ctrl-r ask",
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
		{20, 5, 8, map[int]string{
			1: "  Search       8/8",
			4: "  enter resume",
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
	if got := strings.TrimRight(frame(m, 40, 10)[2], " "); got != "  No keyword matches." {
		t.Fatalf("the message line at 40 columns reads %q", got)
	}
}

func TestAShortWidePaneDrawsEveryLineWithinTheScreen(t *testing.T) {
	for height := 3; height <= 10; height++ {
		lines := frame(transcriptPicker(t, 3), 140, height)
		if len(lines) > height {
			t.Errorf("at 140x%d the frame is %d lines", height, len(lines))
		}
		for i, line := range lines {
			if w := ansi.StringWidth(line); w > 140 {
				t.Errorf("at 140x%d line %d is %d cells wide", height, i, w)
			}
		}
	}
}

func TestALongQueryStaysInViewWhenAskResultsNarrowTheSearch(t *testing.T) {
	m := settle(transcriptPicker(t, 8), tea.WindowSizeMsg{Width: 60, Height: 16}, colorsSettled{})
	query := "ledger migration through every step"
	m.input.SetValue(query)
	m = settle(m, askDone{seq: m.askSeq, query: query, paths: []string{m.rows[0].Path}})
	line := strings.TrimRight(strings.Split(ansi.Strip(m.View().Content), "\n")[1], " ")
	if !strings.HasSuffix(line, "through every step  1/8") {
		t.Fatalf("after ask the search line reads %q, want it to end with the end of the query", line)
	}
}

func TestResizingTheRecentViewReusesItsBody(t *testing.T) {
	m := settle(transcriptPicker(t, 1), tea.WindowSizeMsg{Width: 140, Height: 36}, colorsSettled{}, tea.KeyPressMsg{Code: tea.KeyTab})
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 140, Height: 35})
	if cmd != nil {
		t.Fatalf("resizing the recent view from 36 to 35 rows started a new read")
	}
	m = settle(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 140, Height: 35}); cmd == nil {
		t.Fatalf("resizing where the session left off did not refit its body")
	}
}

func TestAWidePaneTooShortForAPreviewGivesTheListTheWholeWidth(t *testing.T) {
	lines := frame(transcriptPicker(t, 3), 120, 11)
	if got := strings.TrimRight(lines[2], " "); got != "  Ledger migration  active now                                                                           tmp        1m" {
		t.Fatalf("at 120x11 a row reads %q, want the list across the whole width", got)
	}
	if lines := frame(transcriptPicker(t, 3), 120, 12); !strings.Contains(lines[5], "│ you") {
		t.Fatalf("at 120x12 line 5 reads %q, want the preview beside the list starting with you", lines[5])
	}
}

func TestANarrowFooterAfterAskKeepsTheWayBackToSearch(t *testing.T) {
	m := testPicker(t)
	m.colors.settle()
	m.askedFor = "ledger"
	if got := ansi.Strip(m.footer(24)); got != " esc back to search" {
		t.Fatalf("the footer after ask at 24 columns reads %q", got)
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
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
	m = next.(picker)
	m.started = true
	for i := range m.shown {
		m.shown[i].Open = &open.State{PID: 4242, Tmux: &open.Pane{ID: "%3", Session: "work", Window: 1}}
	}
	m.focus = func(open.State) bool { return false }
	return m
}

const confirmLine = "Already open. Enter again resumes a copy. Esc cancels."

func screenOf(m picker) string { return ansi.Strip(m.View().Content) }

func TestEnterOnASessionOpenElsewhereAsksFirstAndEnterAgainResumesACopy(t *testing.T) {
	openedByThisProcess(t, "id-a")
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1,0")
	m := openPicker(t)
	if !m.compact() {
		t.Fatal("the picker is not in its short layout")
	}
	m, msg := press(m, tea.KeyEnter)
	if _, quit := msg.(tea.QuitMsg); quit || m.quitting || m.chosen != nil {
		t.Fatalf("the first enter quit=%v chose %v, want the picker still open with nothing chosen", quit || m.quitting, m.chosen)
	}
	screen := screenOf(m)
	if !strings.Contains(screen, confirmLine) || !strings.Contains(screen, "enter resume a copy") {
		t.Fatalf("after enter the screen reads\n%s\nwant %q and the hint enter resume a copy", screen, confirmLine)
	}
	m, msg = press(m, tea.KeyEnter)
	if msg != (tea.QuitMsg{}) || m.chosen == nil || m.chosen.Path != "a" || m.chosen.Open == nil || m.chosen.Open.PID != os.Getpid() {
		t.Fatalf("the second enter sent %v and chose %+v, want it to quit choosing a, open in this process", msg, m.chosen)
	}
}

func TestEscOnTheQuestionGoesBackToThePicker(t *testing.T) {
	openedByThisProcess(t, "id-a")
	t.Setenv("TMUX", "")
	m, _ := press(openPicker(t), tea.KeyEnter)
	m, msg := press(m, tea.KeyEscape)
	if msg != nil || m.quitting {
		t.Fatalf("esc on the question sent %v quitting=%v, want the picker still open", msg, m.quitting)
	}
	if screen := screenOf(m); strings.Contains(screen, confirmLine) || !strings.Contains(screen, "enter resume ·") {
		t.Fatalf("after esc the screen reads\n%s\nwant the question gone and the hint enter resume", screen)
	}
	m, _ = press(m, tea.KeyEnter)
	if m.chosen != nil || !strings.Contains(screenOf(m), confirmLine) {
		t.Fatalf("enter after esc chose %+v, want the question again", m.chosen)
	}
	m, _ = press(m, tea.KeyEscape)
	if _, msg = press(m, tea.KeyEscape); msg != (tea.QuitMsg{}) {
		t.Fatalf("a second esc sent %v, want it to quit the picker", msg)
	}
}

func TestAnyOtherKeyAPasteOrTheSessionClosingTakesTheQuestionAway(t *testing.T) {
	openedByThisProcess(t, "id-a")
	t.Setenv("TMUX", "")
	asked := func(t *testing.T) picker {
		m, _ := press(openPicker(t), tea.KeyEnter)
		if !strings.Contains(screenOf(m), confirmLine) {
			t.Fatalf("after enter the screen reads\n%s\nwant %q", screenOf(m), confirmLine)
		}
		return m
	}
	m, _ := press(asked(t), tea.KeyDown)
	m, _ = press(m, tea.KeyUp)
	if screen := screenOf(m); strings.Contains(screen, confirmLine) {
		t.Errorf("after moving down and back the screen still reads\n%s", screen)
	}
	if m, _ = press(m, tea.KeyEnter); m.chosen != nil {
		t.Errorf("enter after moving away and back chose %+v, want the question again", m.chosen)
	}
	next, _ := asked(t).Update(heldMsg{})
	if screen := screenOf(next.(picker)); strings.Contains(screen, confirmLine) {
		t.Errorf("after the session closed the screen still reads\n%s", screen)
	}
	next, _ = asked(t).Update(tea.PasteMsg{Content: "ledger"})
	if screen := screenOf(next.(picker)); strings.Contains(screen, confirmLine) {
		t.Errorf("after a paste the screen still reads\n%s", screen)
	}
}

func TestEnterOnASessionInAGhosttyTabSwitchesToItAndHandsTheShellNothing(t *testing.T) {
	openedByThisProcess(t, "id-a")
	t.Setenv("TMUX", "")
	m := openPicker(t)
	var asked []int
	m.focus = func(s open.State) bool {
		asked = append(asked, s.PID)
		return true
	}
	m, msg := press(m, tea.KeyEnter)
	if msg != (tea.QuitMsg{}) || m.chosen != nil || !slices.Equal(asked, []int{os.Getpid()}) {
		t.Fatalf("enter sent %v, chose %+v and asked Ghostty about %v, want it to quit choosing nothing after asking about %d",
			msg, m.chosen, asked, os.Getpid())
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

func TestEnterKeepsTheLastKnownPaneOnlyForTheSameProcess(t *testing.T) {
	pane := &open.Pane{ID: "%3", Session: "work", Window: 1}
	other := &open.Pane{ID: "%9", Session: "play", Window: 2}
	cases := []struct {
		name          string
		cached, fresh *open.State
		want          *open.State
	}{
		{"closed", nil, nil, nil},
		{"closed since the last look", &open.State{PID: 4242, Tmux: pane}, nil, nil},
		{"tmux did not answer the fresh look", &open.State{PID: 4242, Tmux: pane}, &open.State{PID: 4242}, &open.State{PID: 4242, Tmux: pane}},
		{"another process has it open now", &open.State{PID: 4242, Tmux: pane}, &open.State{PID: 4343}, &open.State{PID: 4343}},
		{"moved to another pane", &open.State{PID: 4242, Tmux: pane}, &open.State{PID: 4242, Tmux: other}, &open.State{PID: 4242, Tmux: other}},
	}
	for _, c := range cases {
		got := freshest(c.cached, c.fresh)
		if (got == nil) != (c.want == nil) || got != nil && !got.Equal(*c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestTheEnterHintSaysWhatEnterDoesOnTheFocusedSession(t *testing.T) {
	pane := &open.Pane{ID: "%3", Session: "work", Window: 1}
	cases := []struct {
		tmux string
		open *open.State
		want string
	}{
		{"/private/tmp/tmux-501/default,1,0", &open.State{PID: 4242, Tmux: pane}, "go to it"},
		{"", &open.State{PID: 4242, Tmux: pane}, "resume"},
		{"/private/tmp/tmux-501/default,1,0", &open.State{PID: 4242}, "resume"},
		{"/private/tmp/tmux-501/default,1,0", nil, "resume"},
	}
	for _, c := range cases {
		t.Setenv("TMUX", c.tmux)
		m := testPicker(t)
		m.shown[0].Open = c.open
		if got := m.hints()[0]; got != [2]string{"enter", c.want} {
			t.Errorf("TMUX=%q, open %+v: first hint %q, want enter %s", c.tmux, c.open, got, c.want)
		}
	}
}

func TestEnterOnASessionThatOpenedSinceTheLastLookAsksFirst(t *testing.T) {
	openedByThisProcess(t, "id-a")
	t.Setenv("TMUX", "")
	m := openPicker(t)
	for i := range m.shown {
		m.shown[i].Open = nil
	}
	m, msg := press(m, tea.KeyEnter)
	if _, quit := msg.(tea.QuitMsg); quit || m.chosen != nil || !strings.Contains(screenOf(m), confirmLine) {
		t.Fatalf("enter on a session opened since the last look quit=%v chose %v and shows\n%s\nwant %q", quit, m.chosen, screenOf(m), confirmLine)
	}
}

func TestEnterKeepsTheKnownPaneWhenTmuxDoesNotAnswerTheFreshLook(t *testing.T) {
	openedByThisProcess(t, "id-a")
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1,0")
	m := openPicker(t)
	m.shown[0].Open = &open.State{PID: os.Getpid(), Status: "idle", Tmux: &open.Pane{ID: "%3", Session: "work", Window: 1}}
	m, msg := press(m, tea.KeyEnter)
	if msg != (tea.QuitMsg{}) || m.chosen == nil || m.chosen.Open == nil || m.chosen.Open.Tmux == nil || m.chosen.Open.Tmux.ID != "%3" {
		t.Fatalf("enter sent %v and chose %+v, want it to quit choosing the session in pane %%3", msg, m.chosen)
	}
}

func TestTheQuestionKeepsItsFirstSentenceWhereTheWholeLineDoesNotFit(t *testing.T) {
	openedByThisProcess(t, "id-a")
	t.Setenv("TMUX", "")
	m, _ := press(openPicker(t), tea.KeyEnter)
	first := "Already open. Enter again resumes a copy."
	cases := []struct {
		width      int
		want, gone string
	}{
		{160, confirmLine, ""},
		{130, confirmLine, ""},
		{125, confirmLine, ""},
		{110, confirmLine, ""},
		{96, confirmLine, ""},
		{50, first, "Esc cancels"},
	}
	for _, c := range cases {
		next, _ := m.Update(tea.WindowSizeMsg{Width: c.width, Height: 36})
		screen := screenOf(next.(picker))
		if !strings.Contains(screen, c.want) || (c.gone != "" && strings.Contains(screen, c.gone)) {
			t.Errorf("at %d columns the screen reads\n%s\nwant %q without %q", c.width, screen, c.want, c.gone)
		}
	}
}

const whatsNew = "New in 0.11.0 · Handoff, last, filters, fish, and a skill for agents"

func TestTheNoticeStaysUnderTheSearchBoxAndGivesWayToASearchMessage(t *testing.T) {
	m := transcriptPicker(t, 2)
	m.db = searchDB(t, map[string]string{m.rows[0].Path: "Ledger migration"})
	m.notice = whatsNew
	line := func(m picker) string { return strings.TrimRight(frame(m, 100, 30)[4], " ") }
	if got := line(m); got != "  "+whatsNew {
		t.Errorf("on open the line under the search box reads %q, want the notice", got)
	}
	m = settle(m, tea.KeyPressMsg{Code: 'l', Text: "l"}, tea.KeyPressMsg{Code: 'e', Text: "e"})
	if got := line(m); got != "  "+whatsNew {
		t.Errorf("after typing a search that matches it reads %q, want the notice", got)
	}
	m = settle(m, tea.KeyPressMsg{Code: 'z', Text: "z"})
	if got := line(m); got != "  No keyword matches. Ctrl-R searches by description." {
		t.Errorf("with no match it reads %q, want the no-match hint", got)
	}
	m = settle(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if got := line(m); got != "  "+whatsNew {
		t.Errorf("after clearing the search it reads %q, want the notice back", got)
	}
}

func TestACompactPickerGivesTheNoticeALineOnlyWhenThereIsOne(t *testing.T) {
	m := transcriptPicker(t, 8)
	if got := strings.TrimRight(frame(m, 80, 20)[2], " "); got != "  Ledger migration  active now                                   tmp        1m" {
		t.Errorf("with no notice line 2 reads %q, want the first session", got)
	}
	m.notice = whatsNew
	lines := frame(m, 80, 20)
	if got := strings.TrimRight(lines[2], " "); got != "  "+whatsNew {
		t.Errorf("with a notice line 2 reads %q, want the notice", got)
	}
	if got := strings.TrimRight(lines[3], " "); got != "  Ledger migration  active now                                   tmp        1m" {
		t.Errorf("with a notice line 3 reads %q, want the first session", got)
	}
}

func TestAPickerOpenedWithWordsShowsTheirKeywordMatchesAndAsksAtOnce(t *testing.T) {
	m := testPicker(t)
	t.Setenv("TYPESAFE_API_KEY", "")
	m.db = searchDB(t, map[string]string{"a": "Ledger migration", "b": "Invoice layout"})
	m.askAbout("invoice")
	if got := pathsOf(m.shown); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("before the ask, the list shows %q, want the keyword match [b]", got)
	}
	next, _ := m.Update(askNow{})
	if m := next.(picker); m.phase() != asking {
		t.Fatalf("a picker opened with words is in phase %d, want asking", m.phase())
	}
	m = settle(m, askNow{})
	got := fmt.Sprint(m.input.Value(), " | ", m.shownMessage(), " | ", pathsOf(m.shown))
	if want := "invoice | Ask failed: Claude Code didn't answer. | [b]"; got != want {
		t.Fatalf("after a failed ask the picker shows %q, want %q", got, want)
	}
	next, _ = m.Update(indexed{rows: m.rows})
	m = next.(picker)
	got = fmt.Sprint(m.shownMessage(), " | ", pathsOf(m.shown))
	if want := "Ask failed: Claude Code didn't answer. | [b]"; got != want {
		t.Fatalf("after the sessions refreshed the picker shows %q, want %q", got, want)
	}
}

func TestAPickerOpenedWithWordsBeforeAnySessionsAsksOnceTheyArrive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newPicker(nil, searchDB(t, map[string]string{"a": "Ledger migration"}), "")
	m.askAbout("ledger")
	m = settle(m, askNow{})
	if m.phase() != searching {
		t.Fatalf("with no sessions yet the picker is in phase %d, want still searching", m.phase())
	}
	rows := []session.Row{{Path: "a", Agent: agent.Agents[0], Session: session.Session{Title: "Ledger migration", Cwd: "/tmp", Mtime: 2, Prompts: 5}}}
	next, _ := m.Update(indexed{rows: rows})
	if m := next.(picker); m.phase() != asking {
		t.Fatalf("once sessions arrived the picker is in phase %d, want asking", m.phase())
	}
}

func TestTypingBeforeTheSessionsArriveCancelsTheAskTheOpeningWordsWouldStart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newPicker(nil, searchDB(t, map[string]string{"a": "Ledger migration"}), "")
	m.askAbout("ledger")
	m = settle(m, askNow{})
	m = settle(m, tea.KeyPressMsg{Code: 's', Text: "s"})
	rows := []session.Row{{Path: "a", Agent: agent.Agents[0], Session: session.Session{Title: "Ledger migration", Cwd: "/tmp", Mtime: 2, Prompts: 5}}}
	next, _ := m.Update(indexed{rows: rows})
	if m := next.(picker); m.phase() != searching || m.input.Value() != "ledgers" {
		t.Fatalf("after typing, the picker is in phase %d with %q, want searching for \"ledgers\"", m.phase(), m.input.Value())
	}
}
