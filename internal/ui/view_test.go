package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/claudeclean/internal/cleaner"
	"github.com/balintb/claudeclean/internal/session"
)

func ansiStrip(s string) string { return ansi.Strip(s) }

func assertFits(t *testing.T, frame string, w, h int, label string) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if frame != "" && len(lines) > h {
		t.Fatalf("%s at %dx%d: %d lines", label, w, h, len(lines))
	}
	for i, ln := range lines {
		if lw := lipgloss.Width(ln); lw > w {
			t.Fatalf("%s at %dx%d: line %d is %d wide:\n%q", label, w, h, i, lw, ansiStrip(ln))
		}
	}
	if strings.ContainsRune(frame, '\t') {
		t.Fatalf("%s at %dx%d: frame contains a tab", label, w, h)
	}
}

func nastySessions() []*session.Session {
	long := strings.Repeat("very long title ", 30)
	return []*session.Session{
		sess(1, "-a", "/Users/me/code/a", "日本語のセッションタイトル 🎉 with emoji and wide runes"),
		sess(2, "-a", "/Users/me/code/a", long, live),
		sess(3, "-b", "/Users/me/"+strings.Repeat("deep/", 30)+"folder", "x", gone),
		sess(4, "-c", "/c", "evil \x1b]0;pwned\x07 \x1b[2J title", func(s *session.Session) {
			s.CWD = "/tmp/\x1b[31mred"
			s.GitBranch = "main\x1b[0m"
			s.FirstPrompt = "prompt\x1b[H\x1b[2Jcleared"
			s.LastPrompt = strings.Repeat("ç", 5000)
			s.SharedID = true
			s.CWDGuessed = true
			s.TitleSource = session.TitlePrompt
		}),
		sess(5, "-d", "", "", func(s *session.Session) { s.TitleSource = session.TitleNone }),
	}
}

func modesFor(t *testing.T, w, h int) map[string]Model {
	t.Helper()
	out := map[string]Model{}
	hh := newHarness(t, nastySessions())
	hh.send(tea.WindowSizeMsg{Width: w, Height: h})
	out["browse"] = hh.m

	hh.press("a")
	out["browse-selected"] = hh.m

	hh.press("d")
	out["confirm"] = hh.m
	hh.press("right")
	out["confirm-danger"] = hh.m
	hh.press("esc")

	hh.press("?")
	out["help"] = hh.m
	hh.press("esc")

	hh.press("tab")
	out["detail"] = hh.m
	hh.press("tab", "v")
	out["flat"] = hh.m
	hh.press("v", "/")
	hh.typeText("zzz-no-match")
	out["filter-empty"] = hh.m
	hh.press("esc")

	tr := hh.m
	tr.mode = modeTrashing
	tr.trash = trashState{total: 3, done: 1, current: "evil \x1b[2J"}
	out["trashing"] = tr
	tr.trash.cancelling = true
	out["cancelling"] = tr

	res := hh.m
	res.mode = modeResult
	res.report = cleaner.Report{Outcomes: []cleaner.Outcome{
		{Session: hh.m.all[0], Status: cleaner.StatusTrashed, Items: []cleaner.Item{{Original: "/a/b", Size: 10}}},
		{Session: hh.m.all[1], Status: cleaner.StatusSkipped, Err: fmt.Errorf("in use")},
		{Session: hh.m.all[2], Status: cleaner.StatusFailed, Err: fmt.Errorf("boom \x1b[2J %s", strings.Repeat("long reason ", 20))},
	}, JournalErr: fmt.Errorf("disk full")}
	out["result"] = res
	dry := res
	dry.opt.DryRun = true
	out["result-dry"] = dry

	ld := New(hh.m.opt)
	ld.width, ld.height = w, h
	out["loading"] = ld
	er := ld
	er.mode = modeError
	er.loadErr = fmt.Errorf("could not read \x1b[2J %s", strings.Repeat("reason ", 40))
	out["error"] = er
	return out
}

func TestRenderFitsEveryTerminalSize(t *testing.T) {
	sizes := [][2]int{{1, 1}, {10, 3}, {39, 11}, {40, 12}, {41, 13}, {47, 15}, {48, 16}, {63, 20}, {64, 20}, {80, 24}, {99, 30}, {100, 30}, {101, 31}, {120, 40}, {160, 50}, {250, 70}}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		for name, m := range modesFor(t, w, h) {
			assertFits(t, m.render(), w, h, name)
		}
	}
}

func TestRenderFillsTheScreenExactly(t *testing.T) {
	for _, sz := range [][2]int{{40, 12}, {80, 24}, {120, 40}} {
		w, h := sz[0], sz[1]
		m := modesFor(t, w, h)["browse"]
		lines := strings.Split(m.render(), "\n")
		if len(lines) != h {
			t.Fatalf("%dx%d: %d lines, want %d", w, h, len(lines), h)
		}
		for i, ln := range lines {
			if lipgloss.Width(ln) != w {
				t.Fatalf("%dx%d: line %d is %d wide: %q", w, h, i, lipgloss.Width(ln), ansiStrip(ln))
			}
		}
	}
}

func TestRenderNeverEmitsTranscriptEscapes(t *testing.T) {
	for name, m := range modesFor(t, 140, 45) {
		frame := m.render()
		for _, bad := range []string{"\x1b]0;pwned", "\x1b[2J", "\x1b[H", "\x07"} {
			if strings.Contains(frame, bad) {
				t.Fatalf("%s: frame contains %q from session data", name, bad)
			}
		}
	}
}

func TestRenderShowsKeyInformation(t *testing.T) {
	h := newHarness(t, sample())
	h.moveTo("Fix login bug")
	view := ansiStrip(h.m.render())
	for _, want := range []string{
		"claudeclean", "5 sessions in 3 folders", "1 in use",
		"/work/api", "/work/web", "folder gone",
		"Fix login bug", "IN USE", "SESSION", "ACTIVE", "SIZE",
		"First prompt", "prompt for Fix login bug", "Moves to Trash",
		fmt.Sprint(sample()[0].ID),
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
	if t.Failed() {
		t.Log("\n" + view)
	}
}

func TestViewSetsTerminalOptions(t *testing.T) {
	h := newHarness(t, sample())
	v := h.m.View()
	if !v.AltScreen || v.MouseMode != tea.MouseModeCellMotion || v.WindowTitle != "claudeclean" {
		t.Fatalf("view options: %+v", v)
	}
	h.m.mode = modeTrashing
	h.m.trash = trashState{total: 4, done: 1}
	if pb := h.m.View().ProgressBar; pb == nil || pb.Value != 25 {
		t.Fatalf("progress bar %+v", pb)
	}
}

func TestZeroSizeRendersNothing(t *testing.T) {
	m := New(Options{Now: time.Now})
	if m.render() != "" {
		t.Fatal("render before WindowSizeMsg should be empty")
	}
}

func TestBuildRows(t *testing.T) {
	ss := sample()
	rows := buildRows(ss, "", sortRecent, true, map[string]bool{"-work-web": true}, "")
	var got []string
	for _, r := range rows {
		if r.kind == rowGroup {
			got = append(got, "["+r.group.key+"]")
		} else {
			got = append(got, r.sess.Title)
		}
	}
	want := "[-work-api],Fix login bug,Add rate limiter,[-work-web],[-work-old],Legacy migration"
	if strings.Join(got, ",") != want {
		t.Fatalf("rows:\n got %s\nwant %s", strings.Join(got, ","), want)
	}
	rows = buildRows(ss, "dark", sortRecent, true, map[string]bool{"-work-web": true}, "")
	if len(rows) != 2 || rows[1].sess.Title != "Dark mode toggle" {
		t.Fatalf("filtered rows: %d", len(rows))
	}
	g := rows[0].group
	if len(g.sessions) != 1 || g.size != ss[4].Size {
		t.Fatalf("group aggregates: %+v", g)
	}
	all := buildGroups(ss, "")
	for _, g := range all {
		if g.key == "-work-api" && (g.live != 1 || g.prompts != 3 || len(g.sessions) != 2) {
			t.Fatalf("api group: %+v", g)
		}
	}
}

func TestGroupSorting(t *testing.T) {
	ss := sample()
	order := func(mode sortMode) string {
		var keys []string
		for _, r := range buildRows(ss, "", mode, true, nil, "") {
			if r.kind == rowGroup {
				keys = append(keys, r.group.key)
			}
		}
		return strings.Join(keys, ",")
	}
	if got := order(sortRecent); got != "-work-api,-work-web,-work-old" {
		t.Errorf("recent: %s", got)
	}
	if got := order(sortOldest); got != "-work-web,-work-old,-work-api" {
		t.Errorf("oldest: %s", got)
	}
	if got := order(sortLargest); got != "-work-web,-work-old,-work-api" {
		t.Errorf("largest: %s", got)
	}
	if got := order(sortPrompts); got != "-work-web,-work-old,-work-api" {
		t.Errorf("prompts: %s", got)
	}
}

func TestMatches(t *testing.T) {
	s := sess(1, "-p", "/home/me/proj", "Title Here", func(s *session.Session) { s.GitBranch = "feature/x" })
	for q, want := range map[string]bool{
		"":                true,
		"title":           true,
		"TITLE here":      true,
		"~/proj":          true,
		"feature/x":       true,
		s.ID[:8]:          true,
		"prompt for":      true,
		"title missing":   false,
		"nothing-like-it": false,
	} {
		if got := matches(s, filterTerms(q), "/home/me"); got != want {
			t.Errorf("matches(%q) = %v", q, got)
		}
	}
}

func TestSortModeStrings(t *testing.T) {
	for m := sortRecent; m < sortModeCount; m++ {
		if m.String() == "?" {
			t.Errorf("mode %d has no label", m)
		}
	}
	if sortModeCount.String() != "?" {
		t.Error("out of range label")
	}
}
