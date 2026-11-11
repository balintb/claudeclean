package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/balintb/claudeclean/internal/cleaner"
	"github.com/balintb/claudeclean/internal/fixture"
	"github.com/balintb/claudeclean/internal/session"
)

var testNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

type fakeDeleter struct {
	mu       sync.Mutex
	calls    [][]*session.Session
	status   cleaner.Status
	block    chan struct{} // if set, Run waits for it to close (or ctx)
	sawCtxOK bool
}

func (f *fakeDeleter) Run(ctx context.Context, ss []*session.Session, progress func(cleaner.Event)) cleaner.Report {
	f.mu.Lock()
	f.calls = append(f.calls, ss)
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			f.mu.Lock()
			f.sawCtxOK = true
			f.mu.Unlock()
		}
	}
	var rep cleaner.Report
	for i, s := range ss {
		o := cleaner.Outcome{Session: s, Status: f.status}
		if f.status == cleaner.StatusFailed {
			o.Err = errors.New("simulated failure")
		}
		if f.status == cleaner.StatusTrashed || f.status == cleaner.StatusDryRun {
			o.Items = []cleaner.Item{{Original: "/x/" + s.ID + ".jsonl", Size: s.Size}}
		}
		rep.Outcomes = append(rep.Outcomes, o)
		if progress != nil {
			progress(cleaner.Event{Done: i + 1, Total: len(ss), Outcome: o})
		}
	}
	return rep
}

func (f *fakeDeleter) Calls() [][]*session.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]*session.Session(nil), f.calls...)
}

func sess(n int, project, cwd, title string, mod ...func(*session.Session)) *session.Session {
	s := &session.Session{
		ID:          fixture.ID(n),
		ProjectDir:  project,
		CWD:         cwd,
		CWDExists:   true,
		Title:       title,
		TitleSource: session.TitleAI,
		FirstPrompt: "prompt for " + title,
		LastActive:  testNow.Add(-time.Duration(n) * time.Hour),
		Started:     testNow.Add(-time.Duration(n)*time.Hour - time.Minute),
		Size:        int64(n) * 1024,
		Prompts:     n,
		Messages:    n * 3,
		Artifacts:   []session.Artifact{{Path: "/x", Size: int64(n) * 1024, Files: 1}},
	}
	for _, f := range mod {
		f(s)
	}
	return s
}

func live(s *session.Session) { s.Live, s.LiveReason = true, "open in Claude Code (pid 1)" }
func gone(s *session.Session) { s.CWDExists = false }

func sample() []*session.Session {
	return []*session.Session{
		sess(1, "-work-api", "/work/api", "Fix login bug"),
		sess(2, "-work-api", "/work/api", "Add rate limiter", live),
		sess(3, "-work-web", "/work/web", "Polish landing page"),
		sess(4, "-work-old", "/work/old", "Legacy migration", gone),
		sess(5, "-work-web", "/work/web", "Dark mode toggle"),
	}
}

type harness struct {
	t   *testing.T
	m   Model
	del *fakeDeleter
}

func newHarness(t *testing.T, ss []*session.Session, opts ...func(*Options)) *harness {
	t.Helper()
	del := &fakeDeleter{status: cleaner.StatusTrashed}
	o := Options{
		Root:    "/home/me/.claude",
		Home:    "/home/me",
		Deleter: del,
		Journal: "/home/me/Library/Logs/claudeclean/trash-log.jsonl",
		Now:     func() time.Time { return testNow },
		Load: func(context.Context) (*session.Result, error) {
			return &session.Result{Sessions: ss}, nil
		},
	}
	for _, f := range opts {
		f(&o)
	}
	h := &harness{t: t, m: New(o), del: del}
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	h.send(h.loadMsg())
	if h.m.mode != modeBrowse {
		t.Fatalf("mode after load = %v", h.m.mode)
	}
	return h
}

func (h *harness) loadMsg() tea.Msg {
	h.t.Helper()
	cmd := h.m.load()
	return cmd()
}

func (h *harness) send(msg tea.Msg) tea.Cmd {
	h.t.Helper()
	next, cmd := h.m.Update(msg)
	h.m = next.(Model)
	return cmd
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(k)
	if len(r) != 1 {
		panic("unknown key " + k)
	}
	return tea.KeyPressMsg{Code: r[0], Text: k}
}

func (h *harness) press(keys ...string) tea.Cmd {
	h.t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		cmd = h.send(keyMsg(k))
	}
	return cmd
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (h *harness) pump() {
	h.t.Helper()
	ch := h.m.trash.ch
	if ch == nil {
		h.t.Fatal("no delete in progress")
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			h.send(msg)
		case <-timeout:
			h.t.Fatal("delete did not finish")
		}
	}
}

func (h *harness) moveTo(title string) {
	h.t.Helper()
	for i, r := range h.m.rows {
		if r.kind == rowSession && r.sess.Title == title {
			h.m.cursor = i
			h.m.clampScroll()
			return
		}
	}
	h.t.Fatalf("no row titled %q", title)
}

func (h *harness) moveToGroup(key string) {
	h.t.Helper()
	for i, r := range h.m.rows {
		if r.kind == rowGroup && r.group.key == key {
			h.m.cursor = i
			return
		}
	}
	h.t.Fatalf("no group %q", key)
}

func (h *harness) selectedTitles() []string {
	var out []string
	for _, s := range h.m.all {
		if h.m.selected[s.Key()] {
			out = append(out, s.Title)
		}
	}
	return out
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func titles(ss []*session.Session) string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Title)
	}
	return strings.Join(out, ",")
}

func TestLoadBuildsGroupedRows(t *testing.T) {
	h := newHarness(t, sample())
	groups, sessions := 0, 0
	for _, r := range h.m.rows {
		if r.kind == rowGroup {
			groups++
		} else {
			sessions++
		}
	}
	if groups != 3 || sessions != 5 {
		t.Fatalf("groups=%d sessions=%d", groups, sessions)
	}
	if h.m.rows[0].kind != rowGroup || h.m.rows[0].group.key != "-work-api" {
		t.Fatalf("first row should be the newest folder, got %+v", h.m.rows[0])
	}
}

func TestToggleSelectsAndSkipsLive(t *testing.T) {
	h := newHarness(t, sample())
	h.moveTo("Fix login bug")
	h.press("space")
	if got := h.selectedTitles(); len(got) != 1 || got[0] != "Fix login bug" {
		t.Fatalf("selected %v", got)
	}
	h.press("space") // cursor moved down after toggle; go back
	h.moveTo("Fix login bug")
	h.press("space")
	if len(h.m.selected) != 0 {
		t.Fatalf("toggle off failed: %v", h.selectedTitles())
	}
	h.moveTo("Add rate limiter")
	h.press("space")
	if len(h.m.selected) != 0 {
		t.Fatal("live session became selectable")
	}
	if !strings.Contains(h.m.toast.text, "in use") {
		t.Fatalf("toast %q", h.m.toast.text)
	}
}

func TestGroupToggleSelectsEligibleSessions(t *testing.T) {
	h := newHarness(t, sample())
	h.moveToGroup("-work-api")
	h.press("space")
	if got := strings.Join(h.selectedTitles(), ","); got != "Fix login bug" {
		t.Fatalf("selected %q (live session must be excluded)", got)
	}
	h.moveToGroup("-work-api")
	h.press("space")
	if len(h.m.selected) != 0 {
		t.Fatal("second toggle should clear the folder")
	}
}

func TestDeleteNeedsExplicitConfirmation(t *testing.T) {
	h := newHarness(t, sample())
	h.moveTo("Fix login bug")
	h.press("d")
	if h.m.mode != modeConfirm {
		t.Fatalf("mode %v", h.m.mode)
	}
	if h.m.confirm.focus != 0 {
		t.Fatal("dialog must default to Cancel")
	}
	h.press("d", "x", "space")
	if h.m.mode != modeConfirm || len(h.del.Calls()) != 0 {
		t.Fatalf("mode %v, calls %d", h.m.mode, len(h.del.Calls()))
	}
	h.press("enter")
	if h.m.mode != modeBrowse || len(h.del.Calls()) != 0 {
		t.Fatalf("enter on Cancel: mode %v, calls %d", h.m.mode, len(h.del.Calls()))
	}
	for _, k := range []string{"esc", "n", "q"} {
		h.press("d", k)
		if h.m.mode != modeBrowse || len(h.del.Calls()) != 0 {
			t.Fatalf("%s did not cancel", k)
		}
	}
	h.press("d", "right", "enter")
	if h.m.mode != modeTrashing {
		t.Fatalf("mode %v after confirming", h.m.mode)
	}
	h.pump()
	if len(h.del.Calls()) != 1 {
		t.Fatalf("calls %d", len(h.del.Calls()))
	}
	h.moveTo("Polish landing page")
	h.press("d", "right", "left", "enter")
	if h.m.mode != modeBrowse || len(h.del.Calls()) != 1 {
		t.Fatal("cancel after switching confirmed anyway")
	}
}

func TestConfirmWithYTrashesExactlyTheSelection(t *testing.T) {
	h := newHarness(t, sample())
	h.moveTo("Fix login bug")
	h.press("space")
	h.moveTo("Polish landing page")
	h.press("space")
	h.press("d")
	if got := titles(h.m.confirm.targets); got != "Fix login bug,Polish landing page" {
		t.Fatalf("targets %q", got)
	}
	h.press("y")
	if h.m.mode != modeTrashing {
		t.Fatalf("mode %v", h.m.mode)
	}
	h.pump()
	if h.m.mode != modeResult {
		t.Fatalf("mode %v", h.m.mode)
	}
	calls := h.del.Calls()
	if len(calls) != 1 || titles(calls[0]) != "Fix login bug,Polish landing page" {
		t.Fatalf("deleter got %v", calls)
	}
	if len(h.m.all) != 3 || len(h.m.selected) != 0 {
		t.Fatalf("after delete: %d sessions, %d selected", len(h.m.all), len(h.m.selected))
	}
	view := ansiStrip(h.m.render())
	if !strings.Contains(view, "Moved 2 sessions to the Trash") {
		t.Fatalf("result view:\n%s", view)
	}
	h.press("enter")
	if h.m.mode != modeBrowse {
		t.Fatal("result did not dismiss")
	}
}

func TestDeleteOnLiveSessionDoesNothing(t *testing.T) {
	h := newHarness(t, sample())
	h.moveTo("Add rate limiter")
	h.press("d")
	if h.m.mode != modeBrowse {
		t.Fatalf("mode %v", h.m.mode)
	}
	h.press("y")
	if len(h.del.Calls()) != 0 {
		t.Fatal("live session sent to deleter")
	}
}

func TestDeleteOnFolderTargetsItsEligibleSessions(t *testing.T) {
	h := newHarness(t, sample())
	h.moveToGroup("-work-api")
	h.press("d")
	if h.m.mode != modeConfirm {
		t.Fatalf("mode %v", h.m.mode)
	}
	if titles(h.m.confirm.targets) != "Fix login bug" || h.m.confirm.live != 1 {
		t.Fatalf("targets %q live %d", titles(h.m.confirm.targets), h.m.confirm.live)
	}
	view := ansiStrip(h.m.render())
	if !strings.Contains(view, "in use and will be left alone") {
		t.Fatalf("live warning missing:\n%s", view)
	}
}

func TestHiddenSelectionIsCalledOut(t *testing.T) {
	h := newHarness(t, sample())
	h.moveTo("Fix login bug")
	h.press("space")
	h.press("/")
	h.typeText("landing")
	h.press("enter")
	if len(h.m.visibleSessions()) != 1 {
		t.Fatalf("filter shows %d", len(h.m.visibleSessions()))
	}
	h.press("d")
	if h.m.confirm.hidden != 1 || titles(h.m.confirm.targets) != "Fix login bug" {
		t.Fatalf("hidden %d targets %q", h.m.confirm.hidden, titles(h.m.confirm.targets))
	}
	if view := ansiStrip(h.m.render()); !strings.Contains(view, "hidden by the current filter") {
		t.Fatalf("hidden warning missing:\n%s", view)
	}
}

func TestFilterTypingAndClearing(t *testing.T) {
	h := newHarness(t, sample())
	h.press("/")
	if !h.m.filtering {
		t.Fatal("not filtering")
	}
	h.typeText("WEB")
	sessions := 0
	for _, r := range h.m.rows {
		if r.kind == rowSession {
			sessions++
		}
	}
	if sessions != 2 {
		t.Fatalf("filter 'WEB' shows %d sessions", sessions)
	}
	h.typeText("dqay")
	if h.m.mode != modeBrowse || len(h.m.selected) != 0 || len(h.del.Calls()) != 0 {
		t.Fatal("typing in the filter triggered an action")
	}
	h.press("backspace", "backspace", "backspace", "backspace")
	h.press("esc")
	if h.m.filtering || h.m.filter.Value() != "" || len(h.m.rows) != 8 {
		t.Fatalf("esc did not clear filter: %q rows=%d", h.m.filter.Value(), len(h.m.rows))
	}
	h.press("/")
	h.typeText("dark web")
	h.press("enter")
	if n := len(h.m.visibleSessions()); n != 1 {
		t.Fatalf("multi-term filter matched %d", n)
	}
	h.press("esc")
	if h.m.filter.Value() != "" {
		t.Fatal("esc in browse mode did not clear filter")
	}
}

func TestSelectAllRespectsFilterAndLive(t *testing.T) {
	h := newHarness(t, sample())
	h.press("a")
	if len(h.m.selected) != 4 {
		t.Fatalf("select all chose %d (live must be excluded)", len(h.m.selected))
	}
	h.press("n")
	if len(h.m.selected) != 0 {
		t.Fatal("select none failed")
	}
	h.press("/")
	h.typeText("api")
	h.press("enter", "a")
	if got := strings.Join(h.selectedTitles(), ","); got != "Fix login bug" {
		t.Fatalf("selected %q", got)
	}
}

func TestSelectFolderAndOrphans(t *testing.T) {
	h := newHarness(t, sample())
	h.moveTo("Dark mode toggle")
	h.press("f")
	if got := strings.Join(h.selectedTitles(), ","); got != "Polish landing page,Dark mode toggle" {
		t.Fatalf("folder select %q", got)
	}
	h.press("n", "o")
	if got := strings.Join(h.selectedTitles(), ","); got != "Legacy migration" {
		t.Fatalf("orphan select %q", got)
	}
}

func TestCtrlCDuringTrashStopsGracefully(t *testing.T) {
	h := newHarness(t, sample())
	h.del.block = make(chan struct{})
	h.moveTo("Fix login bug")
	h.press("d", "y")
	if h.m.mode != modeTrashing {
		t.Fatalf("mode %v", h.m.mode)
	}
	if isQuit(h.press("q")) || h.m.mode != modeTrashing {
		t.Fatal("q interrupted a running delete")
	}
	cmd := h.press("ctrl+c")
	if cmd != nil && isQuit(cmd) {
		t.Fatal("ctrl+c quit in the middle of a delete")
	}
	if !h.m.trash.cancelling {
		t.Fatal("not marked as cancelling")
	}
	h.pump()
	if !h.del.sawCtxOK {
		t.Fatal("deleter context was not cancelled")
	}
	if h.m.mode != modeResult {
		t.Fatalf("mode %v", h.m.mode)
	}
	if !isQuit(h.press("ctrl+c")) {
		t.Fatal("ctrl+c did not quit after delete")
	}
}

func TestFailedDeleteKeepsSessions(t *testing.T) {
	h := newHarness(t, sample())
	h.del.status = cleaner.StatusFailed
	h.moveTo("Fix login bug")
	h.press("space", "d", "y")
	h.pump()
	if len(h.m.all) != 5 {
		t.Fatal("failed session removed from list")
	}
	if len(h.m.selected) != 1 {
		t.Fatal("failed session deselected")
	}
	view := ansiStrip(h.m.render())
	if !strings.Contains(view, "failed, left in place") || !strings.Contains(view, "simulated failure") {
		t.Fatalf("failure not shown:\n%s", view)
	}
}

func TestDryRunWording(t *testing.T) {
	h := newHarness(t, sample(), func(o *Options) { o.DryRun = true })
	h.del.status = cleaner.StatusDryRun
	if !strings.Contains(ansiStrip(h.m.render()), "DRY RUN") {
		t.Fatal("dry run badge missing")
	}
	h.moveTo("Fix login bug")
	h.press("d")
	view := ansiStrip(h.m.render())
	if !strings.Contains(view, "Preview moving 1 session") || !strings.Contains(view, "nothing will be moved") {
		t.Fatalf("confirm view:\n%s", view)
	}
	h.press("y")
	h.pump()
	view = ansiStrip(h.m.render())
	if !strings.Contains(view, "Dry run: 1 session would be moved") || !strings.Contains(view, "Would move") {
		t.Fatalf("result view:\n%s", view)
	}
	if len(h.m.all) != 5 {
		t.Fatal("dry run removed sessions from the list")
	}
}

func TestQuitKeys(t *testing.T) {
	h := newHarness(t, sample())
	if !isQuit(h.press("q")) {
		t.Fatal("q did not quit")
	}
	h = newHarness(t, sample())
	if !isQuit(h.press("ctrl+c")) {
		t.Fatal("ctrl+c did not quit")
	}
	h = newHarness(t, sample())
	h.moveTo("Fix login bug")
	h.press("d")
	if isQuit(h.press("q")) || h.m.mode != modeBrowse {
		t.Fatal("q in dialog should cancel")
	}
}

func TestSortAndViewToggles(t *testing.T) {
	h := newHarness(t, sample())
	h.press("v")
	if h.m.grouped || len(h.m.rows) != 5 {
		t.Fatalf("flat view rows=%d", len(h.m.rows))
	}
	want := []string{"Fix login bug", "Add rate limiter", "Polish landing page", "Legacy migration", "Dark mode toggle"}
	for i, w := range want {
		if h.m.rows[i].sess.Title != w {
			t.Fatalf("recent order[%d] = %q", i, h.m.rows[i].sess.Title)
		}
	}
	h.press("s") // oldest
	if h.m.rows[0].sess.Title != "Dark mode toggle" {
		t.Fatalf("oldest first = %q", h.m.rows[0].sess.Title)
	}
	h.press("s") // largest
	if h.m.rows[0].sess.Title != "Dark mode toggle" || h.m.sortBy != sortLargest {
		t.Fatalf("largest first = %q", h.m.rows[0].sess.Title)
	}
	h.press("s") // prompts
	if h.m.sortBy != sortPrompts {
		t.Fatal("sort did not advance")
	}
	h.press("s")
	if h.m.sortBy != sortRecent {
		t.Fatal("sort did not wrap")
	}
	h.press("v")
	if !h.m.grouped {
		t.Fatal("grouped view not restored")
	}
}

func TestCollapseExpand(t *testing.T) {
	h := newHarness(t, sample())
	h.moveTo("Fix login bug")
	h.press("left")
	if !h.m.collapsed["-work-api"] || len(h.m.rows) != 6 {
		t.Fatalf("collapse: rows=%d", len(h.m.rows))
	}
	if r, _ := h.m.current(); r.kind != rowGroup {
		t.Fatal("cursor should move to the collapsed folder")
	}
	h.press("right")
	if h.m.collapsed["-work-api"] || len(h.m.rows) != 8 {
		t.Fatal("expand failed")
	}
	h.press("enter")
	if !h.m.collapsed["-work-api"] {
		t.Fatal("enter on folder should toggle collapse")
	}
}

func TestNavigationBounds(t *testing.T) {
	h := newHarness(t, sample())
	h.press("up", "up", "home")
	if h.m.cursor != 0 {
		t.Fatal("cursor went above the top")
	}
	h.press("end", "down", "down")
	if h.m.cursor != len(h.m.rows)-1 {
		t.Fatal("cursor went below the bottom")
	}
	h.press("home", "pgdown")
	if h.m.cursor != len(h.m.rows)-1 {
		t.Fatalf("pgdown cursor %d", h.m.cursor)
	}
	h.press("G", "g")
	if h.m.cursor != 0 {
		t.Fatal("g did not go home")
	}
}

func TestEmptyListIsSafe(t *testing.T) {
	h := newHarness(t, nil)
	h.press("space", "d", "a", "f", "o", "left", "right", "enter", "down", "end")
	if h.m.mode != modeBrowse || len(h.del.Calls()) != 0 {
		t.Fatal("empty list misbehaved")
	}
	if !strings.Contains(ansiStrip(h.m.render()), "No Claude Code sessions found") {
		t.Fatal("empty state missing")
	}
}

func TestRescanKeepsValidSelection(t *testing.T) {
	ss := sample()
	h := newHarness(t, ss)
	h.press("a")
	if len(h.m.selected) != 4 {
		t.Fatal("setup")
	}
	next := []*session.Session{
		sess(1, "-work-api", "/work/api", "Fix login bug", live),
		ss[1], ss[3], ss[4],
	}
	h.m.opt.Load = func(context.Context) (*session.Result, error) { return &session.Result{Sessions: next}, nil }
	h.press("r")
	if h.m.mode != modeLoading {
		t.Fatal("r did not start a rescan")
	}
	h.send(h.loadMsg())
	if got := strings.Join(h.selectedTitles(), ","); got != "Legacy migration,Dark mode toggle" {
		t.Fatalf("selection after rescan %q", got)
	}
}

func TestLoadErrorAndRetry(t *testing.T) {
	calls := 0
	h := &harness{t: t, del: &fakeDeleter{}}
	h.m = New(Options{Now: func() time.Time { return testNow }, Load: func(context.Context) (*session.Result, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("permission denied")
		}
		return &session.Result{Sessions: sample()}, nil
	}})
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.send(h.loadMsg())
	if h.m.mode != modeError || !strings.Contains(ansiStrip(h.m.render()), "permission denied") {
		t.Fatalf("mode %v", h.m.mode)
	}
	h.press("r")
	h.send(h.loadMsg())
	if h.m.mode != modeBrowse || len(h.m.all) != 5 {
		t.Fatalf("retry: mode %v", h.m.mode)
	}
}

func TestNoLoaderOrDeleterIsHandled(t *testing.T) {
	m := New(Options{})
	msg := m.load()()
	next, _ := m.Update(msg)
	if next.(Model).mode != modeError {
		t.Fatal("missing loader not reported")
	}
	h := newHarness(t, sample(), func(o *Options) { o.Deleter = nil })
	h.moveTo("Fix login bug")
	h.press("d", "y")
	if h.m.mode != modeBrowse {
		t.Fatalf("mode %v without a deleter", h.m.mode)
	}
}

func TestHelpOverlay(t *testing.T) {
	h := newHarness(t, sample())
	h.press("?")
	if h.m.mode != modeHelp || !strings.Contains(ansiStrip(h.m.render()), "Nothing is deleted permanently") {
		t.Fatal("help not shown")
	}
	h.press("d")
	if h.m.mode != modeHelp {
		t.Fatal("keys leaked through the help overlay")
	}
	h.press("esc")
	if h.m.mode != modeBrowse {
		t.Fatal("help did not close")
	}
}

func TestMouse(t *testing.T) {
	h := newHarness(t, sample())
	l := h.m.layout()
	h.send(tea.MouseClickMsg{X: l.listX + 20, Y: l.listY + 1, Button: tea.MouseLeft})
	if h.m.cursor != 1 || len(h.m.selected) != 0 {
		t.Fatalf("cursor %d selected %d", h.m.cursor, len(h.m.selected))
	}
	h.send(tea.MouseClickMsg{X: l.listX + 4, Y: l.listY + 1, Button: tea.MouseLeft})
	if h.m.cursor != 1 || len(h.m.selected) != 1 {
		t.Fatalf("checkbox click: cursor %d selected %d", h.m.cursor, len(h.m.selected))
	}
	h.send(tea.MouseClickMsg{X: l.listX + 4, Y: l.listY + 2, Button: tea.MouseLeft})
	if len(h.m.selected) != 1 {
		t.Fatal("live session selected by mouse")
	}
	h.send(tea.MouseClickMsg{X: 1000, Y: 1000, Button: tea.MouseLeft})
	h.send(tea.MouseClickMsg{X: l.listX, Y: 0, Button: tea.MouseLeft})
	h.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if h.m.cursor != 5 {
		t.Fatalf("wheel cursor %d", h.m.cursor)
	}
	h.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if h.m.cursor != 2 {
		t.Fatalf("wheel up cursor %d", h.m.cursor)
	}
}

func TestToastExpires(t *testing.T) {
	h := newHarness(t, sample())
	h.press("s")
	id := h.m.toast.id
	if h.m.toast.text == "" {
		t.Fatal("no toast")
	}
	h.send(toastDoneMsg{id: id - 1})
	if h.m.toast.text == "" {
		t.Fatal("stale timer cleared a newer toast")
	}
	h.send(toastDoneMsg{id: id})
	if h.m.toast.text != "" {
		t.Fatal("toast did not expire")
	}
}

func TestThemeFollowsBackground(t *testing.T) {
	h := newHarness(t, sample())
	h.send(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	if h.m.dark {
		t.Fatal("light background not detected")
	}
	h.send(tea.BackgroundColorMsg{Color: lipgloss.Color("#000000")})
	if !h.m.dark {
		t.Fatal("dark background not detected")
	}
}
