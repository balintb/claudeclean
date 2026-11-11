package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

type palette struct {
	accent, accentHi, violet  color.Color
	text, muted, faint        color.Color
	border, cursorBg, panelBg color.Color
	green, red, yellow        color.Color
	onBadge                   color.Color
}

func newPalette(dark bool) palette {
	if dark {
		return palette{
			accent:   lipgloss.Color("#D97757"),
			accentHi: lipgloss.Color("#F4A582"),
			violet:   lipgloss.Color("#A99BF0"),
			text:     lipgloss.Color("#ECE8E1"),
			muted:    lipgloss.Color("#A39E93"),
			faint:    lipgloss.Color("#6B665E"),
			border:   lipgloss.Color("#3D3833"),
			cursorBg: lipgloss.Color("#33292A"),
			panelBg:  lipgloss.Color("#1E1A18"),
			green:    lipgloss.Color("#8FD3A0"),
			red:      lipgloss.Color("#F28B82"),
			yellow:   lipgloss.Color("#F2C46D"),
			onBadge:  lipgloss.Color("#1A1614"),
		}
	}
	return palette{
		accent:   lipgloss.Color("#C15F3C"),
		accentHi: lipgloss.Color("#D97757"),
		violet:   lipgloss.Color("#6B5BD2"),
		text:     lipgloss.Color("#2A2622"),
		muted:    lipgloss.Color("#6E675E"),
		faint:    lipgloss.Color("#A8A096"),
		border:   lipgloss.Color("#D9D2C7"),
		cursorBg: lipgloss.Color("#F6E8DF"),
		panelBg:  lipgloss.Color("#FBF8F4"),
		green:    lipgloss.Color("#2E8B57"),
		red:      lipgloss.Color("#C0392B"),
		yellow:   lipgloss.Color("#B7791F"),
		onBadge:  lipgloss.Color("#FFFFFF"),
	}
}

type styles struct {
	p palette

	text, muted, faint, accent, violet lipgloss.Style
	bold                               lipgloss.Style
	green, red, yellow                 lipgloss.Style

	badgeLive, badgeGone, badgeDry, badgeWarn lipgloss.Style

	cursorBar lipgloss.Style
	cursorRow lipgloss.Style

	groupTitle lipgloss.Style
	colHeader  lipgloss.Style

	key, keyDesc, keySep lipgloss.Style

	buttonIdle, buttonFocus, buttonDanger lipgloss.Style
}

func newStyles(dark bool) styles {
	p := newPalette(dark)
	s := styles{p: p}
	fg := func(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	s.text = fg(p.text)
	s.muted = fg(p.muted)
	s.faint = fg(p.faint)
	s.accent = fg(p.accent)
	s.violet = fg(p.violet)
	s.bold = fg(p.text).Bold(true)
	s.green = fg(p.green)
	s.red = fg(p.red)
	s.yellow = fg(p.yellow)

	badge := lipgloss.NewStyle().Foreground(p.onBadge).Bold(true).Padding(0, 1)
	s.badgeLive = badge.Background(p.green)
	s.badgeGone = badge.Background(p.red)
	s.badgeDry = badge.Background(p.yellow)
	s.badgeWarn = badge.Background(p.violet)

	s.cursorBar = fg(p.accent).Background(p.cursorBg)
	s.cursorRow = lipgloss.NewStyle().Background(p.cursorBg)

	s.groupTitle = fg(p.violet).Bold(true)
	s.colHeader = fg(p.faint)

	s.key = fg(p.accent).Bold(true)
	s.keyDesc = fg(p.muted)
	s.keySep = fg(p.faint)

	btn := lipgloss.NewStyle().Padding(0, 2)
	s.buttonIdle = btn.Foreground(p.muted).Background(p.border)
	s.buttonFocus = btn.Foreground(p.onBadge).Background(p.accent).Bold(true)
	s.buttonDanger = btn.Foreground(p.onBadge).Background(p.red).Bold(true)
	return s
}

func gradient(text string, from, to color.Color, bold bool) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	cols := lipgloss.Blend1D(max(len(runes), 2), from, to)
	var b strings.Builder
	for i, r := range runes {
		st := lipgloss.NewStyle().Foreground(cols[i])
		if bold {
			st = st.Bold(true)
		}
		b.WriteString(st.Render(string(r)))
	}
	return b.String()
}
