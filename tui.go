package main

import (
	"database/sql"
	"fmt"
	"image/color"
	"os"
	"os/exec"
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
	wideColumns    = 100
	headerLines    = 4
	clay           = "#D77757"
	colorTimeout   = 300 * time.Millisecond
	labelsInterval = 1500 * time.Millisecond
	marginTop      = 1
	marginSide     = 1
	listChrome     = 6
	stackedMinimum = headerLines + 4
)

type theme struct {
	settled     bool
	forcedPlain bool
	bg, fg      color.Color
	tint        color.Color
	muted       color.Color
	border      color.Color
	accent      color.Color
	mutedSeq    string
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

func (t *theme) plain() bool { return t.forcedPlain || t.bg == nil || t.fg == nil }

func (t *theme) settle() {
	t.settled = true
	t.accent = lipgloss.Color("4")
	if t.plain() {
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

type layout struct {
	listWidth     int
	listHeight    int
	previewWidth  int
	previewHeight int
	stacked       bool
}

func (l layout) hasPreview() bool { return l.previewWidth > 0 && l.previewHeight > 0 }

func (l layout) bodyHeight() int { return max(l.previewHeight-headerLines-1, 0) }

type bodyKey struct {
	path       string
	width      int
	recent     bool
	generation int
	file       fileID
}

type bodyLoaded struct {
	key      bodyKey
	body     []string
	readable bool
}

type askDone struct {
	seq   int
	query string
	paths []string
	err   error
}

type explainDone struct {
	seq      int
	passages map[string]string
}

type colorsSettled struct{}
type labelsTick struct{}

type picker struct {
	all        []Row
	shown      []Row
	db         *sql.DB
	problem    string
	cache      map[string]cacheEntry
	known      map[string]statusEntry
	statusSeen time.Time
	accounts   bool

	input      textinput.Model
	spin       spinner.Model
	body       viewport.Model
	header     []string
	bodies     map[bodyKey][]string
	showing    bodyKey
	displayed  bodyKey
	pinned     bool
	generation int

	cursor, offset int
	width, height  int
	colors         theme

	recent      bool
	hidePreview bool
	askSeq      int
	asking      bool
	askedFor    string
	explained   map[string]string
	message     string

	chosen   *Row
	quitting bool
}

func newPicker(rows []Row, db *sql.DB, problem string) picker {
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = "Search sessions and conversations"
	in.SetVirtualCursor(false)
	in.Focus()
	m := picker{
		all: recentFirst(annotate(rows)), db: db, cache: loadCache(), known: statuses(), accounts: showsAccounts(rows),
		input: in, spin: spinner.New(spinner.WithSpinner(spinner.MiniDot)), bodies: map[bodyKey][]string{},
	}
	m.problem, m.message = problem, problem
	m.shown = m.all
	if info, err := os.Stat(cachePath("status.json")); err == nil {
		m.statusSeen = info.ModTime()
	}
	m.colors.forcedPlain = os.Getenv("HOP_PLAIN_COLORS") != ""
	return m
}

func (m picker) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.Tick(colorTimeout, func(time.Time) tea.Msg { return colorsSettled{} }),
		tea.Tick(labelsInterval, func(time.Time) tea.Msg { return labelsTick{} }),
	}
	if !m.colors.forcedPlain {
		cmds = append(cmds, tea.RequestBackgroundColor, tea.RequestForegroundColor)
	}
	return tea.Batch(cmds...)
}

func (m picker) layout() layout {
	l := layout{listWidth: m.width}
	free := max(m.height-listChrome, 0)
	switch {
	case m.hidePreview:
		l.listHeight = free
	case m.width >= wideColumns:
		l.listWidth = m.width * 45 / 100
		l.previewWidth = m.width - l.listWidth - 3
		l.previewHeight = m.height
		l.listHeight = free
	case free-1 >= stackedMinimum+3:
		l.stacked = true
		l.previewWidth = m.width
		l.listHeight = max((free-1)/2, 3)
		l.previewHeight = free - 1 - l.listHeight
	default:
		l.listHeight = free
	}
	return l
}

func (m *picker) styleInput() {
	styles := textinput.DefaultStyles(m.colors.bg == nil || isDark(m.colors.bg))
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(m.colors.accent)
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(m.colors.muted)
	m.input.SetStyles(styles)
}

func (m *picker) settleColors() {
	m.colors.settle()
	m.styleInput()
	m.generation++
}

func (m picker) focused() *Row {
	if m.cursor >= 0 && m.cursor < len(m.shown) {
		return &m.shown[m.cursor]
	}
	return nil
}

func (m *picker) forgetAsk() {
	m.askSeq++
	m.asking, m.askedFor, m.explained = false, "", nil
}

func (m *picker) search() {
	m.forgetAsk()
	query := m.input.Value()
	m.cursor, m.offset, m.message = 0, 0, m.problem
	switch {
	case strings.TrimSpace(query) == "":
		m.shown = m.all
	case m.db == nil:
		m.shown = m.all
	default:
		shown, err := searchRows(m.db, query, m.all)
		m.shown = shown
		switch {
		case err != nil:
			m.message = "Search failed: " + err.Error()
		case len(m.shown) == 0:
			m.message = "No sessions match “" + query + "”"
		}
	}
}

func (m *picker) keepCursorVisible() {
	m.cursor = min(max(m.cursor, 0), max(len(m.shown)-1, 0))
	h := max(m.layout().listHeight, 1)
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
}

func (m picker) loadPreview() (picker, tea.Cmd) {
	l := m.layout()
	r := m.focused()
	if r == nil || !l.hasPreview() {
		m.header, m.showing, m.displayed = nil, bodyKey{}, bodyKey{}
		m.body.SetContentLines(nil)
		return m, nil
	}
	query := m.askedFor
	if query == "" {
		query = m.input.Value()
	}
	req := previewRequest{path: r.Path, width: l.previewWidth, recent: m.recent, query: query,
		explained: m.explained[r.Path], secondary: m.colors.mutedSeq}
	m.header = previewHeader(req, m.cache, m.known, m.db)
	key := bodyKey{path: r.Path, width: l.previewWidth, recent: m.recent, generation: m.generation}
	if info, err := os.Stat(r.Path); err == nil {
		key.file = identify(info)
	}
	m.showing = key
	m.body.SetWidth(l.previewWidth)
	m.body.SetHeight(l.bodyHeight())
	if body, ok := m.bodies[key]; ok {
		m.showBody(key, body)
		return m, nil
	}
	if !m.displayed.sameView(key) {
		m.body.SetContentLines(nil)
	}
	return m, func() tea.Msg {
		body, err := previewBody(req)
		return bodyLoaded{key, body, err == nil}
	}
}

func (k bodyKey) sameView(other bodyKey) bool {
	return k.path == other.path && k.recent == other.recent
}

func (m *picker) showBody(key bodyKey, body []string) {
	keepPlace := m.displayed.sameView(key) && !m.pinned
	offset := m.body.YOffset()
	m.body.SetContentLines(body)
	m.displayed = key
	if keepPlace {
		m.body.SetYOffset(offset)
	} else {
		m.body.GotoBottom()
		m.pinned = true
	}
}

func (m picker) startAsk() (picker, tea.Cmd) {
	query := strings.TrimSpace(m.input.Value())
	if query == "" {
		return m, nil
	}
	if m.db == nil {
		m.message = m.problem
		return m, nil
	}
	m.askSeq++
	m.asking, m.message = true, ""
	seq, db, rows := m.askSeq, m.db, m.all
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		paths, err := ask(db, query, rows)
		return askDone{seq, query, paths, err}
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

func (m picker) afterMove() (tea.Model, tea.Cmd) {
	m.keepCursorVisible()
	return m.loadPreview()
}

func (m picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(msg.Width-2*marginSide, 1), max(msg.Height-marginTop, 1)
		m.input.SetWidth(max(m.layout().listWidth-14, 4))
		return m.afterMove()
	case tea.BackgroundColorMsg:
		m.colors.bg = msg.Color
		if m.colors.fg != nil {
			m.settleColors()
			return m.loadPreview()
		}
	case tea.ForegroundColorMsg:
		m.colors.fg = msg.Color
		if m.colors.bg != nil {
			m.settleColors()
			return m.loadPreview()
		}
	case colorsSettled:
		if !m.colors.settled {
			m.settleColors()
			return m.loadPreview()
		}
	case labelsTick:
		next := tea.Tick(labelsInterval, func(time.Time) tea.Msg { return labelsTick{} })
		info, err := os.Stat(cachePath("status.json"))
		if err != nil || !info.ModTime().After(m.statusSeen) {
			return m, next
		}
		m.statusSeen, m.known = info.ModTime(), statuses()
		m.all = annotate(m.all)
		m.shown = inOrder(m.all, pathsOf(m.shown))
		m.generation++
		updated, load := m.loadPreview()
		return updated, tea.Batch(next, load)
	case bodyLoaded:
		if msg.key.generation == m.generation && msg.readable {
			m.bodies[msg.key] = msg.body
		}
		if msg.key == m.showing {
			m.showBody(msg.key, msg.body)
		}
	case spinner.TickMsg:
		if m.asking {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
	case askDone:
		if msg.seq != m.askSeq {
			return m, nil
		}
		m.asking = false
		if msg.err != nil {
			m.message = msg.err.Error()
			return m, nil
		}
		m.shown, m.cursor, m.offset, m.askedFor = inOrder(m.all, msg.paths), 0, 0, msg.query
		if len(m.shown) == 0 {
			m.message = "Deep search found nothing for “" + msg.query + "”"
		}
		updated, load := m.loadPreview()
		key, top := typesafeKey(), msg.paths[:min(explainTop, len(msg.paths))]
		if key == "" || len(top) == 0 {
			return updated, load
		}
		seq, db, query := msg.seq, m.db, msg.query
		return updated, tea.Batch(load, func() tea.Msg {
			return explainDone{seq, explain(db, key, query, top)}
		})
	case explainDone:
		if msg.seq == m.askSeq {
			m.explained = msg.passages
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
			m.cursor--
			return m.afterMove()
		case "down", "ctrl+n":
			m.cursor++
			return m.afterMove()
		case "pgup":
			m.cursor -= max(m.layout().listHeight, 1)
			return m.afterMove()
		case "pgdown":
			m.cursor += max(m.layout().listHeight, 1)
			return m.afterMove()
		case "tab":
			m.recent = !m.recent
			return m.loadPreview()
		case "ctrl+/", "ctrl+_":
			m.hidePreview = !m.hidePreview
			m.input.SetWidth(max(m.layout().listWidth-14, 4))
			return m.afterMove()
		case "alt+up":
			m.body.PageUp()
			m.pinned = m.body.AtBottom()
			return m, nil
		case "alt+down":
			m.body.PageDown()
			m.pinned = m.body.AtBottom()
			return m, nil
		case "ctrl+r", "alt+enter":
			return m.startAsk()
		case "ctrl+u":
			m.input.SetValue("")
			m.search()
			return m.afterMove()
		}
	}
	return m.editInput(msg)
}

func (m picker) editInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() == before {
		return m, cmd
	}
	m.search()
	updated, load := m.afterMove()
	return updated, tea.Batch(cmd, load)
}

func (m picker) rowLine(r Row, selected bool, width int) string {
	paint := func(st lipgloss.Style) lipgloss.Style {
		if selected && !m.colors.plain() {
			return st.Background(m.colors.tint)
		}
		if selected {
			return st.Reverse(true)
		}
		return st
	}
	plain, muted := paint(lipgloss.NewStyle()), paint(lipgloss.NewStyle().Foreground(m.colors.muted))
	t := rowTextOf(r, m.accounts)
	segment := ""
	if t.waiting != "" {
		segment = paint(lipgloss.NewStyle().Foreground(m.colors.accent)).Render(t.waiting) + muted.Render(" · ")
	}
	line := plain.Render(" "+t.title+"  ") + segment + muted.Render(strings.Join(t.context, " · "))
	line = ansi.Truncate(line, max(width-1, 0), muted.Render("…"))
	if pad := width - lipgloss.Width(line); pad > 0 {
		line += plain.Render(strings.Repeat(" ", pad))
	}
	return line
}

func (m picker) inputBox(width int) []string {
	border := lipgloss.NewStyle().Foreground(m.colors.border)
	muted := lipgloss.NewStyle().Foreground(m.colors.muted)
	label := " " + lipgloss.NewStyle().Foreground(lipgloss.Color(clay)).Render("✻") + " " +
		lipgloss.NewStyle().Bold(true).Render("Claude sessions") + " "
	if m.asking {
		label = " " + lipgloss.NewStyle().Foreground(lipgloss.Color(clay)).Render(m.spin.View()) + " " +
			lipgloss.NewStyle().Bold(true).Render("Searching deeper…") + " "
	}
	top := ansi.Truncate(border.Render("╭─")+label, max(width-1, 0), "")
	top += border.Render(strings.Repeat("─", max(width-lipgloss.Width(top)-1, 0)) + "╮")
	count := muted.Render(fmt.Sprintf("%d/%d", len(m.shown), len(m.all)))
	inner := max(width-4, 0)
	field := ansi.Truncate(m.input.View(), max(inner-lipgloss.Width(count)-1, 0), "")
	gap := max(inner-lipgloss.Width(field)-lipgloss.Width(count), 1)
	middle := border.Render("│") + " " + field + strings.Repeat(" ", gap) + count + " " + border.Render("│")
	bottom := border.Render("╰" + strings.Repeat("─", max(width-2, 0)) + "╯")
	return []string{top, middle, bottom}
}

func (m picker) footer(width int) string {
	muted := lipgloss.NewStyle().Foreground(m.colors.muted)
	pairs := [][2]string{{"enter", "resume"}, {"tab", "view"}, {"ctrl-r", "deep search"}}
	if len(m.shown) == 0 {
		pairs = [][2]string{{"ctrl-r", "deep search"}, {"ctrl-u", "clear search"}}
	}
	line, used := "", 1
	for i, p := range pairs {
		hint, separator := p[0]+muted.Render(" "+p[1]), ""
		if i > 0 {
			separator = muted.Render(" · ")
		}
		used += ansi.StringWidth(separator + hint)
		if used > width {
			break
		}
		line += separator + hint
	}
	return " " + line
}

func (m picker) previewLines(l layout) []string {
	if !l.hasPreview() {
		return nil
	}
	lines := append([]string{}, m.header...)
	for len(lines) < headerLines {
		lines = append(lines, "")
	}
	lines = append(lines, "")
	if l.bodyHeight() > 0 {
		lines = append(lines, strings.Split(m.body.View(), "\n")...)
	}
	for len(lines) < l.previewHeight {
		lines = append(lines, "")
	}
	lines = lines[:l.previewHeight]
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], l.previewWidth, "")
	}
	return lines
}

func pad(line string, width int) string {
	return line + strings.Repeat(" ", max(width-lipgloss.Width(line), 0))
}

func (m picker) View() tea.View {
	if m.width == 0 || m.quitting {
		return tea.NewView("")
	}
	l := m.layout()
	muted := lipgloss.NewStyle().Foreground(m.colors.muted)
	column := m.inputBox(l.listWidth)
	column = append(column, ansi.Truncate(muted.Render(" "+m.message), l.listWidth, "…"))
	for i := m.offset; i < m.offset+l.listHeight; i++ {
		if i < len(m.shown) {
			column = append(column, m.rowLine(m.shown[i], i == m.cursor, l.listWidth))
		} else {
			column = append(column, "")
		}
	}
	preview := m.previewLines(l)
	var screen []string
	switch {
	case l.hasPreview() && !l.stacked:
		column = append(column, "", m.footer(l.listWidth))
		divider := lipgloss.NewStyle().Foreground(m.colors.border).Render("│")
		for i := range preview {
			left := ""
			if i < len(column) {
				left = column[i]
			}
			screen = append(screen, pad(left, l.listWidth)+" "+divider+" "+preview[i])
		}
	case l.stacked:
		screen = append(column, lipgloss.NewStyle().Foreground(m.colors.border).Render(strings.Repeat("─", m.width)))
		screen = append(append(screen, preview...), "", m.footer(m.width))
	default:
		screen = append(column, "", m.footer(m.width))
	}
	screen = screen[:min(len(screen), m.height)]
	side := strings.Repeat(" ", marginSide)
	for i := range screen {
		screen[i] = side + screen[i]
	}
	view := tea.NewView(strings.Repeat("\n", marginTop) + strings.Join(screen, "\n"))
	view.AltScreen = true
	if c := m.input.Cursor(); c != nil && !m.asking && m.height > 1 {
		c.Position.X += 2 + marginSide
		c.Position.Y += 1 + marginTop
		view.Cursor = c
	}
	return view
}

func pathsOf(rows []Row) []string {
	paths := make([]string, len(rows))
	for i, r := range rows {
		paths[i] = r.Path
	}
	return paths
}

func pick() (*Row, error) {
	problem := ""
	rows, err := index()
	if err != nil {
		problem = "Sessions may be out of date: " + err.Error()
	}
	db, err := openSearch()
	if err != nil {
		problem = "Search is unavailable: " + err.Error()
	}
	spawnLabeler()
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("hop pick needs a terminal: %w", err)
	}
	defer tty.Close()
	final, err := tea.NewProgram(newPicker(rows, db, problem), tea.WithInput(tty), tea.WithOutput(tty)).Run()
	if err != nil {
		return nil, err
	}
	return final.(picker).chosen, nil
}
