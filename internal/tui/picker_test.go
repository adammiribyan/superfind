package tui

import (
	"image/color"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
)

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
