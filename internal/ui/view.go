package ui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/claudeclean/internal/cleaner"
	"github.com/balintb/claudeclean/internal/format"
	"github.com/balintb/claudeclean/internal/layout"
	"github.com/balintb/claudeclean/internal/session"
)

const (
	minWidth  = 40
	minHeight = 12

	headerLines = 2 // title bar + toolbar
	footerLines = 2 // status + help
)

// screenLayout is shared with mouse handling so clicks hit the drawn row.
type screenLayout struct {
	showList, showDetail bool
	listPanelW, detailW  int
	bodyH                int
	listX, listY         int // top-left of the first list row, in cells
	listW, listH         int // size of the row area
}

func (m Model) layout() screenLayout {
	var l screenLayout
	w, h := m.width, m.height
	if w < minWidth || h < minHeight {
		return l
	}
	l.bodyH = h - headerLines - footerLines
	wide := w >= 100
	switch {
	case wide:
		l.showList, l.showDetail = true, true
		l.detailW = max(38, min(64, w*38/100))
		l.listPanelW = w - l.detailW
	case m.detail:
		l.showDetail = true
		l.detailW = w
	default:
		l.showList = true
		l.listPanelW = w
	}
	if l.showList {
		l.listX = 1
		l.listY = headerLines + 2 // top border + column header
		l.listW = l.listPanelW - 2
		l.listH = max(l.bodyH-3, 0) // borders + column header
	}
	return l
}

func (m Model) listHeight() int { return m.layout().listH }

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "claudeclean"
	if m.mode == modeTrashing && m.trash.total > 0 {
		v.ProgressBar = tea.NewProgressBar(tea.ProgressBarDefault, m.trash.done*100/m.trash.total)
	}
	return v
}

func (m Model) render() string {
	w, h := m.width, m.height
	if w <= 0 || h <= 0 {
		return ""
	}
	if w < minWidth || h < minHeight {
		msg := m.st.muted.Render(fmt.Sprintf("Terminal too small: %d×%d, need %d×%d", w, h, minWidth, minHeight))
		return place(w, h, format.Truncate(msg, w))
	}
	switch m.mode {
	case modeLoading:
		return place(w, h, m.loadingView())
	case modeError:
		return place(w, h, m.errorView())
	}

	base := m.browseView()
	var modal string
	switch m.mode {
	case modeConfirm:
		modal = m.confirmView()
	case modeTrashing:
		modal = m.trashingView()
	case modeResult:
		modal = m.resultView()
	case modeHelp:
		modal = m.helpView()
	}
	if modal == "" {
		return base
	}
	if ml := strings.Split(modal, "\n"); len(ml) > h {
		modal = strings.Join(ml[:h], "\n")
	}
	if lipgloss.Width(modal) > w {
		ml := strings.Split(modal, "\n")
		for i := range ml {
			ml[i] = ansi.Truncate(ml[i], w, "")
		}
		modal = strings.Join(ml, "\n")
	}
	mw, mh := lipgloss.Width(modal), lipgloss.Height(modal)
	x, y := max((w-mw)/2, 0), max((h-mh)/2, 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(modal).X(x).Y(y).Z(1),
	).Render()
}

func place(w, h int, s string) string {
	lines := strings.Split(lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, s), "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, ln := range lines {
		lines[i] = fit(ln, w)
	}
	return strings.Join(lines, "\n")
}

func (m Model) logo() string {
	return m.st.accent.Bold(true).Render("✻ ") + gradient("claudeclean", m.st.p.accent, m.st.p.violet, true)
}

func (m Model) loadingView() string {
	dir := format.TildePath(filepath.Join(m.opt.Root, layout.ProjectsDir), m.opt.Home)
	return lipgloss.JoinVertical(lipgloss.Center,
		m.logo(),
		"",
		m.spin.View()+" "+m.st.muted.Render("Reading sessions in "+format.Printable(dir)+"…"),
	)
}

func (m Model) errorView() string {
	body := lipgloss.JoinVertical(lipgloss.Left,
		m.st.red.Bold(true).Render("Could not read your Claude sessions"),
		"",
		m.st.text.Width(min(60, m.width-8)).Render(format.Printable(fmt.Sprint(m.loadErr))),
		"",
		m.keyHint("r", "retry")+"  "+m.keyHint("q", "quit"),
	)
	return m.box(body, m.st.p.red, min(66, m.width-2))
}

func (m Model) browseView() string {
	l := m.layout()
	parts := []string{m.headerView(), m.toolbarView()}
	var body string
	switch {
	case l.showList && l.showDetail:
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			m.listPanel(l.listPanelW, l.bodyH),
			m.detailPanel(l.detailW, l.bodyH),
		)
	case l.showDetail:
		body = m.detailPanel(l.detailW, l.bodyH)
	default:
		body = m.listPanel(l.listPanelW, l.bodyH)
	}
	parts = append(parts, body, m.statusView(), m.helpLine())
	return strings.Join(parts, "\n")
}

func (m Model) headerView() string {
	var total int64
	live := 0
	folders := map[string]bool{}
	for _, s := range m.all {
		total += s.Size
		folders[s.ProjectDir] = true
		if s.Live {
			live++
		}
	}
	left := " " + m.logo() + "  " + m.st.muted.Render(fmt.Sprintf("%s in %s · %s",
		format.Plural(len(m.all), "session", "sessions"),
		format.Plural(len(folders), "folder", "folders"),
		format.Bytes(total)))
	var badges []string
	if live > 0 {
		badges = append(badges, m.st.green.Render(fmt.Sprintf("● %d in use", live)))
	}
	if m.opt.DryRun {
		badges = append(badges, m.st.badgeDry.Render("DRY RUN"))
	}
	return spread(left, strings.Join(badges, "  ")+" ", m.width)
}

func (m Model) toolbarView() string {
	right := m.st.faint.Render("sort ") + m.st.muted.Render(m.sortBy.String()) +
		m.st.faint.Render("  ·  view ") + m.st.muted.Render(map[bool]string{true: "by folder", false: "flat"}[m.grouped]) + " "
	var left string
	switch {
	case m.filtering:
		f := m.filter
		f.SetWidth(max(m.width-lipgloss.Width(right)-8, 10))
		left = " " + m.st.accent.Bold(true).Render("/ ") + f.View()
	case m.filter.Value() != "":
		left = " " + m.st.accent.Render("/ ") + m.st.text.Render(format.Printable(m.filter.Value())) +
			m.st.faint.Render(fmt.Sprintf("  %s · esc clears", format.Plural(len(m.visibleSessions()), "match", "matches")))
	default:
		left = " " + m.st.faint.Render("/ filter")
	}
	return spread(left, right, m.width)
}

func (m Model) statusView() string {
	right := ""
	if len(m.rows) > 0 {
		right = m.st.faint.Render(fmt.Sprintf("%d/%d ", m.cursor+1, len(m.rows)))
	}
	room := m.width - lipgloss.Width(right) - 1
	var left string
	if m.toast.text != "" {
		icon, st := "• ", m.st.muted
		switch m.toast.kind {
		case toastWarn:
			icon, st = "! ", m.st.yellow
		case toastGood:
			icon, st = "✓ ", m.st.green
		}
		left = " " + st.Render(format.Truncate(icon+format.Printable(m.toast.text), max(room-1, 1)))
	} else if n, size, hidden := m.selectionStats(); n > 0 {
		left = " " + m.st.accent.Bold(true).Render(fmt.Sprintf("● %d selected", n)) +
			m.st.muted.Render(" · "+format.Bytes(size))
		if hidden > 0 {
			left += m.st.yellow.Render(fmt.Sprintf(" · %d hidden by filter", hidden))
		}
		if hint := m.st.faint.Render("  ·  d moves them to the Trash"); lipgloss.Width(left+hint) <= room {
			left += hint
		}
	} else {
		left = " " + m.st.faint.Render(format.Truncate("space selects · d moves the highlighted item to the Trash", max(room-1, 1)))
	}
	return spread(left, right, m.width)
}

func (m Model) helpLine() string {
	return padRight(format.Truncate(" "+m.help.ShortHelpView(m.keys.ShortHelp()), m.width), m.width)
}

type columns struct {
	indent, title, age, prompts, size int
}

func (m Model) columns(inner int) columns {
	c := columns{age: 9, prompts: 8, size: 10}
	if m.grouped {
		c.indent = 2
	}
	if inner < 64 {
		c.prompts = 0
	}
	if inner < 48 {
		c.age = 0
	}
	c.title = max(inner-2-c.indent-2-c.age-c.prompts-c.size, 4)
	return c
}

func (m Model) listPanel(w, h int) string {
	l := m.layout()
	inner := w - 2
	c := m.columns(inner)
	lines := make([]string, 0, h-2)

	hdr := strings.Repeat(" ", 2+c.indent+2) + padRight("SESSION", c.title)
	if c.age > 0 {
		hdr += padLeft("ACTIVE", c.age)
	}
	if c.prompts > 0 {
		hdr += padLeft("PROMPTS", c.prompts)
	}
	hdr += padLeft("SIZE ", c.size)
	lines = append(lines, m.st.colHeader.Render(fit(hdr, inner)))

	if len(m.rows) == 0 {
		msg := "No Claude Code sessions found"
		if m.filter.Value() != "" {
			msg = fmt.Sprintf("No sessions match %q", format.Printable(m.filter.Value()))
		}
		empty := lipgloss.Place(inner, max(l.listH, 1), lipgloss.Center, lipgloss.Center, m.st.muted.Render(format.Truncate(msg, inner)))
		lines = append(lines, strings.Split(empty, "\n")...)
	} else {
		end := min(m.offset+l.listH, len(m.rows))
		for i := m.offset; i < end; i++ {
			lines = append(lines, m.renderRow(m.rows[i], i == m.cursor, inner, c))
		}
	}

	info := ""
	if len(m.rows) > l.listH && l.listH > 0 {
		pct := 100
		if len(m.rows) > l.listH {
			pct = m.offset * 100 / (len(m.rows) - l.listH)
		}
		info = fmt.Sprintf("%d%%", pct)
	}
	return m.panel("Sessions", lines, w, h, !m.detail, info)
}

func (m Model) renderRow(r row, cursor bool, w int, c columns) string {
	paint := func(st lipgloss.Style, s string) string {
		if cursor {
			st = st.Background(m.st.p.cursorBg)
		}
		return st.Render(s)
	}
	bar := paint(m.st.faint, "  ")
	if cursor {
		bar = paint(m.st.accent.Bold(true), "▌ ")
	}

	if r.kind == rowGroup {
		g := r.group
		arrow := "▾ "
		if m.collapsed[g.key] && m.filter.Value() == "" {
			arrow = "▸ "
		}
		sel, eligible := 0, 0
		for _, s := range g.sessions {
			if !s.Live {
				eligible++
				if m.selected[s.Key()] {
					sel++
				}
			}
		}
		var meta []string
		if sel > 0 {
			meta = append(meta, paint(m.st.accent, fmt.Sprintf("%d/%d selected", sel, eligible)))
		}
		if g.live > 0 {
			meta = append(meta, paint(m.st.green, fmt.Sprintf("● %d in use", g.live)))
		}
		if !g.exists {
			meta = append(meta, paint(m.st.red, "folder gone"))
		}
		meta = append(meta, paint(m.st.muted, fmt.Sprintf("%s · %s", format.Plural(len(g.sessions), "session", "sessions"), format.Bytes(g.size))))
		right := strings.Join(meta, paint(m.st.faint, "  ")) + paint(m.st.faint, " ")
		labelW := max(w-2-2-lipgloss.Width(right)-1, 4)
		label := format.TruncateLeft(format.Printable(g.label), labelW)
		if g.guessed {
			label = format.TruncateLeft(format.Printable(g.label)+"?", labelW)
		}
		left := bar + paint(m.st.violet, arrow) + paint(m.st.groupTitle, label)
		return spreadPainted(left, right, w, paint)
	}

	s := r.sess
	check := paint(m.st.faint, "○ ")
	titleSt := m.st.text
	switch {
	case s.Live:
		check = paint(m.st.faint, "◌ ")
		titleSt = m.st.muted
	case m.selected[s.Key()]:
		check = paint(m.st.accent.Bold(true), "● ")
		titleSt = m.st.accent
	}
	if s.TitleSource == session.TitlePrompt || s.TitleSource == session.TitleCommand || s.TitleSource == session.TitleNone {
		titleSt = titleSt.Italic(true)
	}

	var badge string
	badgeW := 0
	if s.Live {
		badge = " " + m.st.badgeLive.Render("IN USE")
		badgeW = lipgloss.Width(badge)
	}
	title := format.Printable(s.Title)
	if !m.grouped {
		folder := filepath.Base(format.Printable(s.CWD))
		title = folder + " › " + title
	}
	titleText := padRight(format.Truncate(title, max(c.title-badgeW-1, 1)), c.title-badgeW)

	out := bar + paint(m.st.faint, strings.Repeat(" ", c.indent)) + check + paint(titleSt, titleText)
	if badge != "" {
		out += badge
	}
	if c.age > 0 {
		out += paint(m.st.muted, padLeft(format.Ago(s.LastActive, m.opt.Now()), c.age))
	}
	if c.prompts > 0 {
		out += paint(m.st.muted, padLeft(fmt.Sprint(s.Prompts), c.prompts))
	}
	sizeSt := m.st.muted
	if s.Size >= 100<<20 {
		sizeSt = m.st.yellow
	}
	out += paint(sizeSt, padLeft(format.Bytes(s.Size)+" ", c.size))
	return fitPainted(out, w, paint)
}

func (m Model) detailPanel(w, h int) string {
	inner := w - 4 // borders + 1 cell padding each side
	var lines []string
	r, ok := m.current()
	switch {
	case !ok:
		lines = []string{"", m.st.faint.Render(format.Truncate("Nothing to show", inner))}
	case r.kind == rowGroup:
		lines = m.groupDetail(r.group, inner)
	default:
		lines = m.sessionDetail(r.sess, inner)
	}
	padded := make([]string, len(lines))
	for i, ln := range lines {
		padded[i] = " " + ln
	}
	title := "Details"
	if ok && r.kind == rowGroup {
		title = "Folder"
	} else if ok {
		title = "Session"
	}
	return m.panel(title, padded, w, h, m.detail, "")
}

func (m Model) field(label, value string, w int) []string {
	lw := 10
	vw := max(w-lw, 4)
	wrapped := wrap(value, vw)
	out := make([]string, 0, len(wrapped))
	for i, ln := range wrapped {
		l := strings.Repeat(" ", lw)
		if i == 0 {
			l = m.st.muted.Render(padRight(label, lw))
		}
		out = append(out, l+ln)
	}
	return out
}

func (m Model) sessionDetail(s *session.Session, w int) []string {
	now := m.opt.Now()
	var out []string
	titleLines := wrap(m.st.bold.Render(format.Printable(s.Title)), w)
	out = append(out, limitLines(titleLines, 3)...)

	var badges []string
	if s.Live {
		badges = append(badges, m.st.badgeLive.Render("IN USE"))
	}
	if !s.CWDExists {
		badges = append(badges, m.st.badgeGone.Render("FOLDER GONE"))
	}
	if s.SharedID {
		badges = append(badges, m.st.badgeWarn.Render("SHARED ID"))
	}
	if m.selected[s.Key()] {
		badges = append(badges, m.st.accent.Bold(true).Render("● selected"))
	}
	if len(badges) > 0 {
		out = append(out, format.Truncate(strings.Join(badges, " "), w))
	}
	if s.Live {
		out = append(out, m.st.green.Render(format.Truncate(s.LiveReason, w)))
	}
	out = append(out, "")

	folder := format.TildePath(format.Printable(s.CWD), m.opt.Home)
	if s.CWDGuessed {
		folder += m.st.faint.Render(" (guessed)")
	}
	out = append(out, m.field("Folder", folder, w)...)
	if s.CWDExists {
		out = append(out, m.field("", m.st.green.Render("✓ still on disk"), w)...)
	} else {
		out = append(out, m.field("", m.st.red.Render("✗ no longer on disk"), w)...)
	}
	if s.GitBranch != "" {
		out = append(out, m.field("Branch", m.st.violet.Render(format.Printable(s.GitBranch)), w)...)
	}
	out = append(out, m.field("Started", fmt.Sprintf("%s %s", format.Timestamp(s.Started), m.st.faint.Render(format.Ago(s.Started, now))), w)...)
	out = append(out, m.field("Active", fmt.Sprintf("%s %s", format.Timestamp(s.LastActive), m.st.faint.Render(format.Ago(s.LastActive, now))), w)...)
	out = append(out, m.field("Prompts", fmt.Sprintf("%d %s", s.Prompts, m.st.faint.Render(fmt.Sprintf("· %d messages", s.Messages))), w)...)
	out = append(out, m.field("Size", fmt.Sprintf("%s %s", format.Bytes(s.Size), m.st.faint.Render("· "+format.Plural(s.FileCount(), "file", "files"))), w)...)
	if s.Version != "" {
		out = append(out, m.field("Version", m.st.muted.Render(format.Printable(s.Version)), w)...)
	}
	if w-10 >= len(s.ID) {
		out = append(out, m.field("ID", m.st.faint.Render(s.ID), w)...)
	} else {
		out = append(out, m.st.muted.Render("ID"), m.st.faint.Render(format.Truncate(s.ID, w)))
	}

	if s.FirstPrompt != "" {
		out = append(out, "", m.st.accent.Render("First prompt"))
		out = append(out, limitLines(wrap(m.st.text.Render(format.Printable(s.FirstPrompt)), w), 4)...)
	}
	if s.LastPrompt != "" && s.LastPrompt != s.FirstPrompt {
		out = append(out, "", m.st.accent.Render("Last prompt"))
		out = append(out, limitLines(wrap(m.st.text.Render(format.Printable(s.LastPrompt)), w), 3)...)
	}

	out = append(out, "", m.st.accent.Render("Moves to Trash"))
	for _, a := range s.Artifacts {
		label := a.Kind.String()
		size := format.Bytes(a.Size)
		if a.Kind.IsDir() {
			size += m.st.faint.Render(fmt.Sprintf(" · %d", a.Files))
		}
		out = append(out, spread(m.st.muted.Render("  "+label), size, w))
	}
	if s.SharedID {
		out = append(out, wrap(m.st.faint.Render("Same ID exists in another folder, so shared file history is kept."), w)...)
	}
	return out
}

func (m Model) groupDetail(g *group, w int) []string {
	now := m.opt.Now()
	var out []string
	out = append(out, limitLines(wrap(m.st.groupTitle.Render(format.Printable(g.label)), w), 3)...)
	if !g.exists {
		out = append(out, m.st.badgeGone.Render("FOLDER GONE"))
	}
	out = append(out, "")
	out = append(out, m.field("Sessions", fmt.Sprint(len(g.sessions)), w)...)
	out = append(out, m.field("Size", format.Bytes(g.size), w)...)
	out = append(out, m.field("Prompts", fmt.Sprint(g.prompts), w)...)
	out = append(out, m.field("Newest", format.Ago(g.last, now), w)...)
	out = append(out, m.field("Oldest", format.Ago(g.first, now), w)...)
	if g.live > 0 {
		out = append(out, m.field("In use", m.st.green.Render(fmt.Sprintf("%d (protected)", g.live)), w)...)
	}
	out = append(out, m.field("Stored in", m.st.faint.Render(format.Printable(g.key)), w)...)
	out = append(out, "", m.st.accent.Render("Recent sessions"))
	for i, s := range g.sessions {
		if i == 8 {
			out = append(out, m.st.faint.Render(fmt.Sprintf("  … and %d more", len(g.sessions)-i)))
			break
		}
		t := "  " + format.Truncate(format.Printable(s.Title), max(w-12, 4))
		out = append(out, spread(m.st.text.Render(t), m.st.faint.Render(format.Ago(s.LastActive, now)), w))
	}
	out = append(out, "")
	out = append(out, wrap(m.st.faint.Render("space selects every session in this folder · ←/→ collapse or expand"), w)...)
	return out
}

func (m Model) modalWidth() int { return max(min(70, m.width-4), 30) }

func (m Model) confirmView() string {
	c := m.confirm
	w := m.modalWidth()
	inner := w - 6
	verb := "Move"
	if m.opt.DryRun {
		verb = "Preview moving"
	}
	head := m.st.yellow.Bold(true).Render("⚠ ") +
		m.st.bold.Render(fmt.Sprintf("%s %s to the Trash?", verb, format.Plural(len(c.targets), "session", "sessions")))
	lines := []string{head}
	sub := fmt.Sprintf("%s · %s", format.Bytes(c.size), format.Plural(c.files, "file", "files"))
	if c.scope != "" && c.scope != "selected" {
		sub += " · " + c.scope
	}
	lines = append(lines, m.st.muted.Render(format.Truncate(format.Printable(sub), inner)), "")

	maxItems := max(min(len(c.targets), m.height-22), 1)
	for i, s := range c.targets {
		if i == maxItems {
			lines = append(lines, m.st.faint.Render(fmt.Sprintf("  … and %d more", len(c.targets)-i)))
			break
		}
		size := m.st.muted.Render(format.Bytes(s.Size))
		folder := m.st.faint.Render(" " + format.TruncateLeft(format.TildePath(format.Printable(s.CWD), m.opt.Home), 22))
		tw := max(inner-lipgloss.Width(size)-lipgloss.Width(folder)-4, 4)
		left := m.st.accent.Render("• ") + m.st.text.Render(format.Truncate(format.Printable(s.Title), tw)) + folder
		lines = append(lines, spread(left, size, inner))
	}
	lines = append(lines, "")
	if c.live > 0 {
		lines = append(lines, wrap(m.st.green.Render(fmt.Sprintf("● %s in use and will be left alone.", format.Plural(c.live, "session is", "sessions are"))), inner)...)
	}
	if c.hidden > 0 {
		lines = append(lines, wrap(m.st.yellow.Render(fmt.Sprintf("! %d of these %s hidden by the current filter.", c.hidden, map[bool]string{true: "is", false: "are"}[c.hidden == 1])), inner)...)
	}
	if m.opt.DryRun {
		lines = append(lines, wrap(m.st.muted.Render("Dry run: nothing will be moved. You'll see exactly what would happen."), inner)...)
	} else {
		lines = append(lines, wrap(m.st.muted.Render("Nothing is deleted. Each file is checked again, then moved to the macOS Trash and recorded in the log."), inner)...)
	}
	lines = append(lines, "")

	cancel := m.st.buttonIdle.Render("Cancel")
	ok := m.st.buttonIdle.Render("Move to Trash")
	if m.opt.DryRun {
		ok = m.st.buttonIdle.Render("Run preview")
	}
	if c.focus == 0 {
		cancel = m.st.buttonFocus.Render("Cancel")
	} else if m.opt.DryRun {
		ok = m.st.buttonFocus.Render("Run preview")
	} else {
		ok = m.st.buttonDanger.Render("Move to Trash")
	}
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, cancel, "   ", ok)
	lines = append(lines, lipgloss.PlaceHorizontal(inner, lipgloss.Center, buttons), "")
	lines = append(lines, lipgloss.PlaceHorizontal(inner, lipgloss.Center,
		m.keyHint("y", "confirm")+"  "+m.keyHint("n/esc", "cancel")+"  "+m.keyHint("←/→", "switch")))
	return m.box(strings.Join(lines, "\n"), m.st.p.accent, w)
}

func (m Model) trashingView() string {
	w := m.modalWidth()
	inner := w - 6
	t := m.trash
	verb := "Moving sessions to the Trash"
	if m.opt.DryRun {
		verb = "Checking what would be moved"
	}
	pct := 0.0
	if t.total > 0 {
		pct = float64(t.done) / float64(t.total)
	}
	bar := m.bar
	bar.SetWidth(max(inner-10, 10))
	lines := []string{
		m.spin.View() + " " + m.st.bold.Render(verb+"…"),
		"",
		bar.ViewAs(pct) + m.st.muted.Render(fmt.Sprintf("  %d/%d", t.done, t.total)),
	}
	if t.current != "" {
		lines = append(lines, m.st.faint.Render(format.Truncate("Last: "+format.Printable(t.current), inner)))
	} else {
		lines = append(lines, "")
	}
	lines = append(lines, "")
	if t.cancelling {
		lines = append(lines, m.st.yellow.Render(format.Truncate("Stopping after the current session…", inner)))
	} else {
		lines = append(lines, m.keyHint("ctrl+c", "stop after the current session"))
	}
	return m.box(strings.Join(lines, "\n"), m.st.p.violet, w)
}

func (m Model) resultView() string {
	rep := m.report
	w := m.modalWidth()
	inner := w - 6
	trashed := rep.Count(cleaner.StatusTrashed)
	var size int64
	items := 0
	for _, o := range rep.Outcomes {
		for _, it := range o.Items {
			size += it.Size
			items++
		}
	}
	var lines []string
	if m.opt.DryRun {
		n := 0
		for _, o := range rep.Outcomes {
			if o.Status == cleaner.StatusDryRun {
				n++
			}
		}
		lines = append(lines,
			m.st.yellow.Bold(true).Render("◇ ")+m.st.bold.Render(fmt.Sprintf("Dry run: %s would be moved", format.Plural(n, "session", "sessions"))),
			m.st.muted.Render(fmt.Sprintf("%s in %s · nothing was changed", format.Bytes(size), format.Plural(items, "item", "items"))),
		)
	} else if trashed > 0 {
		lines = append(lines,
			m.st.green.Bold(true).Render("✓ ")+m.st.bold.Render(fmt.Sprintf("Moved %s to the Trash", format.Plural(trashed, "session", "sessions"))),
			m.st.muted.Render(fmt.Sprintf("%s freed from %s · %s", format.Bytes(size), format.TildePath(m.opt.Root, m.opt.Home), format.Plural(items, "item", "items"))),
		)
	} else {
		lines = append(lines, m.st.yellow.Bold(true).Render("! ")+m.st.bold.Render("Nothing was moved"))
	}

	var failed, skipped []string
	for _, o := range rep.Outcomes {
		reason := ""
		if o.Err != nil {
			reason = format.Printable(o.Err.Error())
		}
		line := "  • " + format.Printable(o.Session.Title)
		switch o.Status {
		case cleaner.StatusFailed:
			failed = append(failed, line+": "+reason)
		case cleaner.StatusSkipped:
			skipped = append(skipped, line+": "+reason)
		}
	}
	addList := func(head string, st lipgloss.Style, list []string) {
		if len(list) == 0 {
			return
		}
		lines = append(lines, "", st.Bold(true).Render(head))
		for i, l := range list {
			if i == 5 {
				lines = append(lines, m.st.faint.Render(fmt.Sprintf("  … and %d more", len(list)-i)))
				break
			}
			lines = append(lines, limitLines(wrap(m.st.muted.Render(l), inner), 2)...)
		}
	}
	if m.opt.DryRun {
		var paths []string
		for _, o := range rep.Outcomes {
			for _, it := range o.Items {
				path := format.TildePath(format.Printable(it.Original), m.opt.Home)
				paths = append(paths, padRight("  "+it.Kind.String(), 15)+format.TruncateLeft(path, max(inner-15, 8)))
			}
		}
		if len(paths) > 0 {
			lines = append(lines, "", m.st.accent.Render("Would move"))
			limit := max(min(len(paths), m.height-20), 1)
			for i, p := range paths {
				if i == limit {
					lines = append(lines, m.st.faint.Render(fmt.Sprintf("  … and %d more", len(paths)-i)))
					break
				}
				lines = append(lines, m.st.muted.Render(p))
			}
		}
	}
	addList(fmt.Sprintf("✗ %d failed, left in place", len(failed)), m.st.red, failed)
	addList(fmt.Sprintf("○ %d skipped", len(skipped)), m.st.yellow, skipped)

	if !m.opt.DryRun && trashed > 0 {
		lines = append(lines, "")
		if rep.JournalErr != nil {
			lines = append(lines, wrap(m.st.yellow.Render("Log not written: "+format.Printable(rep.JournalErr.Error())), inner)...)
		} else if m.opt.Journal != "" {
			lines = append(lines, wrap(m.st.faint.Render("Logged to "+format.TildePath(m.opt.Journal, m.opt.Home)), inner)...)
		}
		lines = append(lines, wrap(m.st.faint.Render("Changed your mind? Drag the items back out of the Trash; the log lists each original path."), inner)...)
	}
	lines = append(lines, "", lipgloss.PlaceHorizontal(inner, lipgloss.Center, m.st.buttonFocus.Render("OK")))
	border := m.st.p.green
	if len(failed) > 0 {
		border = m.st.p.red
	} else if m.opt.DryRun || trashed == 0 {
		border = m.st.p.yellow
	}
	return m.box(strings.Join(lines, "\n"), border, w)
}

func (m Model) helpView() string {
	w := max(min(84, m.width-4), 30)
	inner := w - 6
	hm := m.help
	hm.SetWidth(inner)
	lines := []string{
		m.logo() + m.st.muted.Render("  keys"),
		"",
		hm.FullHelpView(m.keys.FullHelp()),
		"",
		m.st.accent.Render("Safety"),
	}
	for _, s := range []string{
		"Nothing is deleted permanently. Files go to the macOS Trash.",
		"Sessions open in Claude Code, or written to in the last two minutes, can't be selected or moved.",
		"Right before each move, every path is checked again: it must be a known session file inside your Claude folder, and no symlinks are followed.",
		"The transcript moves last, so a failure leaves the session listed and you can retry.",
		"Every move is recorded in the log so you can find things again.",
	} {
		lines = append(lines, wrap(m.st.muted.Render("• "+s), inner)...)
	}
	lines = append(lines, "", m.keyHint("esc", "close"))
	return m.box(strings.Join(lines, "\n"), m.st.p.violet, w)
}

func (m Model) keyHint(k, desc string) string {
	return m.st.key.Render(k) + " " + m.st.keyDesc.Render(desc)
}

func (m Model) box(content string, border color.Color, w int) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(1, 2).
		Width(w).
		Render(content)
}

func (m Model) panel(title string, lines []string, w, h int, focused bool, info string) string {
	if w < 4 || h < 2 {
		return ""
	}
	bc := m.st.faint.Foreground(m.st.p.border)
	tc := m.st.muted
	if focused {
		bc = m.st.accent
		tc = m.st.accent.Bold(true)
	}
	inner := w - 2
	t := format.Truncate(" "+title+" ", max(inner-2, 0))
	top := bc.Render("╭─") + tc.Render(t) + bc.Render(strings.Repeat("─", max(inner-1-lipgloss.Width(t), 0))+"╮")
	bottomFill := inner
	infoText := ""
	if info != "" && inner > len(info)+4 {
		infoText = m.st.faint.Render(" " + info + " ")
		bottomFill = inner - lipgloss.Width(infoText) - 1
	}
	bottom := bc.Render("╰" + strings.Repeat("─", max(bottomFill, 0)))
	if infoText != "" {
		bottom += infoText + bc.Render("─")
	}
	bottom += bc.Render("╯")

	out := make([]string, 0, h)
	out = append(out, top)
	side := bc.Render("│")
	for i := 0; i < h-2; i++ {
		ln := ""
		if i < len(lines) {
			ln = lines[i]
		}
		out = append(out, side+fit(ln, inner)+side)
	}
	out = append(out, bottom)
	return strings.Join(out, "\n")
}

func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "")
	}
	return padRight(s, w)
}

func padRight(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func padLeft(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return padRight(format.Truncate(left, w), w)
	}
	return left + strings.Repeat(" ", gap) + right
}

func spreadPainted(left, right string, w int, paint func(lipgloss.Style, string) string) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return fitPainted(left, w, paint)
	}
	return left + paint(lipgloss.NewStyle(), strings.Repeat(" ", gap)) + right
}

func fitPainted(s string, w int, paint func(lipgloss.Style, string) string) string {
	if lipgloss.Width(s) > w {
		return ansi.Truncate(s, w, "")
	}
	if d := w - lipgloss.Width(s); d > 0 {
		s += paint(lipgloss.NewStyle(), strings.Repeat(" ", d))
	}
	return s
}

func wrap(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	return strings.Split(lipgloss.Wrap(s, w, " "), "\n")
}

func limitLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	out := append([]string{}, lines[:n]...)
	out[n-1] = ansi.Truncate(out[n-1], max(lipgloss.Width(out[n-1])-1, 1), "") + "…"
	return out
}
