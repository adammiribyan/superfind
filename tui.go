package main

import (
	"database/sql"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	wideColumns  = 100
	headerLines  = 4
	clay         = "#D77757"
	colorTimeout = 300 * time.Millisecond
)

type theme struct {
	known    bool
	plain    bool
	bg, fg   color.Color
	tint     color.Color
	muted    color.Color
	border   color.Color
	accent   color.Color
	mutedSeq string
}

func ansiFor(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

func blend(a, b color.Color, t float64) color.Color {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	f := func(x, y uint32) uint8 { return uint8(float64(x>>8)*(1-t) + float64(y>>8)*t) }
	return color.RGBA{f(ar, br), f(ag, bg), f(ab, bb), 255}
}

func isDark(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return 0.2126*float64(r>>8)+0.7152*float64(g>>8)+0.0722*float64(b>>8) < 128
}

func (t *theme) settle() {
	t.accent = lipgloss.Color("4")
	if t.plain || t.bg == nil || t.fg == nil {
		t.plain = true
		t.muted, t.border = lipgloss.Color("8"), lipgloss.Color("8")
		t.mutedSeq = ansiDim
		return
	}
	blue, share := color.Color(color.RGBA{0x1C, 0x64, 0xC8, 255}), 0.10
	if isDark(t.bg) {
		blue, share = color.RGBA{0x63, 0xA8, 0xF8, 255}, 0.16
	}
	t.tint = blend(t.bg, blue, share)
	t.muted = blend(t.bg, t.fg, 0.70)
	t.border = blend(t.bg, t.fg, 0.35)
	t.mutedSeq = ansiFor(t.muted)
}

type previewKey struct {
	path   string
	width  int
	recent bool
	query  string
}

type previewLoaded struct {
	key  previewKey
	text previewText
}

type askDone struct {
	query string
	paths []string
	err   error
}

type explainDone struct{ query string }
type colorsSettled struct{}
type labelsTick struct{}

type picker struct {
	all        []Row
	shown      []Row
	db         *sql.DB
	cache      map[string]cacheEntry
	known      map[string]statusEntry
	statusSeen time.Time
	accounts   bool

	input    textinput.Model
	spin     spinner.Model
	body     viewport.Model
	header   []string
	previews map[previewKey]previewText
	showing  previewKey

	cursor, offset int
	width, height  int
	colors         theme

	recent      bool
	hidePreview bool
	asking      bool
	askedFor    string
	message     string

	chosen   *Row
	quitting bool
}

func newPicker(rows []Row, db *sql.DB) picker {
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = "Search sessions and conversations"
	in.SetVirtualCursor(false)
	in.Focus()
	m := picker{
		all: recentFirst(rows), db: db, cache: loadCache(), known: statuses(), accounts: showsAccounts(rows),
		input: in, spin: spinner.New(spinner.WithSpinner(spinner.MiniDot)), previews: map[previewKey]previewText{},
	}
	m.shown = m.all
	if info, err := os.Stat(cachePath("status.json")); err == nil {
		m.statusSeen = info.ModTime()
	}
	m.colors.plain = os.Getenv("HOP_PLAIN_COLORS") != ""
	return m
}

func (m picker) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.Tick(colorTimeout, func(time.Time) tea.Msg { return colorsSettled{} }),
		tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return labelsTick{} }),
	}
	if !m.colors.plain {
		cmds = append(cmds, tea.RequestBackgroundColor, tea.RequestForegroundColor)
	}
	return tea.Batch(cmds...)
}

const (
	marginTop  = 1
	marginSide = 1
)

func (m *picker) styleInput() {
	styles := textinput.DefaultStyles(m.colors.bg == nil || isDark(m.colors.bg))
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(m.colors.accent)
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(m.colors.muted)
	m.input.SetStyles(styles)
}

func (m picker) wide() bool { return m.width >= wideColumns }

func (m picker) leftWidth() int {
	if m.wide() && !m.hidePreview {
		return m.width * 45 / 100
	}
	return m.width
}

func (m picker) previewWidth() int {
	if m.wide() {
		return m.width - m.leftWidth() - 3
	}
	return m.width
}

func (m picker) listHeight() int {
	free := m.height - 6
	if !m.wide() && !m.hidePreview {
		return max(free/2, 3)
	}
	return max(free, 1)
}

func (m picker) bodyHeight() int {
	if m.wide() {
		return max(m.height-headerLines-1, 1)
	}
	return max(m.height-6-m.listHeight()-headerLines-2, 1)
}

func (m picker) focused() *Row {
	if m.cursor >= 0 && m.cursor < len(m.shown) {
		return &m.shown[m.cursor]
	}
	return nil
}

func (m *picker) search() {
	m.shown = searchRows(m.db, m.input.Value())
	if m.input.Value() == "" {
		m.shown = m.all
	}
	m.cursor, m.offset = 0, 0
	m.message = ""
	if len(m.shown) == 0 && strings.TrimSpace(m.input.Value()) != "" {
		m.message = "No sessions match “" + m.input.Value() + "”"
	}
}

func (m *picker) keepCursorVisible() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if h := m.listHeight(); m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
}

func (m picker) loadPreview() (picker, tea.Cmd) {
	r := m.focused()
	if r == nil || m.hidePreview {
		m.header, m.showing = nil, previewKey{}
		m.body.SetContent("")
		return m, nil
	}
	key := previewKey{r.Path, m.previewWidth(), m.recent, m.askedFor}
	if key.query == "" {
		key.query = m.input.Value()
	}
	m.showing = key
	if text, ok := m.previews[key]; ok {
		m.showPreview(text)
		return m, nil
	}
	cache, known, db, secondary := m.cache, m.known, m.db, m.colors.mutedSeq
	return m, func() tea.Msg {
		req := previewRequest{key.path, key.width, key.recent, key.query, secondary}
		return previewLoaded{key, buildPreview(req, cache, known, db)}
	}
}

func (m *picker) showPreview(text previewText) {
	m.header = text.header
	m.body.SetWidth(m.previewWidth())
	m.body.SetHeight(m.bodyHeight())
	m.body.SetContentLines(text.body)
	m.body.GotoBottom()
}

func (m picker) startAsk() (picker, tea.Cmd) {
	query := strings.TrimSpace(m.input.Value())
	if query == "" {
		return m, nil
	}
	m.asking, m.message = true, ""
	db, rows := m.db, m.all
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		paths, err := ask(db, query, rows)
		return askDone{query, paths, err}
	})
}

func spawnLabeler() {
	if typesafeKey() == "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "label")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Start()
}

func (m picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width-2*marginSide, msg.Height-marginTop
		m.input.SetWidth(max(m.leftWidth()-12, 10))
		m.keepCursorVisible()
		return m.loadPreview()
	case tea.BackgroundColorMsg:
		m.colors.bg = msg.Color
		if m.colors.fg != nil {
			m.colors.known = true
			m.colors.settle()
			m.styleInput()
			m.previews = map[previewKey]previewText{}
			return m.loadPreview()
		}
	case tea.ForegroundColorMsg:
		m.colors.fg = msg.Color
		if m.colors.bg != nil {
			m.colors.known = true
			m.colors.settle()
			m.styleInput()
			m.previews = map[previewKey]previewText{}
			return m.loadPreview()
		}
	case colorsSettled:
		if !m.colors.known {
			m.colors.settle()
			m.styleInput()
			m.previews = map[previewKey]previewText{}
			return m.loadPreview()
		}
	case labelsTick:
		next := tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return labelsTick{} })
		if info, err := os.Stat(cachePath("status.json")); err == nil && info.ModTime().After(m.statusSeen) {
			m.statusSeen, m.known = info.ModTime(), statuses()
			m.previews = map[previewKey]previewText{}
			m, cmd := m.loadPreview()
			return m, tea.Batch(next, cmd)
		}
		return m, next
	case previewLoaded:
		m.previews[msg.key] = msg.text
		if msg.key == m.showing {
			m.showPreview(msg.text)
		}
	case spinner.TickMsg:
		if m.asking {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
	case askDone:
		m.asking = false
		if msg.query != strings.TrimSpace(m.input.Value()) {
			return m, nil
		}
		if msg.err != nil {
			m.message = msg.err.Error()
			return m, nil
		}
		m.shown, m.cursor, m.offset, m.askedFor = inOrder(m.all, msg.paths), 0, 0, msg.query
		if len(m.shown) == 0 {
			m.message = "Nothing matches “" + msg.query + "” by meaning either"
		}
		next, cmd := m.loadPreview()
		key, db, top := typesafeKey(), m.db, msg.paths[:min(explainTop, len(msg.paths))]
		if key == "" || len(top) == 0 {
			return next, cmd
		}
		query := msg.query
		return next, tea.Batch(cmd, func() tea.Msg {
			explain(db, key, query, top)
			return explainDone{query}
		})
	case explainDone:
		if msg.query == m.askedFor {
			m.previews = map[previewKey]previewText{}
			return m.loadPreview()
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			m.chosen = m.focused()
			m.quitting = true
			return m, tea.Quit
		case "up", "ctrl+p":
			m.cursor = max(m.cursor-1, 0)
		case "down", "ctrl+n":
			m.cursor = min(m.cursor+1, max(len(m.shown)-1, 0))
		case "pgup":
			m.cursor = max(m.cursor-m.listHeight(), 0)
		case "pgdown":
			m.cursor = min(m.cursor+m.listHeight(), max(len(m.shown)-1, 0))
		case "tab":
			m.recent = !m.recent
		case "ctrl+/", "ctrl+_":
			m.hidePreview = !m.hidePreview
			m.input.SetWidth(max(m.leftWidth()-12, 10))
			m.previews = map[previewKey]previewText{}
		case "alt+up":
			m.body.PageUp()
			return m, nil
		case "alt+down":
			m.body.PageDown()
			return m, nil
		case "alt+enter":
			return m.startAsk()
		case "ctrl+u":
			m.input.SetValue("")
			m.askedFor = ""
			m.search()
		default:
			before := m.input.Value()
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			if m.input.Value() != before {
				m.askedFor = ""
				m.search()
			}
			m.keepCursorVisible()
			next, load := m.loadPreview()
			return next, tea.Batch(cmd, load)
		}
		m.keepCursorVisible()
		return m.loadPreview()
	}
	return m, nil
}

func (m picker) rowLine(r Row, selected bool, width int) string {
	s := r.Session
	paint := func(st lipgloss.Style) lipgloss.Style {
		if selected && !m.colors.plain {
			return st.Background(m.colors.tint)
		}
		if selected {
			return st.Reverse(true)
		}
		return st
	}
	plain, muted := paint(lipgloss.NewStyle()), paint(lipgloss.NewStyle().Foreground(m.colors.muted))
	status := statusLabel(r.Path, m.known)
	if s.Active() {
		status = ""
	}
	var parts []string
	segment := ""
	if status == waitingLabel {
		segment = paint(lipgloss.NewStyle().Foreground(m.colors.accent)).Render(status) + muted.Render(" · ")
	} else if status != "" {
		parts = append(parts, status)
	}
	parts = append(parts, when(s), s.Project())
	if !folderExists(s.Cwd) {
		parts = append(parts, "folder removed")
	}
	if m.accounts {
		parts = append(parts, filepath.Base(r.ConfigDir))
	}
	title := strings.Join(strings.Fields(s.Title), " ")
	line := plain.Render(" "+title+"  ") + segment + muted.Render(strings.Join(parts, " · "))
	line = ansi.Truncate(line, width-1, muted.Render("…"))
	if pad := width - lipgloss.Width(line); pad > 0 {
		line += plain.Render(strings.Repeat(" ", pad))
	}
	return line
}

func (m picker) inputBox(width int) []string {
	border := lipgloss.NewStyle().Foreground(m.colors.border)
	muted := lipgloss.NewStyle().Foreground(m.colors.muted)
	mark := lipgloss.NewStyle().Foreground(lipgloss.Color(clay)).Render("✻")
	label := " " + mark + " " + lipgloss.NewStyle().Bold(true).Render("Claude sessions") + " "
	if m.asking {
		label = " " + lipgloss.NewStyle().Foreground(lipgloss.Color(clay)).Render(m.spin.View()) + " " +
			lipgloss.NewStyle().Bold(true).Render("Searching by meaning…") + " "
	}
	top := border.Render("╭─") + label
	top += border.Render(strings.Repeat("─", max(width-lipgloss.Width(top)-1, 0)) + "╮")
	count := muted.Render(fmt.Sprintf("%d/%d", len(m.shown), len(m.all)))
	field := m.input.View()
	inner := width - 4
	gap := inner - lipgloss.Width(field) - lipgloss.Width(count)
	middle := border.Render("│") + " " + ansi.Truncate(field, inner-lipgloss.Width(count)-1, "") +
		strings.Repeat(" ", max(gap, 1)) + count + " " + border.Render("│")
	bottom := border.Render("╰" + strings.Repeat("─", max(width-2, 0)) + "╯")
	return []string{top, middle, bottom}
}

func (m picker) footer() string {
	muted := lipgloss.NewStyle().Foreground(m.colors.muted)
	pairs := [][2]string{{"enter", "resume"}, {"tab", "view"}, {"⌥ enter", "ask"}}
	if len(m.shown) == 0 {
		pairs = [][2]string{{"⌥ enter", "ask"}, {"ctrl-u", "clear search"}}
	}
	var hints []string
	for _, p := range pairs {
		hints = append(hints, p[0]+muted.Render(" "+p[1]))
	}
	return " " + strings.Join(hints, muted.Render(" · "))
}

func (m picker) previewLines(width, height int) []string {
	lines := append([]string{}, m.header...)
	for len(lines) < headerLines {
		lines = append(lines, "")
	}
	lines = append(lines, "")
	lines = append(lines, strings.Split(m.body.View(), "\n")...)
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "")
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines[:height]
}

func (m picker) View() tea.View {
	if m.width == 0 || m.quitting {
		return tea.NewView("")
	}
	left := m.leftWidth()
	muted := lipgloss.NewStyle().Foreground(m.colors.muted)
	column := m.inputBox(left)
	column = append(column, muted.Render(" "+m.message))
	for i := m.offset; i < m.offset+m.listHeight(); i++ {
		if i < len(m.shown) {
			column = append(column, m.rowLine(m.shown[i], i == m.cursor, left))
		} else {
			column = append(column, "")
		}
	}

	var screen []string
	switch {
	case m.hidePreview:
		screen = append(column, "", m.footer())
	case m.wide():
		column = append(column, "", m.footer())
		preview := m.previewLines(m.previewWidth(), m.height)
		divider := lipgloss.NewStyle().Foreground(m.colors.border).Render("│")
		for i := 0; i < m.height; i++ {
			l := ""
			if i < len(column) {
				l = column[i]
			}
			l += strings.Repeat(" ", max(left-lipgloss.Width(l), 0))
			screen = append(screen, l+" "+divider+" "+preview[i])
		}
	default:
		screen = append(column, lipgloss.NewStyle().Foreground(m.colors.border).Render(strings.Repeat("─", m.width)))
		screen = append(screen, m.previewLines(m.width, m.height-len(column)-3)...)
		screen = append(screen, "", m.footer())
	}
	if len(screen) > m.height {
		screen = screen[:m.height]
	}
	side := strings.Repeat(" ", marginSide)
	for i := range screen {
		screen[i] = side + screen[i]
	}
	view := tea.NewView(strings.Repeat("\n", marginTop) + strings.Join(screen, "\n"))
	view.AltScreen = true
	if c := m.input.Cursor(); c != nil && !m.asking {
		c.Position.X += 2 + marginSide
		c.Position.Y += 1 + marginTop
		view.Cursor = c
	}
	return view
}

func pick() (*Row, error) {
	rows := index()
	db, _ := openSearch()
	spawnLabeler()
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer tty.Close()
	final, err := tea.NewProgram(newPicker(rows, db), tea.WithInput(tty), tea.WithOutput(tty)).Run()
	if err != nil {
		return nil, err
	}
	return final.(picker).chosen, nil
}
