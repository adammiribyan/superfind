package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
)

const (
	gaugeCell = "■"
	urgent    = "!"
)

var (
	amber = [2]color.Color{color.RGBA{0xB4, 0x6A, 0x00, 255}, color.RGBA{0xF0, 0xB2, 0x4A, 255}}
	red   = [2]color.Color{color.RGBA{0xC8, 0x32, 0x32, 255}, color.RGBA{0xF2, 0x6D, 0x6D, 255}}
)

func (m picker) gaugeStyles(g render.Gauge) (fill, track lipgloss.Style) {
	t := m.colors
	if t.plain() {
		fill = lipgloss.NewStyle()
		if g.Low {
			fill = fill.Bold(true)
		}
		return fill, t.muted
	}
	dark := 0
	if isDark(t.bg) {
		dark = 1
	}
	fillColor := blend(t.bg, t.fg, 0.55)
	switch {
	case g.NearlyFull:
		fillColor = red[dark]
	case g.Low:
		fillColor = amber[dark]
	}
	return foreground(fillColor), foreground(blend(t.bg, t.fg, 0.2))
}

func (m picker) gauge(r session.Row, width int, paint func(lipgloss.Style) lipgloss.Style) string {
	if width == 0 {
		return ""
	}
	blank := paint(lipgloss.NewStyle())
	g, known := render.GaugeOf(r.Session)
	if !known {
		return blank.Render(strings.Repeat(" ", width))
	}
	fill, track := m.gaugeStyles(g)
	mark := " "
	if g.NearlyFull {
		mark = urgent
	}
	return paint(fill).Render(mark+strings.Repeat(gaugeCell, g.Filled)) +
		paint(track).Render(strings.Repeat(gaugeCell, render.GaugeCells-g.Filled)) +
		blank.Render(strings.Repeat(" ", width-1-render.GaugeCells))
}
