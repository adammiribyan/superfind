package render

import (
	"testing"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/open"
	"github.com/adammiribyan/superfind/internal/session"
)

func TestAgeIsCompact(t *testing.T) {
	now := session.Now()
	cases := map[float64]string{
		now - 30:          "1m",
		now + 30:          "1m",
		now - 130:         "2m",
		now - 16*60 - 5:   "16m",
		now - 6*3600 - 5:  "6h",
		now - 2*86400 - 5: "2d",
		float64(time.Date(2025, 11, 2, 12, 0, 0, 0, time.Local).Unix()): "Nov 2025",
	}
	if today := time.Now(); today.YearDay() > 9 {
		cases[float64(time.Date(today.Year(), 1, 3, 12, 0, 0, 0, time.Local).Unix())] = "Jan 3"
	}
	for mtime, want := range cases {
		if got := Age(mtime); got != want {
			t.Errorf("Age(now-%.0fs) = %q, want %q", now-mtime, got, want)
		}
	}
}

func TestCellsNameOneStatusAndLeaveTheProjectBlankHere(t *testing.T) {
	t.Setenv("PWD", "/work/app/web")
	old := session.Now() - 3*86400
	claude := &agent.Agent{ID: "claude"}
	cases := []struct {
		row  session.Row
		want Cells
	}{
		{session.Row{Agent: claude, Status: session.WaitingLabel, FolderGone: true, Session: session.Session{Title: "a", Cwd: "/work/shop", Mtime: session.Now()}},
			Cells{Title: "a", Status: "waiting on you", Project: "shop", Age: "1m", Waiting: true}},
		{session.Row{Agent: claude, FolderGone: true, Session: session.Session{Title: "b", Cwd: "/work/shop", Mtime: session.Now()}},
			Cells{Title: "b", Status: "active now", Project: "shop", Age: "1m"}},
		{session.Row{Agent: claude, FolderGone: true, Session: session.Session{Title: "c", Cwd: "/work/shop", Mtime: old}},
			Cells{Title: "c", Status: "folder removed", Project: "shop", Age: "3d"}},
		{session.Row{Agent: claude, Session: session.Session{Title: "d", Cwd: "/work/app", Mtime: old}},
			Cells{Title: "d", Age: "3d"}},
	}
	for _, c := range cases {
		if got := CellsOf(c.row, Tags{}); got != c.want {
			t.Errorf("CellsOf(%s) = %+v, want %+v", c.row.Session.Title, got, c.want)
		}
	}
}

func TestAnOpenSessionSaysSoUnlessItHasALabel(t *testing.T) {
	claude := &agent.Agent{ID: "claude"}
	held := &open.State{PID: 4242, Status: "busy"}
	cases := []struct {
		row  session.Row
		want Cells
	}{
		{session.Row{Agent: claude, Open: held, Session: session.Session{Title: "a", Cwd: "/work/shop", Mtime: session.Now()}},
			Cells{Title: "a", Status: "open", Project: "shop", Age: "1m"}},
		{session.Row{Agent: claude, Open: held, FolderGone: true, Session: session.Session{Title: "b", Cwd: "/work/shop", Mtime: session.Now() - 3*86400}},
			Cells{Title: "b", Status: "open", Project: "shop", Age: "3d"}},
		{session.Row{Agent: claude, Open: held, Status: "unfinished", Session: session.Session{Title: "c", Cwd: "/work/shop", Mtime: session.Now()}},
			Cells{Title: "c", Status: "unfinished", Project: "shop", Age: "1m"}},
	}
	for _, c := range cases {
		if got := CellsOf(c.row, Tags{}); got != c.want {
			t.Errorf("CellsOf(%s) = %+v, want %+v", c.row.Session.Title, got, c.want)
		}
	}
	row := session.Row{Agent: claude, Open: held, Session: session.Session{Title: "a", Cwd: "/work/shop", Mtime: session.Now()}}
	if got := RowContext(RowTextOf(row, Tags{}), ""); got != "open · shop" {
		t.Errorf("row context %q", got)
	}
	row.Open = nil
	if got := RowContext(RowTextOf(row, Tags{}), ""); got != "active now · shop" {
		t.Errorf("row context without a process %q", got)
	}
}

func TestNarrowListsDropAgentThenProject(t *testing.T) {
	w := Widths{Project: 14, Agent: 6}
	cases := map[int]Columns{
		60: {Project: 14, Agent: 6},
		59: {Project: 14},
		52: {Project: 14},
		51: {},
	}
	for width, want := range cases {
		if got := w.Fit(width); got != want {
			t.Errorf("Fit(%d) = %+v, want %+v", width, got, want)
		}
	}
}

func TestALabeledRowGivesUpItsAgentThenItsProjectBeforeItsTitleGetsUnderSixteenCells(t *testing.T) {
	fit := Columns{Project: 14, Agent: 6}
	labeled := Cells{Title: "Ledger migration", Status: "waiting on you", Project: "payments-reco…", Agent: "claude"}
	cases := []struct {
		cells Cells
		width int
		want  Columns
	}{
		{labeled, 68, Columns{Project: 14, Agent: 6}},
		{labeled, 67, Columns{Project: 14}},
		{labeled, 60, Columns{Project: 14}},
		{labeled, 59, Columns{}},
		{Cells{Title: "Ledger migration", Project: "payments-reco…", Agent: "claude"}, 59, Columns{Project: 14, Agent: 6}},
		{Cells{Title: "Ledger migration", Status: "waiting on you", Agent: "claude"}, 80, Columns{Agent: 6}},
	}
	for _, c := range cases {
		if got := fit.For(c.cells, c.width); got != c.want {
			t.Errorf("at width %d with status %q the row keeps %+v, want %+v", c.width, c.cells.Status, got, c.want)
		}
	}
}
