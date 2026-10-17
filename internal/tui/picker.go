package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image/color"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/ask"
	"github.com/adammiribyan/superfind/internal/ghostty"
	"github.com/adammiribyan/superfind/internal/news"
	"github.com/adammiribyan/superfind/internal/open"
	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
	"github.com/adammiribyan/superfind/internal/store"
	"github.com/adammiribyan/superfind/internal/typesafe"
)

const (
	wideColumns    = 100
	headerLines    = 4
	colorTimeout   = time.Second
	labelsInterval = 1500 * time.Millisecond
	marginTop      = 1
	marginSide     = 1
	compactRows    = 23
	minBody        = 6
	minList        = 5
	minInput       = 10
	minStatusTitle = 8
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
	compact       bool
	search        int
	message       int
}

func (l layout) hasPreview() bool { return l.previewWidth > 0 && l.previewHeight > 0 }

func (l layout) bodyHeight() int { return max(l.previewHeight-headerLines-1, 0) }

const secondaryMark = "\x00"

var placeholders = []string{"Search sessions and conversations", "Search sessions", "Search"}

type bodyKey struct {
	path   string
	width  int
	height int
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

type heldMsg map[open.Key]open.State

type picker struct {
	rows       []session.Row
	agents     bool
	all        []session.Row
	shown      []session.Row
	db         *sql.DB
	problem    string
	dbProblem  string
	statusSeen time.Time
	held       map[open.Key]open.State
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
	notice      string
	feed        *news.Feed
	confirming  string
	inTmux      bool
	focus       func(open.State) bool

	asks     *asks
	reported *atomic.Bool
	chosen   *session.Row
	folder   bool
	quitting bool
	reindex  bool
	indexing bool
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

func (m *picker) populate() {
	m.all = session.SameKind(m.rows, m.agents)
	m.tags, m.label = render.TagsFor(m.all), render.PickerLabel(m.all, agent.Places())
	m.widths = render.Measure(m.all, m.tags)
}

func newPicker(rows []session.Row, db *sql.DB, problem string) picker {
	in := textinput.New()
	in.Prompt = ""
	in.SetVirtualCursor(false)
	in.Focus()
	held := open.Find(agent.Places())
	m := picker{
		rows: session.RecentFirst(session.Annotate(rows, held)), db: db, held: held,
		input: in, spin: spinner.New(spinner.WithSpinner(spinner.MiniDot)), bodies: map[bodyKey][]string{}, loading: map[bodyKey]bool{},
		asks: &asks{}, reported: &atomic.Bool{}, inTmux: os.Getenv("TMUX") != "", focus: ghostty.Focus,
	}
	m.populate()
	m.problem, m.dbProblem = problem, problem
	if len(rows) == 0 && problem == "" {
		m.problem, m.indexing = "Loading sessions…", true
	}
	m.filter()
	if info, err := os.Stat(store.CachePath("status.json")); err == nil {
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
		cmds = append(cmds, tea.Sequence(tea.RequestBackgroundColor, tea.RequestForegroundColor,
			tea.Raw(ansi.RequestPrimaryDeviceAttributes)))
	}
	return tea.Batch(cmds...)
}

func (m picker) compact() bool { return m.screenHeight <= compactRows }

func (m picker) layout() layout {
	l := layout{listWidth: m.width, compact: m.compact(), search: 3, message: 1}
	if l.compact {
		l.search = 1
		if m.shownMessage() == "" {
			l.message = 0
		}
	}
	free := max(m.height-l.search-l.message-2, 0)
	rest := free - 1
	preview := max(rest-rest/2, headerLines+1+minBody)
	switch {
	case m.hidePreview:
		l.listHeight = free
	case m.width >= wideColumns && m.height-headerLines-1 >= minBody:
		l.listWidth = m.width * 54 / 100
		l.previewWidth = m.width - l.listWidth - 3
		l.previewHeight = m.height
		l.listHeight = free
	case rest-preview >= minList:
		l.stacked = true
		l.previewWidth = m.width
		l.listHeight = rest - preview
		l.previewHeight = preview
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
	q := session.Parse(query)
	if q.Agents != m.agents {
		m.agents = q.Agents
		m.populate()
	}
	m.message = m.problem
	var err error
	switch {
	case strings.TrimSpace(query) == "", m.db == nil:
		m.shown = q.Rows(m.all, false)
		if len(m.all) == 0 && len(m.rows) > 0 && m.problem == "" && strings.TrimSpace(query) == "" {
			m.message = "Only automated sessions found. Type is:agent to see them."
		}
	default:
		m.shown, err = session.Search(m.db, query, m.all)
		if errors.Is(err, session.ErrDamaged) {
			m.db, m.reindex = nil, true
			m.shown = q.Rows(m.all, false)
		}
		switch {
		case err != nil:
			m.message = "Search failed: " + err.Error()
		case len(m.shown) == 0 && len(m.all) > 0 && q.Words != "":
			m.message = "No keyword matches. Ctrl-R searches by description."
		case len(m.shown) == 0 && len(m.all) > 0:
			m.message = "No sessions match “" + query + "”"
		}
	}
	if q.Problem != nil && err == nil {
		m.message = q.Problem.Hint
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
	req := render.PreviewRequest{Agent: r.Agent, Path: r.Path, Width: l.previewWidth, Recent: m.recent,
		Query: query, Explained: m.explained[r.Path], Secondary: m.colors.mutedSeq}
	m.header = render.PreviewHeader(req, *r, m.tags, m.db)
	height := m.bodyHeight(l)
	if !m.recent {
		req.Height = height
	}
	key := bodyKey{path: r.Path, width: l.previewWidth, height: req.Height, recent: m.recent}
	if id, _, err := r.Agent.Stat(r.Path); err == nil {
		key.file = id
	}
	m.showing = key
	m.body.SetWidth(l.previewWidth)
	m.body.SetHeight(height)
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
	q := session.Parse(m.input.Value())
	query, rows := q.Words, q.Rows(m.all, false)
	if query == "" || len(m.all) == 0 || q.Problem != nil {
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

func findHeld() tea.Msg { return heldMsg(open.Find(agent.Places())) }

func (m picker) reannotate() (tea.Model, tea.Cmd) {
	focused := ""
	if r := m.focused(); r != nil {
		focused = r.Path
	}
	m.rows = session.Annotate(m.rows, m.held)
	m.populate()
	if m.askedFor == "" {
		m.filter()
	} else {
		_, m.shown = session.Narrow(m.input.Value(), session.InOrder(m.all, pathsOf(m.shown)))
	}
	if i := slices.Index(pathsOf(m.shown), focused); i >= 0 {
		m.cursor = i
	}
	if !m.asksToConfirm() {
		m.confirming = ""
	}
	return m.afterMove()
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
	if width := next.inputWidth(); width != next.input.Width() {
		next.input.SetWidth(width)
		next.input.SetCursor(next.input.Position())
	}
	for _, placeholder := range placeholders {
		next.input.Placeholder = placeholder
		if next.input.Width()+1 >= lipgloss.Width(placeholder) {
			break
		}
	}
	return next, cmd
}

func (m picker) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.screenHeight = msg.Height
		m.width, m.height = max(msg.Width-2*marginSide, 1), max(msg.Height-marginTop, 1)
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
	case colorsSettled, uv.PrimaryDeviceAttributesEvent:
		if !m.colors.settled {
			m.settleColors()
			return m.loadPreview()
		}
	case indexed:
		focused := ""
		if r := m.focused(); r != nil {
			focused = r.Path
		}
		m.held = open.Find(agent.Places())
		m.rows, m.indexing = session.RecentFirst(session.Annotate(msg.rows, m.held)), false
		m.populate()
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
		} else if len(m.rows) == 0 && m.problem == "" {
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
		next := tea.Batch(tea.Tick(labelsInterval, func(time.Time) tea.Msg { return labelsTick{} }), findHeld)
		if line, changed := m.feed.Poll(); changed {
			m.notice = line
		}
		info, err := os.Stat(store.CachePath("status.json"))
		if err != nil || !info.ModTime().After(m.statusSeen) {
			return m, next
		}
		m.statusSeen = info.ModTime()
		updated, load := m.reannotate()
		return updated, tea.Batch(next, load)
	case heldMsg:
		if maps.EqualFunc(m.held, msg, open.State.Equal) {
			return m, nil
		}
		m.held = msg
		return m.reannotate()
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
			m.message = "No sessions match “" + msg.query + "”"
		}
		updated, load := m.loadPreview()
		key, model := ask.Ranker()
		top := msg.paths[:min(ask.ExplainTop, len(msg.paths))]
		if model != "jev" || key == "" || len(top) == 0 || updated.db == nil {
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
	case tea.PasteMsg:
		m.confirming = ""
	case tea.KeyPressMsg:
		confirming := m.asksToConfirm()
		m.confirming = ""
		switch msg.String() {
		case "esc":
			if confirming {
				return m, nil
			}
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
			return m.enter(confirming)
		case "ctrl+o":
			return m.goToFolder()
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
			return m.afterMove()
		case "alt+up", "shift+up":
			m.body.PageUp()
			m.pinned = m.body.AtBottom()
			return m, nil
		case "alt+down", "shift+down":
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

const confirmCopy = "Already open. Enter again resumes a copy. Esc cancels."

func freshest(cached, fresh *open.State) *open.State {
	if fresh != nil && fresh.Tmux == nil && cached != nil && cached.PID == fresh.PID {
		kept := *fresh
		kept.Tmux = cached.Tmux
		return &kept
	}
	return fresh
}

func (m picker) asksToConfirm() bool {
	r := m.focused()
	return r != nil && r.Open != nil && r.Path == m.confirming
}

func (m picker) shownMessage() string {
	if m.asksToConfirm() {
		return confirmCopy
	}
	if m.message == "" {
		return m.notice
	}
	return m.message
}

func (m picker) enter(confirmed bool) (tea.Model, tea.Cmd) {
	r := m.focused()
	if r == nil {
		m.quitting = true
		return m, tea.Quit
	}
	if _, err := agent.ResumeCommand(r.Agent, r.Home, r.Path, r.Session.Cwd); err != nil {
		m.message = err.Error()
		return m, nil
	}
	var fresh *open.State
	if state, ok := open.Find(agent.Places())[open.KeyOf(r.Agent.ID, r.Session.ID)]; ok {
		fresh = &state
	}
	chosen := *r
	chosen.Open = freshest(r.Open, fresh)
	reach := open.NotOpen
	if !confirmed {
		reach = open.Reaching(chosen.Open, m.inTmux, m.focus)
	}
	switch reach {
	case open.Focused:
		m.quitting = true
		return m, tea.Quit
	case open.Elsewhere:
		m.shown = slices.Clone(m.shown)
		m.shown[m.cursor].Open = chosen.Open
		m.confirming = r.Path
		return m, nil
	}
	m.chosen, m.quitting = &chosen, true
	return m, tea.Quit
}

func (m picker) goToFolder() (tea.Model, tea.Cmd) {
	r := m.focused()
	if r == nil {
		return m, nil
	}
	chosen := *r
	m.chosen, m.folder, m.quitting = &chosen, true, true
	return m, tea.Quit
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
	right += m.gauge(r, c.Gauge, paint)
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

func (m picker) searchLabel() string {
	bold := lipgloss.NewStyle().Bold(true)
	switch m.phase() {
	case asking:
		return m.colors.accent.Render(m.spin.View()) + " " + bold.Render("Asking…")
	case asked:
		return bold.Render("Sessions by description")
	}
	return bold.Render(m.label)
}

func (m picker) count() string {
	if len(m.all) == 0 {
		return ""
	}
	return m.colors.muted.Render(fmt.Sprintf("%d/%d", len(m.shown), len(m.all)))
}

func (m picker) compactSearch(width int) (string, int) {
	label, room := m.searchLabel(), width-2-lipgloss.Width(m.count())-1
	if room-lipgloss.Width(label)-2 < minInput {
		return "", room
	}
	return label + "  ", room - lipgloss.Width(label) - 2
}

func (m picker) inputWidth() int {
	l := m.layout()
	if !l.compact {
		return max(l.listWidth-12, 4)
	}
	_, room := m.compactSearch(l.listWidth)
	return max(room-1, 4)
}

func (m picker) inputBox(width int) []string {
	count := m.count()
	if m.compact() {
		label, _ := m.compactSearch(width)
		inner := max(width-2-lipgloss.Width(label), 0)
		field := ansi.Truncate(m.input.View(), max(inner-lipgloss.Width(count)-1, 0), "")
		gap := max(inner-lipgloss.Width(field)-lipgloss.Width(count), 1)
		return []string{ansi.Truncate(" "+label+field+strings.Repeat(" ", gap)+count+" ", width, "")}
	}
	border := m.colors.border
	top := ansi.Truncate(border.Render("╭─")+" "+m.searchLabel()+" ", max(width-1, 0), "")
	top += border.Render(strings.Repeat("─", max(width-lipgloss.Width(top)-1, 0)) + "╮")
	inner := max(width-4, 0)
	field := ansi.Truncate(m.input.View(), max(inner-lipgloss.Width(count)-1, 0), "")
	gap := max(inner-lipgloss.Width(field)-lipgloss.Width(count), 1)
	middle := border.Render("│") + " " + field + strings.Repeat(" ", gap) + count + " " + border.Render("│")
	bottom := border.Render("╰" + strings.Repeat("─", max(width-2, 0)) + "╯")
	return []string{top, middle, bottom}
}

func (m picker) hints() [][2]string {
	enter, r := "resume", m.focused()
	switch {
	case m.asksToConfirm():
		enter = "resume a copy"
	case r != nil && r.Open != nil && r.Open.Tmux != nil && m.inTmux:
		enter = "go to it"
	}
	switch {
	case m.phase() == asking:
		return [][2]string{{"esc", "stop asking"}}
	case m.phase() == asked && len(m.shown) == 0:
		return [][2]string{{"esc", "back to search"}}
	case m.phase() == asked:
		return [][2]string{{"enter", enter}, {"tab", "view"}, {"ctrl-o", "go to folder"}, {"esc", "back to search"}}
	case len(m.all) == 0:
		return [][2]string{{"esc", "quit"}}
	case len(m.shown) == 0:
		return [][2]string{{"ctrl-r", "ask"}, {"ctrl-u", "clear search"}}
	}
	return [][2]string{{"enter", enter}, {"tab", "view"}, {"ctrl-r", "ask"}, {"ctrl-o", "go to folder"}}
}

var hintPriority = map[string]int{"esc": 0, "enter": 1, "ctrl-r": 2, "ctrl-u": 3, "tab": 4, "ctrl-o": 5}

func (m picker) footer(width int) string {
	hints := m.hints()
	for len(hints) > 0 && 1+lipgloss.Width(m.joinHints(hints)) > width {
		drop := 0
		for i, h := range hints {
			if hintPriority[h[0]] >= hintPriority[hints[drop][0]] {
				drop = i
			}
		}
		hints = slices.Delete(hints, drop, drop+1)
	}
	return " " + m.joinHints(hints)
}

func (m picker) joinHints(hints [][2]string) string {
	muted := m.colors.muted
	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = h[0] + muted.Render(" "+h[1])
	}
	return strings.Join(parts, muted.Render(" · "))
}

func fitMessage(message string, width int) string {
	for lipgloss.Width(message) > width {
		end := strings.LastIndex(message, ". ")
		if end < 0 {
			break
		}
		message = message[:end+1]
	}
	return message
}

func (m picker) matchLineFree() bool {
	return len(m.header) == headerLines && m.header[headerLines-1] == ""
}

func (m picker) bodyHeight(l layout) int {
	if m.matchLineFree() && l.previewHeight > headerLines {
		return l.bodyHeight() + 1
	}
	return l.bodyHeight()
}

func (m picker) previewLines(l layout) []string {
	if !l.hasPreview() {
		return nil
	}
	lines := append([]string{}, m.header...)
	for len(lines) < headerLines {
		lines = append(lines, "")
	}
	if !m.matchLineFree() {
		lines = append(lines, "")
	}
	if m.bodyHeight(l) > 0 {
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
	if l.message > 0 {
		column = append(column, ansi.Truncate(muted.Render(" "+fitMessage(m.shownMessage(), l.listWidth-1)), l.listWidth, "…"))
	}
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
	view.ProgramStatus = m.status()
	if view.ProgramStatus != nil {
		m.reported.Store(true)
	}
	if c := m.input.Cursor(); c != nil && !m.asking && m.height > 1 {
		x, y := 2, 1
		if l.compact {
			label, _ := m.compactSearch(l.listWidth)
			x, y = 1+lipgloss.Width(label), 0
		}
		c.Position.X += marginSide + x
		c.Position.Y += marginTop + y
		view.Cursor = c
	}
	return view
}

func (m picker) status() *tea.ProgramStatus {
	switch {
	case m.asking:
		return &tea.ProgramStatus{State: tea.ProgramStateWorking, App: "superfind", Title: "Asking"}
	case m.indexing:
		return &tea.ProgramStatus{State: tea.ProgramStateWorking, App: "superfind", Title: "Indexing"}
	}
	return nil
}

func pathsOf(rows []session.Row) []string {
	paths := make([]string, len(rows))
	for i, r := range rows {
		paths[i] = r.Path
	}
	return paths
}

func Pick(b news.Build) (*session.Row, bool, error) {
	problem := ""
	db, err := session.OpenSearch()
	if err != nil {
		problem = "Search is unavailable: " + err.Error()
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, false, fmt.Errorf("superfind pick needs a terminal: %w", err)
	}
	defer tty.Close()
	options := []tea.ProgramOption{tea.WithInput(tty), tea.WithOutput(tty)}
	if render.NoColor {
		options = append(options, tea.WithColorProfile(colorprofile.Ascii))
	}
	start := newPicker(session.CachedRows(), db, problem)
	start.feed = news.Open(b)
	start.notice = start.feed.Line()
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
	if start.reported.Load() {
		tty.WriteString(ansi.ClearProgramStatus)
	}
	if err != nil {
		return nil, false, err
	}
	return final.(picker).chosen, final.(picker).folder, nil
}
