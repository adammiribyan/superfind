package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/ask"
	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
	"github.com/adammiribyan/superfind/internal/typesafe"
)

const (
	wideColumns    = 100
	headerLines    = 4
	colorTimeout   = 300 * time.Millisecond
	labelsInterval = 1500 * time.Millisecond
	marginTop      = 1
	marginSide     = 1
	listChrome     = 6
	minStatusTitle = 8
	stackedMinimum = headerLines + 4
	selectionShare = 0.24
)

type theme struct {
	settled               bool
	noColor               bool
	bg, fg                color.Color
	tint                  color.Color
	muted, border, accent lipgloss.Style
	mutedSeq              string
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

func (t *theme) plain() bool { return t.noColor || t.bg == nil || t.fg == nil }

func foreground(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

func (t *theme) settle() {
	t.settled = true
	t.mutedSeq = render.Dim
	if t.noColor {
		t.muted, t.border, t.accent = lipgloss.NewStyle().Faint(true), lipgloss.NewStyle().Faint(true), lipgloss.NewStyle().Bold(true)
		return
	}
	t.accent = foreground(lipgloss.Color("4"))
	if t.plain() {
		t.muted, t.border = foreground(lipgloss.Color("8")), foreground(lipgloss.Color("8"))
		return
	}
	blue := color.Color(color.RGBA{0x1C, 0x64, 0xC8, 255})
	if isDark(t.bg) {
		blue = color.RGBA{0x63, 0xA8, 0xF8, 255}
	}
	t.tint = blend(t.bg, blue, selectionShare)
	muted := blend(t.bg, t.fg, 0.70)
	t.muted, t.border = foreground(muted), foreground(blend(t.bg, t.fg, 0.35))
	t.mutedSeq = ansiFor(muted)
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

const secondaryMark = "\x00"

type bodyKey struct {
	path   string
	width  int
	recent bool
	file   agent.FileID
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

type indexed struct {
	rows     []session.Row
	problems []error
	err      error
}

type labelsTick struct{}

type picker struct {
	all        []session.Row
	shown      []session.Row
	db         *sql.DB
	problem    string
	dbProblem  string
	statusSeen time.Time
	tags       render.Tags
	widths     render.Widths
	label      string

	input     textinput.Model
	spin      spinner.Model
	body      viewport.Model
	header    []string
	bodies    map[bodyKey][]string
	loading   map[bodyKey]bool
	showing   bodyKey
	displayed bodyKey
	started   bool
	pinned    bool

	cursor, offset int
	width, height  int
	screenHeight   int
	colors         theme

	recent      bool
	hidePreview bool
	askSeq      int
	asking      bool
	askedFor    string
	explained   map[string]string
	message     string

	asks     *asks
	chosen   *session.Row
	quitting bool
	reindex  bool
}

type phase int

const (
	searching phase = iota
	asking
	asked
)

func (m picker) phase() phase {
	switch {
	case m.asking:
		return asking
	case m.askedFor != "":
		return asked
	}
	return searching
}

func (m picker) matchWords() []string {
	if m.phase() != searching {
		return nil
	}
	words, _ := session.Narrow(m.input.Value(), nil)
	return strings.FieldsFunc(words, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
}

func newPicker(rows []session.Row, db *sql.DB, problem string) picker {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "Search sessions and conversations"
	in.SetVirtualCursor(false)
	in.Focus()
	m := picker{
		all: session.RecentFirst(session.Annotate(rows)), db: db, tags: render.TagsFor(rows), label: render.PickerLabel(rows, agent.Places()),
		input: in, spin: spinner.New(spinner.WithSpinner(spinner.MiniDot)), bodies: map[bodyKey][]string{}, loading: map[bodyKey]bool{},
		asks: &asks{},
	}
	m.widths = render.Measure(m.all, m.tags)
	m.problem, m.dbProblem = problem, problem
	if len(rows) == 0 && problem == "" {
		m.problem = "Indexing your sessions…"
	}
	m.message = m.problem
	m.shown = m.all
	if info, err := os.Stat(session.CachePath("status.json")); err == nil {
		m.statusSeen = info.ModTime()
	}
	m.colors.noColor = render.NoColor
	if m.colors.noColor {
		m.settleColors()
	}
	return m
}

func index() tea.Msg {
	rows, problems, err := session.Index(nil)
	return indexed{rows, problems, err}
}

func rebuild() tea.Msg {
	rows, problems, err := session.Rebuild(nil)
	return indexed{rows, problems, err}
}

func (m picker) Init() tea.Cmd {
	cmds := []tea.Cmd{
		index,
		tea.Tick(colorTimeout, func(time.Time) tea.Msg { return colorsSettled{} }),
		tea.Tick(labelsInterval, func(time.Time) tea.Msg { return labelsTick{} }),
	}
	if !m.colors.noColor {
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
		l.listWidth = m.width * 54 / 100
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
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Focused.Placeholder = m.colors.muted
	styles.Cursor.Color = nil
	m.input.SetStyles(styles)
}

func (m *picker) settleColors() {
	m.colors.settle()
	m.styleInput()
}

func (m picker) focused() *session.Row {
	if m.cursor >= 0 && m.cursor < len(m.shown) {
		return &m.shown[m.cursor]
	}
	return nil
}

type asks struct {
	ctx     context.Context
	cancel  context.CancelFunc
	running sync.WaitGroup
}

func (a *asks) stop() {
	if a.cancel != nil {
		a.cancel()
	}
}

func (m *picker) forgetAsk() {
	m.asks.stop()
	m.askSeq++
	m.asking, m.askedFor, m.explained = false, "", nil
}

func (m *picker) search() {
	m.forgetAsk()
	m.cursor, m.offset = 0, 0
	m.filter()
}

func (m *picker) filter() {
	query := m.input.Value()
	m.message = m.problem
	switch {
	case strings.TrimSpace(query) == "":
		m.shown = m.all
	case m.db == nil:
		m.shown = m.all
	default:
		shown, err := session.Search(m.db, query, m.all)
		m.shown = shown
		words, _ := session.Narrow(query, nil)
		if errors.Is(err, session.ErrDamaged) {
			m.db, m.shown, m.reindex = nil, m.all, true
		}
		switch {
		case err != nil:
			m.message = "Search failed: " + err.Error()
		case len(m.shown) == 0 && len(m.all) > 0 && words != "":
			m.message = "No session contains these words. ctrl-r asks by description."
		case len(m.shown) == 0 && len(m.all) > 0:
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
	req := render.PreviewRequest{Agent: r.Agent, Path: r.Path, Width: l.previewWidth, Recent: m.recent, Query: query,
		Explained: m.explained[r.Path], Secondary: m.colors.mutedSeq}
	m.header = render.PreviewHeader(req, *r, m.tags, m.db)
	key := bodyKey{path: r.Path, width: l.previewWidth, recent: m.recent}
	if id, _, err := r.Agent.Stat(r.Path); err == nil {
		key.file = id
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
	if m.loading[key] {
		return m, nil
	}
	m.loading[key] = true
	req.Secondary = secondaryMark
	return m, func() tea.Msg {
		body, err := render.PreviewBody(req)
		return bodyLoaded{key, body, err == nil}
	}
}

func (k bodyKey) sameView(other bodyKey) bool {
	return k.path == other.path && k.recent == other.recent
}

func (m *picker) showBody(key bodyKey, body []string) {
	keepPlace := m.displayed.sameView(key) && !m.pinned
	offset := m.body.YOffset()
	colored := make([]string, len(body))
	for i, line := range body {
		colored[i] = strings.ReplaceAll(line, secondaryMark, m.colors.mutedSeq)
	}
	m.body.SetContentLines(colored)
	m.displayed = key
	if keepPlace {
		m.body.SetYOffset(offset)
	} else {
		m.body.GotoBottom()
		m.pinned = true
	}
}

func (m picker) startAsk() (picker, tea.Cmd) {
	query, rows := session.Narrow(m.input.Value(), m.all)
	if query == "" || len(m.all) == 0 {
		return m, nil
	}
	if m.db == nil {
		m.message = m.problem
		return m, nil
	}
	m.asks.stop()
	m.asks.ctx, m.asks.cancel = context.WithCancel(context.Background())
	m.askSeq++
	m.asking, m.message = true, ""
	seq, db, ctx, running := m.askSeq, m.db, m.asks.ctx, &m.asks.running
	running.Add(1)
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		defer running.Done()
		paths, err := ask.Ask(ctx, db, query, rows)
		return askDone{seq, query, paths, err}
	})
}

func spawnLabeler() {
	if typesafe.Key() == "" {
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
	model, cmd := m.update(msg)
	next := model.(picker)
	_, patienceOver := msg.(colorsSettled)
	if !next.started && next.width > 0 && (patienceOver || next.colors.settled && next.displayed == next.showing) {
		next.started = true
	}
	return next, cmd
}

func (m picker) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.screenHeight = msg.Height
		m.width, m.height = max(msg.Width-2*marginSide, 1), max(msg.Height-marginTop, 1)
		m.input.SetWidth(max(m.layout().listWidth-12, 4))
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
	case indexed:
		focused := ""
		if r := m.focused(); r != nil {
			focused = r.Path
		}
		m.all, m.tags, m.label = session.RecentFirst(session.Annotate(msg.rows)), render.TagsFor(msg.rows), render.PickerLabel(msg.rows, agent.Places())
		m.widths = render.Measure(m.all, m.tags)
		if msg.err == nil {
			if db, err := session.OpenSearch(); err == nil {
				if m.db != nil && !m.asking {
					m.db.Close()
				}
				m.db, m.dbProblem = db, ""
			}
		}
		m.problem = m.dbProblem
		if msg.err != nil {
			m.problem = "Sessions may be out of date: " + msg.err.Error()
		} else if unreadable := session.Unreadable(msg.problems); unreadable != "" {
			m.problem = unreadable
		} else if len(m.all) == 0 && m.problem == "" {
			m.problem = render.NoSessions()
		}
		if m.askedFor != "" {
			m.shown = session.InOrder(m.all, pathsOf(m.shown))
			m.message = m.problem
		} else {
			m.filter()
		}
		if i := slices.Index(pathsOf(m.shown), focused); i >= 0 {
			m.cursor = i
		}
		spawnLabeler()
		return m.afterMove()
	case labelsTick:
		next := tea.Tick(labelsInterval, func(time.Time) tea.Msg { return labelsTick{} })
		info, err := os.Stat(session.CachePath("status.json"))
		if err != nil || !info.ModTime().After(m.statusSeen) {
			return m, next
		}
		m.statusSeen = info.ModTime()
		focused := ""
		if r := m.focused(); r != nil {
			focused = r.Path
		}
		m.all = session.Annotate(m.all)
		m.widths = render.Measure(m.all, m.tags)
		if m.askedFor == "" {
			m.filter()
		} else {
			_, m.shown = session.Narrow(m.input.Value(), session.InOrder(m.all, pathsOf(m.shown)))
		}
		if i := slices.Index(pathsOf(m.shown), focused); i >= 0 {
			m.cursor = i
		}
		updated, load := m.afterMove()
		return updated, tea.Batch(next, load)
	case bodyLoaded:
		delete(m.loading, msg.key)
		if msg.readable {
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
		m.shown, m.cursor, m.offset, m.askedFor = session.InOrder(m.all, msg.paths), 0, 0, msg.query
		if len(m.shown) == 0 {
			m.message = "Ask found nothing for “" + msg.query + "”"
		}
		updated, load := m.loadPreview()
		key, top := typesafe.Key(), msg.paths[:min(ask.ExplainTop, len(msg.paths))]
		if key == "" || len(top) == 0 || updated.db == nil {
			return updated, load
		}
		seq, db, query, ctx := msg.seq, updated.db, msg.query, m.asks.ctx
		return updated, tea.Batch(load, func() tea.Msg {
			return explainDone{seq, ask.Explain(ctx, db, key, query, top)}
		})
	case explainDone:
		if msg.seq == m.askSeq {
			m.explained = msg.passages
			return m.loadPreview()
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			if m.phase() != searching {
				m.search()
				return m.afterMove()
			}
			m.quitting = true
			return m, tea.Quit
		case "ctrl+c":
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
			m.input.SetWidth(max(m.layout().listWidth-12, 4))
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
	if m.reindex {
		m.reindex = false
		cmd = tea.Batch(cmd, rebuild)
	}
	updated, load := m.afterMove()
	return updated, tea.Batch(cmd, load)
}

func cell(text string, width int) string { return pad(ansi.Truncate(text, width, "…"), width) + "  " }

func (m picker) rowLine(r session.Row, selected bool, width int, c render.Columns, words []string) string {
	paint := func(st lipgloss.Style) lipgloss.Style {
		if selected && !m.colors.plain() {
			return st.Background(m.colors.tint)
		}
		if selected {
			return st.Reverse(true)
		}
		return st
	}
	plain, muted, title := paint(lipgloss.NewStyle()), paint(m.colors.muted), paint(lipgloss.NewStyle())
	if selected {
		muted, title = plain, title.Bold(true)
	}
	hit := title.Bold(true)
	if !m.colors.plain() {
		hit = hit.Foreground(lipgloss.Color("4"))
	}
	cells := render.CellsOf(r, m.tags)
	c = c.For(cells, width)
	right := ""
	if c.Project > 0 {
		right += muted.Render(cell(cells.Project, c.Project))
	}
	if c.Agent > 0 {
		right += muted.Render(cell(cells.Agent, c.Agent))
	}
	right += muted.Render(fmt.Sprintf("%*s ", render.AgeWidth, cells.Age))
	room := max(width-3-lipgloss.Width(right), 1)
	marked := ""
	for _, span := range render.Marks(cells.Title, words) {
		if span.Hit {
			marked += hit.Render(span.Text)
		} else {
			marked += title.Render(span.Text)
		}
	}
	status := ""
	if word := 2 + lipgloss.Width(cells.Status); cells.Status != "" && room-word >= minStatusTitle {
		room -= word
		paintStatus := muted
		if cells.Waiting {
			paintStatus = plain
		}
		status = plain.Render("  ") + paintStatus.Render(cells.Status)
	}
	left := plain.Render(" ") + ansi.Truncate(marked, room, title.Render("…")) + status
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 2)
	return ansi.Truncate(left+plain.Render(strings.Repeat(" ", gap))+right, width, "")
}

func (m picker) inputBox(width int) []string {
	border, muted := m.colors.border, m.colors.muted
	bold := lipgloss.NewStyle().Bold(true)
	label := " " + bold.Render(m.label) + " "
	switch m.phase() {
	case asking:
		label = " " + m.colors.accent.Render(m.spin.View()) + " " + bold.Render("Asking…") + " "
	case asked:
		label = " " + bold.Render("Sessions by description") + " "
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

func (m picker) hints() [][2]string {
	switch {
	case m.phase() == asking:
		return [][2]string{{"esc", "stop asking"}}
	case m.phase() == asked && len(m.shown) == 0:
		return [][2]string{{"esc", "back to search"}}
	case m.phase() == asked:
		return [][2]string{{"enter", "resume"}, {"tab", "view"}, {"esc", "back to search"}}
	case len(m.all) == 0:
		return [][2]string{{"esc", "quit"}}
	case len(m.shown) == 0:
		return [][2]string{{"ctrl-r", "ask"}, {"ctrl-u", "clear search"}}
	}
	return [][2]string{{"enter", "resume"}, {"tab", "view"}, {"ctrl-r", "ask"}}
}

func (m picker) footer(width int) string {
	muted := m.colors.muted
	line, used := "", 1
	for i, p := range m.hints() {
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
	if !m.started || m.quitting {
		return tea.NewView("")
	}
	l := m.layout()
	muted := m.colors.muted
	column := m.inputBox(l.listWidth)
	column = append(column, ansi.Truncate(muted.Render(" "+m.message), l.listWidth, "…"))
	cols, words := m.widths.Fit(l.listWidth), m.matchWords()
	for i := m.offset; i < m.offset+l.listHeight; i++ {
		if i < len(m.shown) {
			column = append(column, m.rowLine(m.shown[i], i == m.cursor, l.listWidth, cols, words))
		} else {
			column = append(column, "")
		}
	}
	preview := m.previewLines(l)
	var screen []string
	switch {
	case l.hasPreview() && !l.stacked:
		column = append(column, "", m.footer(l.listWidth))
		divider := m.colors.border.Render("│")
		for i := range preview {
			left := ""
			if i < len(column) {
				left = column[i]
			}
			screen = append(screen, pad(left, l.listWidth)+" "+divider+" "+preview[i])
		}
	case l.stacked:
		screen = append(column, m.colors.border.Render(strings.Repeat("─", m.width)))
		screen = append(append(screen, preview...), "", m.footer(m.width))
	default:
		screen = append(column, "", m.footer(m.width))
	}
	screen = append(make([]string, marginTop), screen[:min(len(screen), m.height)]...)
	screen = screen[:min(len(screen), m.screenHeight)]
	side := strings.Repeat(" ", marginSide)
	for i := marginTop; i < len(screen); i++ {
		screen[i] = side + screen[i]
	}
	view := tea.NewView(strings.Join(screen, "\n"))
	view.AltScreen = true
	if c := m.input.Cursor(); c != nil && !m.asking && m.height > 1 {
		c.Position.X += 2 + marginSide
		c.Position.Y += 1 + marginTop
		view.Cursor = c
	}
	return view
}

func pathsOf(rows []session.Row) []string {
	paths := make([]string, len(rows))
	for i, r := range rows {
		paths[i] = r.Path
	}
	return paths
}

func Pick() (*session.Row, error) {
	problem := ""
	db, err := session.OpenSearch()
	if err != nil {
		problem = "Search is unavailable: " + err.Error()
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("superfind pick needs a terminal: %w", err)
	}
	defer tty.Close()
	options := []tea.ProgramOption{tea.WithInput(tty), tea.WithOutput(tty)}
	if render.NoColor {
		options = append(options, tea.WithColorProfile(colorprofile.Ascii))
	}
	start := newPicker(session.CachedRows(), db, problem)
	final, err := tea.NewProgram(start, options...).Run()
	start.asks.stop()
	stopped := make(chan struct{})
	go func() {
		start.asks.running.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
	}
	if err != nil {
		return nil, err
	}
	return final.(picker).chosen, nil
}
