package ui

import (
	"sort"
	"strings"
	"time"

	"github.com/balintb/claudeclean/internal/format"
	"github.com/balintb/claudeclean/internal/session"
)

type sortMode int

const (
	sortRecent sortMode = iota
	sortOldest
	sortLargest
	sortPrompts
	sortModeCount
)

func (s sortMode) String() string {
	switch s {
	case sortRecent:
		return "most recent"
	case sortOldest:
		return "oldest"
	case sortLargest:
		return "largest"
	case sortPrompts:
		return "most prompts"
	default:
		return "?"
	}
}

type group struct {
	key      string // project dir name
	label    string // display path
	cwd      string
	exists   bool
	guessed  bool
	sessions []*session.Session
	size     int64
	prompts  int
	last     time.Time
	first    time.Time
	live     int
}

type rowKind int

const (
	rowGroup rowKind = iota
	rowSession
)

type row struct {
	kind  rowKind
	group *group
	sess  *session.Session
}

func (r row) id() string {
	if r.kind == rowGroup {
		return "g:" + r.group.key
	}
	return "s:" + r.sess.Key()
}

func matches(s *session.Session, terms []string, home string) bool {
	if len(terms) == 0 {
		return true
	}
	hay := strings.ToLower(strings.Join([]string{
		s.Title, s.CWD, format.TildePath(s.CWD, home), s.FirstPrompt, s.LastPrompt, s.GitBranch, s.ID,
	}, "\x00"))
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

func filterTerms(q string) []string {
	return strings.Fields(strings.ToLower(q))
}

func lessSession(mode sortMode) func(a, b *session.Session) bool {
	tie := func(a, b *session.Session) bool { return a.Key() < b.Key() }
	switch mode {
	case sortOldest:
		return func(a, b *session.Session) bool {
			if !a.LastActive.Equal(b.LastActive) {
				return a.LastActive.Before(b.LastActive)
			}
			return tie(a, b)
		}
	case sortLargest:
		return func(a, b *session.Session) bool {
			if a.Size != b.Size {
				return a.Size > b.Size
			}
			return tie(a, b)
		}
	case sortPrompts:
		return func(a, b *session.Session) bool {
			if a.Prompts != b.Prompts {
				return a.Prompts > b.Prompts
			}
			return tie(a, b)
		}
	default:
		return func(a, b *session.Session) bool {
			if !a.LastActive.Equal(b.LastActive) {
				return a.LastActive.After(b.LastActive)
			}
			return tie(a, b)
		}
	}
}

func lessGroup(mode sortMode) func(a, b *group) bool {
	tie := func(a, b *group) bool { return a.label < b.label }
	switch mode {
	case sortOldest:
		return func(a, b *group) bool {
			if !a.first.Equal(b.first) {
				return a.first.Before(b.first)
			}
			return tie(a, b)
		}
	case sortLargest:
		return func(a, b *group) bool {
			if a.size != b.size {
				return a.size > b.size
			}
			return tie(a, b)
		}
	case sortPrompts:
		return func(a, b *group) bool {
			if a.prompts != b.prompts {
				return a.prompts > b.prompts
			}
			return tie(a, b)
		}
	default:
		return func(a, b *group) bool {
			if !a.last.Equal(b.last) {
				return a.last.After(b.last)
			}
			return tie(a, b)
		}
	}
}

func buildGroups(ss []*session.Session, home string) []*group {
	byKey := map[string]*group{}
	var out []*group
	for _, s := range ss {
		g := byKey[s.ProjectDir]
		if g == nil {
			g = &group{key: s.ProjectDir, cwd: s.CWD, exists: s.CWDExists, guessed: s.CWDGuessed}
			byKey[s.ProjectDir] = g
			out = append(out, g)
		}
		if (g.guessed && !s.CWDGuessed) || (!g.exists && s.CWDExists) {
			g.cwd, g.exists, g.guessed = s.CWD, s.CWDExists, s.CWDGuessed
		}
		g.sessions = append(g.sessions, s)
		g.size += s.Size
		g.prompts += s.Prompts
		if s.LastActive.After(g.last) {
			g.last = s.LastActive
		}
		if g.first.IsZero() || s.LastActive.Before(g.first) {
			g.first = s.LastActive
		}
		if s.Live {
			g.live++
		}
	}
	for _, g := range out {
		g.label = format.TildePath(g.cwd, home)
	}
	return out
}

func buildRows(all []*session.Session, query string, mode sortMode, grouped bool, collapsed map[string]bool, home string) []row {
	terms := filterTerms(query)
	var shown []*session.Session
	for _, s := range all {
		if matches(s, terms, home) {
			shown = append(shown, s)
		}
	}
	less := lessSession(mode)
	if !grouped {
		sort.SliceStable(shown, func(i, j int) bool { return less(shown[i], shown[j]) })
		rows := make([]row, len(shown))
		for i, s := range shown {
			rows[i] = row{kind: rowSession, sess: s}
		}
		return rows
	}
	groups := buildGroups(shown, home)
	gl := lessGroup(mode)
	sort.SliceStable(groups, func(i, j int) bool { return gl(groups[i], groups[j]) })
	var rows []row
	for _, g := range groups {
		sort.SliceStable(g.sessions, func(i, j int) bool { return less(g.sessions[i], g.sessions[j]) })
		rows = append(rows, row{kind: rowGroup, group: g})
		// Filtering shows matches even in collapsed folders.
		if collapsed[g.key] && len(terms) == 0 {
			continue
		}
		for _, s := range g.sessions {
			rows = append(rows, row{kind: rowSession, group: g, sess: s})
		}
	}
	return rows
}
