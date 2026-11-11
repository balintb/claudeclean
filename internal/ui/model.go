package ui

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/balintb/claudeclean/internal/cleaner"
	"github.com/balintb/claudeclean/internal/format"
	"github.com/balintb/claudeclean/internal/session"
)

type Deleter interface {
	Run(ctx context.Context, sessions []*session.Session, progress func(cleaner.Event)) cleaner.Report
}

type Options struct {
	Root    string
	Load    func(context.Context) (*session.Result, error)
	Deleter Deleter
	DryRun  bool
	Journal string
	Home    string
	Now     func() time.Time
}

type mode int

const (
	modeLoading mode = iota
	modeBrowse
	modeConfirm
	modeTrashing
	modeResult
	modeHelp
	modeError
)

type (
	loadedMsg struct {
		res *session.Result
		err error
	}
	progressMsg  cleaner.Event
	doneMsg      cleaner.Report
	toastDoneMsg struct{ id int }
)

type toastKind int

const (
	toastInfo toastKind = iota
	toastWarn
	toastGood
)

type confirmState struct {
	targets []*session.Session
	live    int // selected but in use, will be skipped
	hidden  int // targets not visible under the current filter
	scope   string
	size    int64
	files   int
	focus   int // 0 = cancel (default), 1 = move to trash
}

type trashState struct {
	total, done int
	current     string
	cancel      context.CancelFunc
	cancelling  bool
	ch          chan tea.Msg
}

type Model struct {
	opt    Options
	keys   keyMap
	st     styles
	dark   bool
	width  int
	height int

	mode     mode
	loadErr  error
	warnings []string

	all       []*session.Session
	rows      []row
	cursor    int
	offset    int
	selected  map[string]bool
	collapsed map[string]bool
	sortBy    sortMode
	grouped   bool
	detail    bool // narrow terminals: show details instead of the list

	filter    textinput.Model
	filtering bool

	spin  spinner.Model
	bar   progress.Model
	help  help.Model
	toast struct {
		id   int
		text string
		kind toastKind
	}

	confirm confirmState
	trash   trashState
	report  cleaner.Report
}

func New(opt Options) Model {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	m := Model{
		opt:       opt,
		keys:      defaultKeys(),
		dark:      true,
		mode:      modeLoading,
		selected:  map[string]bool{},
		collapsed: map[string]bool{},
		grouped:   true,
	}
	m.filter = textinput.New()
	m.filter.Prompt = ""
	m.filter.Placeholder = "type to filter by title, folder, prompt, branch or id"
	m.filter.CharLimit = 200
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot))
	m.help = help.New()
	m.applyTheme()
	return m
}

func (m *Model) applyTheme() {
	m.st = newStyles(m.dark)
	m.spin.Style = m.st.accent
	m.bar = progress.New(
		progress.WithColors(m.st.p.accent, m.st.p.violet),
		progress.WithoutPercentage(),
		progress.WithFillCharacters('━', '─'),
	)
	m.bar.EmptyColor = m.st.p.border
	hs := help.DefaultStyles(m.dark)
	hs.ShortKey, hs.FullKey = m.st.key, m.st.key
	hs.ShortDesc, hs.FullDesc = m.st.keyDesc, m.st.keyDesc
	hs.ShortSeparator, hs.FullSeparator = m.st.keySep, m.st.keySep
	m.help.Styles = hs
	ts := textinput.DefaultStyles(m.dark)
	ts.Focused.Text = m.st.text
	ts.Blurred.Text = m.st.muted
	ts.Focused.Placeholder = m.st.faint
	ts.Blurred.Placeholder = m.st.faint
	ts.Cursor.Color = m.st.p.accent
	m.filter.SetStyles(ts)
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.load(), m.spin.Tick, tea.RequestBackgroundColor)
}

func (m Model) load() tea.Cmd {
	load := m.opt.Load
	return func() tea.Msg {
		if load == nil {
			return loadedMsg{err: fmt.Errorf("no loader configured")}
		}
		res, err := load(context.Background())
		return loadedMsg{res: res, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		m.clampScroll()
		return m, nil

	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.applyTheme()
		return m, nil

	case spinner.TickMsg:
		if m.mode != modeLoading && m.mode != modeTrashing {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case loadedMsg:
		return m.onLoaded(msg), nil

	case progressMsg:
		m.trash.done = msg.Done
		m.trash.total = msg.Total
		if msg.Outcome.Session != nil {
			m.trash.current = msg.Outcome.Session.Title
		}
		return m, listen(m.trash.ch)

	case doneMsg:
		return m.onDone(cleaner.Report(msg)), nil

	case toastDoneMsg:
		if msg.id == m.toast.id {
			m.toast.text = ""
		}
		return m, nil

	case tea.MouseWheelMsg:
		if m.mode == modeBrowse {
			switch msg.Button {
			case tea.MouseWheelUp:
				m.moveCursor(-3)
			case tea.MouseWheelDown:
				m.moveCursor(3)
			}
		}
		return m, nil

	case tea.MouseClickMsg:
		if m.mode == modeBrowse && msg.Button == tea.MouseLeft {
			return m.onClick(msg.X, msg.Y)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.onKey(msg)
	}

	if m.filtering {
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) onLoaded(msg loadedMsg) Model {
	if msg.err != nil {
		m.loadErr = msg.err
		m.mode = modeError
		return m
	}
	m.loadErr = nil
	m.all = msg.res.Sessions
	m.warnings = msg.res.Warnings
	present := map[string]bool{}
	for _, s := range m.all {
		if !s.Live {
			present[s.Key()] = true
		}
	}
	for k := range m.selected {
		if !present[k] {
			delete(m.selected, k)
		}
	}
	m.mode = modeBrowse
	m.rebuild()
	return m
}

func (m Model) onDone(rep cleaner.Report) Model {
	m.report = rep
	if m.trash.cancel != nil {
		m.trash.cancel()
	}
	m.trash = trashState{}
	gone := map[string]bool{}
	for _, o := range rep.Outcomes {
		if o.Status == cleaner.StatusTrashed {
			gone[o.Session.Key()] = true
			delete(m.selected, o.Session.Key())
		}
	}
	if len(gone) > 0 {
		kept := m.all[:0:0]
		for _, s := range m.all {
			if !gone[s.Key()] {
				kept = append(kept, s)
			}
		}
		m.all = kept
	}
	m.mode = modeResult
	m.rebuild()
	return m
}

func listen(ch chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m Model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.ForceQuit) {
		if m.mode == modeTrashing {
			// Never abandon a delete midway; stop after the current session.
			if !m.trash.cancelling && m.trash.cancel != nil {
				m.trash.cancelling = true
				m.trash.cancel()
			}
			return m, nil
		}
		return m, tea.Quit
	}

	switch m.mode {
	case modeLoading:
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		return m, nil
	case modeError:
		switch {
		case key.Matches(msg, m.keys.Rescan):
			m.mode = modeLoading
			return m, tea.Batch(m.load(), m.spin.Tick)
		case key.Matches(msg, m.keys.Quit), key.Matches(msg, m.keys.Back):
			return m, tea.Quit
		}
		return m, nil
	case modeConfirm:
		return m.onConfirmKey(msg)
	case modeTrashing:
		return m, nil
	case modeResult:
		if key.Matches(msg, m.keys.Activate, m.keys.Back, m.keys.Toggle, m.keys.Quit) {
			m.mode = modeBrowse
		}
		return m, nil
	case modeHelp:
		if key.Matches(msg, m.keys.Help, m.keys.Back, m.keys.Quit, m.keys.Activate) {
			m.mode = modeBrowse
		}
		return m, nil
	}

	if m.filtering {
		return m.onFilterKey(msg)
	}
	return m.onBrowseKey(msg)
}

func (m Model) onFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter.Blur()
		m.filter.SetValue("")
		m.rebuild()
		return m, nil
	case "enter":
		m.filtering = false
		m.filter.Blur()
		return m, nil
	case "up", "down":
		if msg.String() == "up" {
			m.moveCursor(-1)
		} else {
			m.moveCursor(1)
		}
		return m, nil
	}
	before := m.filter.Value()
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	if m.filter.Value() != before {
		m.cursor, m.offset = 0, 0
		m.rebuild()
	}
	return m, cmd
}

func (m Model) onBrowseKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case key.Matches(msg, k.Back):
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.rebuild()
		} else if m.detail {
			m.detail = false
		}
		return m, nil
	case key.Matches(msg, k.Help):
		m.mode = modeHelp
	case key.Matches(msg, k.Up):
		m.moveCursor(-1)
	case key.Matches(msg, k.Down):
		m.moveCursor(1)
	case key.Matches(msg, k.PageUp):
		m.moveCursor(-max(m.listHeight()-1, 1))
	case key.Matches(msg, k.PageDown):
		m.moveCursor(max(m.listHeight()-1, 1))
	case key.Matches(msg, k.Home):
		m.moveCursor(-len(m.rows))
	case key.Matches(msg, k.End):
		m.moveCursor(len(m.rows))
	case key.Matches(msg, k.Collapse):
		m.setCollapsed(true)
	case key.Matches(msg, k.Expand):
		m.setCollapsed(false)
	case key.Matches(msg, k.Activate):
		if r, ok := m.current(); ok && r.kind == rowGroup {
			m.setCollapsed(!m.collapsed[r.group.key])
		}
	case key.Matches(msg, k.Toggle):
		return m.toggleCurrent()
	case key.Matches(msg, k.SelectAll):
		return m.selectWhere("shown", func(*session.Session) bool { return true })
	case key.Matches(msg, k.SelectNone):
		n := len(m.selected)
		m.selected = map[string]bool{}
		if n > 0 {
			return m.notify(toastInfo, "Selection cleared")
		}
	case key.Matches(msg, k.SelectFolder):
		r, ok := m.current()
		if !ok {
			return m, nil
		}
		dir := r.group
		if dir == nil && r.sess != nil {
			pd := r.sess.ProjectDir
			return m.selectWhere("in this folder", func(s *session.Session) bool { return s.ProjectDir == pd })
		}
		if dir != nil {
			return m.selectWhere("in this folder", func(s *session.Session) bool { return s.ProjectDir == dir.key })
		}
	case key.Matches(msg, k.SelectOrphans):
		return m.selectWhere("whose folder is gone", func(s *session.Session) bool { return !s.CWDExists })
	case key.Matches(msg, k.Delete):
		return m.askDelete()
	case key.Matches(msg, k.Filter):
		m.filtering = true
		return m, m.filter.Focus()
	case key.Matches(msg, k.Sort):
		m.sortBy = (m.sortBy + 1) % sortModeCount
		m.rebuild()
		return m.notify(toastInfo, "Sorted by "+m.sortBy.String())
	case key.Matches(msg, k.View):
		m.grouped = !m.grouped
		m.rebuild()
		if m.grouped {
			return m.notify(toastInfo, "Grouped by folder")
		}
		return m.notify(toastInfo, "Flat list")
	case key.Matches(msg, k.Detail):
		m.detail = !m.detail
	case key.Matches(msg, k.Rescan):
		m.mode = modeLoading
		return m, tea.Batch(m.load(), m.spin.Tick)
	}
	return m, nil
}

func (m Model) onConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Confirm):
		return m.startTrash()
	case key.Matches(msg, k.Cancel):
		m.mode = modeBrowse
		m.confirm = confirmState{}
		return m.notify(toastInfo, "Nothing was moved")
	case key.Matches(msg, k.SwitchButton):
		m.confirm.focus = 1 - m.confirm.focus
	case key.Matches(msg, k.Activate):
		if m.confirm.focus == 1 {
			return m.startTrash()
		}
		m.mode = modeBrowse
		m.confirm = confirmState{}
		return m.notify(toastInfo, "Nothing was moved")
	}
	return m, nil
}

func (m Model) toggleCurrent() (tea.Model, tea.Cmd) {
	r, ok := m.current()
	if !ok {
		return m, nil
	}
	if r.kind == rowGroup {
		var eligible []*session.Session
		allOn := true
		for _, s := range r.group.sessions {
			if s.Live {
				continue
			}
			eligible = append(eligible, s)
			if !m.selected[s.Key()] {
				allOn = false
			}
		}
		if len(eligible) == 0 {
			return m.notify(toastWarn, "Every session in this folder is in use")
		}
		for _, s := range eligible {
			if allOn {
				delete(m.selected, s.Key())
			} else {
				m.selected[s.Key()] = true
			}
		}
		return m, nil
	}
	s := r.sess
	if s.Live {
		return m.notify(toastWarn, "This session is in use and can't be trashed: "+s.LiveReason)
	}
	if m.selected[s.Key()] {
		delete(m.selected, s.Key())
	} else {
		m.selected[s.Key()] = true
	}
	m.moveCursor(1)
	return m, nil
}

func (m Model) selectWhere(what string, pred func(*session.Session) bool) (tea.Model, tea.Cmd) {
	added, live := 0, 0
	for _, r := range m.visibleSessions() {
		if !pred(r) {
			continue
		}
		if r.Live {
			live++
			continue
		}
		if !m.selected[r.Key()] {
			m.selected[r.Key()] = true
			added++
		}
	}
	text := fmt.Sprintf("Selected %s %s", format.Plural(added, "session", "sessions"), what)
	if live > 0 {
		text += fmt.Sprintf(" (%d in use, skipped)", live)
	}
	return m.notify(toastInfo, text)
}

func (m Model) visibleSessions() []*session.Session {
	terms := filterTerms(m.filter.Value())
	var out []*session.Session
	for _, s := range m.all {
		if matches(s, terms, m.opt.Home) {
			out = append(out, s)
		}
	}
	return out
}

func (m Model) askDelete() (tea.Model, tea.Cmd) {
	var targets []*session.Session
	scope := ""
	if len(m.selected) > 0 {
		for _, s := range m.all {
			if m.selected[s.Key()] {
				targets = append(targets, s)
			}
		}
		scope = "selected"
	} else if r, ok := m.current(); ok {
		if r.kind == rowGroup {
			targets = append(targets, r.group.sessions...)
			scope = "folder " + r.group.label
		} else {
			targets = []*session.Session{r.sess}
		}
	}
	if len(targets) == 0 {
		return m.notify(toastWarn, "Nothing to move: select sessions with space first")
	}

	visible := map[string]bool{}
	for _, s := range m.visibleSessions() {
		visible[s.Key()] = true
	}
	c := confirmState{scope: scope}
	for _, s := range targets {
		if s.Live {
			c.live++
			continue
		}
		c.targets = append(c.targets, s)
		c.size += s.Size
		c.files += s.FileCount()
		if !visible[s.Key()] {
			c.hidden++
		}
	}
	if len(c.targets) == 0 {
		if c.live == 1 {
			return m.notify(toastWarn, "This session is in use and can't be trashed")
		}
		return m.notify(toastWarn, fmt.Sprintf("All %d sessions are in use and can't be trashed", c.live))
	}
	m.confirm = c
	m.mode = modeConfirm
	return m, nil
}

func (m Model) startTrash() (tea.Model, tea.Cmd) {
	targets := m.confirm.targets
	m.confirm = confirmState{}
	if len(targets) == 0 || m.opt.Deleter == nil {
		m.mode = modeBrowse
		return m.notify(toastWarn, "Nothing was moved")
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Buffered so the worker never blocks if the UI stops listening.
	ch := make(chan tea.Msg, len(targets)+2)
	m.trash = trashState{total: len(targets), cancel: cancel, ch: ch}
	m.mode = modeTrashing
	d := m.opt.Deleter
	go func() {
		defer close(ch)
		rep := d.Run(ctx, targets, func(e cleaner.Event) { ch <- progressMsg(e) })
		ch <- doneMsg(rep)
	}()
	return m, tea.Batch(listen(ch), m.spin.Tick)
}

func (m Model) notify(kind toastKind, text string) (tea.Model, tea.Cmd) {
	m.toast.id++
	m.toast.text = text
	m.toast.kind = kind
	id := m.toast.id
	return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return toastDoneMsg{id: id} })
}

func (m *Model) rebuild() {
	cur := ""
	if r, ok := m.current(); ok {
		cur = r.id()
	}
	m.rows = buildRows(m.all, m.filter.Value(), m.sortBy, m.grouped, m.collapsed, m.opt.Home)
	m.cursor = min(m.cursor, max(len(m.rows)-1, 0))
	if cur != "" {
		for i, r := range m.rows {
			if r.id() == cur {
				m.cursor = i
				break
			}
		}
	}
	m.clampScroll()
}

func (m Model) current() (row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return row{}, false
	}
	return m.rows[m.cursor], true
}

func (m *Model) moveCursor(delta int) {
	if len(m.rows) == 0 {
		m.cursor = 0
		return
	}
	m.cursor = max(0, min(len(m.rows)-1, m.cursor+delta))
	m.clampScroll()
}

func (m *Model) setCollapsed(collapse bool) {
	r, ok := m.current()
	if !ok || !m.grouped || r.group == nil {
		return
	}
	m.collapsed[r.group.key] = collapse
	if collapse {
		for i, rr := range m.rows {
			if rr.kind == rowGroup && rr.group.key == r.group.key {
				m.cursor = i
				break
			}
		}
	}
	m.rebuild()
}

func (m *Model) clampScroll() {
	h := m.listHeight()
	if h <= 0 {
		m.offset = 0
		return
	}
	margin := min(2, max((h-1)/2, 0))
	if m.cursor < m.offset+margin {
		m.offset = m.cursor - margin
	}
	if m.cursor > m.offset+h-1-margin {
		m.offset = m.cursor - h + 1 + margin
	}
	m.offset = max(0, min(m.offset, max(len(m.rows)-h, 0)))
}

func (m Model) onClick(x, y int) (tea.Model, tea.Cmd) {
	l := m.layout()
	if !l.showList || x < l.listX || x >= l.listX+l.listW {
		return m, nil
	}
	i := y - l.listY
	if i < 0 || i >= l.listH {
		return m, nil
	}
	idx := m.offset + i
	if idx >= len(m.rows) {
		return m, nil
	}
	m.cursor = idx
	m.clampScroll()
	if x-l.listX <= 5 {
		mm, cmd := m.toggleCurrent()
		model := mm.(Model)
		model.cursor = idx
		model.clampScroll()
		return model, cmd
	}
	return m, nil
}

func (m Model) selectionStats() (n int, size int64, hidden int) {
	visible := map[string]bool{}
	for _, s := range m.visibleSessions() {
		visible[s.Key()] = true
	}
	for _, s := range m.all {
		if m.selected[s.Key()] {
			n++
			size += s.Size
			if !visible[s.Key()] {
				hidden++
			}
		}
	}
	return n, size, hidden
}
