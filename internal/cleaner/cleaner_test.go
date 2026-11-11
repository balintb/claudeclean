package cleaner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/balintb/claudeclean/internal/fixture"
	"github.com/balintb/claudeclean/internal/layout"
	"github.com/balintb/claudeclean/internal/session"
)

var (
	now     = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	noProcs = func(int) bool { return false }
)

// fakeTrash moves items into its own dir and can fail or lie about success.
type fakeTrash struct {
	mu      sync.Mutex
	dir     string
	calls   []string
	failOn  func(path string) error
	lie     bool              // report success without moving anything
	onTrash func(path string) // runs after a successful move
}

func newFakeTrash(t *testing.T) *fakeTrash {
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &fakeTrash{dir: d}
}

func (f *fakeTrash) Trash(path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, path)
	if f.failOn != nil {
		if err := f.failOn(path); err != nil {
			return "", err
		}
	}
	if f.lie {
		return "/nowhere", nil
	}
	dest := filepath.Join(f.dir, fmt.Sprintf("%03d-%s", len(f.calls), filepath.Base(path)))
	if err := os.Rename(path, dest); err != nil {
		return "", err
	}
	if f.onTrash != nil {
		f.onTrash(path)
	}
	return dest, nil
}

func (f *fakeTrash) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type env struct {
	t     *testing.T
	root  *fixture.Root
	trash *fakeTrash
	c     *Cleaner
}

func newEnv(t *testing.T) *env {
	r := fixture.New(t)
	ft := newFakeTrash(t)
	return &env{t: t, root: r, trash: ft, c: &Cleaner{
		Root:        r.Path,
		Trasher:     ft,
		JournalPath: filepath.Join(t.TempDir(), "logs", "trash-log.jsonl"),
		Now:         nowFunc,
		Alive:       noProcs,
	}}
}

func (e *env) scan() []*session.Session {
	e.t.Helper()
	res, err := session.Scan(context.Background(), session.Options{Root: e.root.Path, Now: nowFunc, Alive: noProcs})
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Sessions
}

func (e *env) find(id string) *session.Session {
	e.t.Helper()
	for _, s := range e.scan() {
		if s.ID == id {
			return s
		}
	}
	e.t.Fatalf("session %s not found", id)
	return nil
}

func (e *env) run(ss ...*session.Session) Report {
	return e.c.Run(context.Background(), ss, nil)
}

func TestTrashesEverythingTranscriptLast(t *testing.T) {
	e := newEnv(t)
	id := fixture.ID(1)
	e.root.Session("-p", id, fixture.Opts{CWD: "/p", Title: "One", Extra: true})
	e.root.Session("-p", fixture.ID(2), fixture.Opts{CWD: "/p"})
	s := e.find(id)

	var events []Event
	rep := e.c.Run(context.Background(), []*session.Session{s}, func(ev Event) { events = append(events, ev) })
	if len(rep.Outcomes) != 1 || rep.Outcomes[0].Status != StatusTrashed {
		t.Fatalf("outcome: %+v", rep.Outcomes)
	}
	if rep.JournalErr != nil {
		t.Fatalf("journal: %v", rep.JournalErr)
	}
	calls := e.trash.Calls()
	if len(calls) != 6 {
		t.Fatalf("trash calls: %v", calls)
	}
	if !strings.HasSuffix(calls[len(calls)-1], id+".jsonl") {
		t.Fatalf("transcript not trashed last: %v", calls)
	}
	for _, rel := range []string{
		"projects/-p/" + id + ".jsonl", "projects/-p/" + id, "file-history/" + id,
		"session-env/" + id, "todos/" + id + "-agent-" + id + ".json", "debug/" + id + ".txt",
	} {
		if e.root.Exists(rel) {
			t.Errorf("%s still exists", rel)
		}
	}
	for _, rel := range []string{"projects/-p/" + fixture.ID(2) + ".jsonl", "projects/-p", "projects", "file-history", "todos", "debug"} {
		if !e.root.Exists(rel) {
			t.Errorf("%s was removed", rel)
		}
	}
	if len(events) != 1 || events[0].Done != 1 || events[0].Total != 1 {
		t.Errorf("events: %+v", events)
	}
	if n := len(rep.Outcomes[0].Items); n != 6 {
		t.Errorf("items: %d", n)
	}
	for _, it := range rep.Outcomes[0].Items {
		if it.TrashedAs == "" || !strings.HasPrefix(it.TrashedAs, e.trash.dir) {
			t.Errorf("item %s has no trash location", it.Original)
		}
		if _, err := os.Lstat(it.TrashedAs); err != nil {
			t.Errorf("trashed item missing from trash: %v", err)
		}
	}
	if rep.Count(StatusTrashed) != 1 {
		t.Error("Count wrong")
	}
}

func TestJournalRecordsEveryItem(t *testing.T) {
	e := newEnv(t)
	id := fixture.ID(1)
	e.root.Session("-p", id, fixture.Opts{CWD: "/p", Title: "Logged", Extra: true})
	rep := e.run(e.find(id))
	if rep.JournalErr != nil {
		t.Fatal(rep.JournalErr)
	}
	f, err := os.Open(e.c.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, _ := f.Stat()
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("journal perms %v, want 0600", fi.Mode().Perm())
	}
	var entries []journalEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var je journalEntry
		if err := json.Unmarshal(sc.Bytes(), &je); err != nil {
			t.Fatalf("bad journal line %q: %v", sc.Text(), err)
		}
		entries = append(entries, je)
	}
	if len(entries) != len(rep.Outcomes[0].Items) {
		t.Fatalf("%d journal entries for %d items", len(entries), len(rep.Outcomes[0].Items))
	}
	for i, je := range entries {
		it := rep.Outcomes[0].Items[i]
		if je.SessionID != id || je.Title != "Logged" || je.Original != it.Original || je.TrashedAs != it.TrashedAs || je.Kind != it.Kind.String() {
			t.Errorf("entry %d = %+v, item %+v", i, je, it)
		}
	}

	e.root.Session("-p", fixture.ID(2), fixture.Opts{CWD: "/p"})
	e.run(e.find(fixture.ID(2)))
	b, _ := os.ReadFile(e.c.JournalPath)
	if got := strings.Count(string(b), "\n"); got != len(entries)+1 {
		t.Errorf("journal has %d lines after second run, want %d", got, len(entries)+1)
	}
}

func TestJournalFailureDoesNotStopTrashing(t *testing.T) {
	e := newEnv(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e.c.JournalPath = filepath.Join(blocker, "sub", "log.jsonl") // parent is a file
	e.root.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p"})
	rep := e.run(e.find(fixture.ID(1)))
	if rep.Outcomes[0].Status != StatusTrashed {
		t.Fatalf("status %v: %v", rep.Outcomes[0].Status, rep.Outcomes[0].Err)
	}
	if rep.JournalErr == nil {
		t.Fatal("journal error not reported")
	}
}

func TestNoJournalWhenPathEmpty(t *testing.T) {
	e := newEnv(t)
	e.c.JournalPath = ""
	e.root.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p"})
	if rep := e.run(e.find(fixture.ID(1))); rep.Outcomes[0].Status != StatusTrashed || rep.JournalErr != nil {
		t.Fatalf("%+v", rep)
	}
}

func TestDryRunTouchesNothing(t *testing.T) {
	e := newEnv(t)
	e.c.DryRun = true
	e.c.Trasher = nil // must not be needed
	id := fixture.ID(1)
	e.root.Session("-p", id, fixture.Opts{CWD: "/p", Extra: true})
	before := e.root.Snapshot()
	rep := e.run(e.find(id))
	if rep.Outcomes[0].Status != StatusDryRun {
		t.Fatalf("status %v: %v", rep.Outcomes[0].Status, rep.Outcomes[0].Err)
	}
	if len(rep.Outcomes[0].Items) != 6 {
		t.Errorf("dry run listed %d items", len(rep.Outcomes[0].Items))
	}
	after := e.root.Snapshot()
	if len(before) != len(after) {
		t.Fatal("dry run changed the tree")
	}
	if _, err := os.Stat(e.c.JournalPath); !errors.Is(err, os.ErrNotExist) {
		t.Error("dry run wrote a journal")
	}
}

func TestLiveSessionIsNeverTrashed(t *testing.T) {
	e := newEnv(t)
	id := fixture.ID(1)
	e.root.Session("-p", id, fixture.Opts{CWD: "/p", Extra: true})
	s := e.find(id) // scanned while not live
	e.root.Registry(4242, id, "busy")
	e.c.Alive = func(pid int) bool { return pid == 4242 }
	rep := e.run(s)
	o := rep.Outcomes[0]
	if o.Status != StatusSkipped || !errors.Is(o.Err, ErrLive) {
		t.Fatalf("got %v %v", o.Status, o.Err)
	}
	if len(e.trash.Calls()) != 0 {
		t.Fatalf("trash called: %v", e.trash.Calls())
	}
}

func TestRecentlyWrittenSessionIsSkipped(t *testing.T) {
	e := newEnv(t)
	id := fixture.ID(1)
	e.root.Session("-p", id, fixture.Opts{CWD: "/p"})
	s := e.find(id)
	p := s.TranscriptPath(e.root.Path)
	if err := os.Chtimes(p, now.Add(-5*time.Second), now.Add(-5*time.Second)); err != nil {
		t.Fatal(err)
	}
	rep := e.run(s)
	if rep.Outcomes[0].Status != StatusSkipped || len(e.trash.Calls()) != 0 {
		t.Fatalf("got %v, calls %v", rep.Outcomes[0].Status, e.trash.Calls())
	}
}

func TestRegistryUnreadableFailsClosed(t *testing.T) {
	e := newEnv(t)
	e.root.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p"})
	s := e.find(fixture.ID(1))
	e.root.WriteFile("sessions", []byte("not a dir"))
	rep := e.run(s)
	if rep.Outcomes[0].Status != StatusFailed || len(e.trash.Calls()) != 0 {
		t.Fatalf("got %v %v, calls %v", rep.Outcomes[0].Status, rep.Outcomes[0].Err, e.trash.Calls())
	}
}

func TestAuxFailureLeavesTranscript(t *testing.T) {
	e := newEnv(t)
	id := fixture.ID(1)
	e.root.Session("-p", id, fixture.Opts{CWD: "/p", Extra: true})
	boom := errors.New("trash full")
	e.trash.failOn = func(p string) error {
		if strings.Contains(p, "session-env") {
			return boom
		}
		return nil
	}
	rep := e.run(e.find(id))
	o := rep.Outcomes[0]
	if o.Status != StatusFailed || !errors.Is(o.Err, boom) {
		t.Fatalf("got %v %v", o.Status, o.Err)
	}
	if !e.root.Exists("projects/-p/" + id + ".jsonl") {
		t.Fatal("transcript trashed despite earlier failure")
	}
	if len(o.Items) == 0 {
		t.Error("partial items not reported")
	}
	e.trash.failOn = nil
	rep = e.run(e.find(id))
	if rep.Outcomes[0].Status != StatusTrashed {
		t.Fatalf("retry: %v %v", rep.Outcomes[0].Status, rep.Outcomes[0].Err)
	}
	if e.root.Exists("projects/-p/" + id + ".jsonl") {
		t.Fatal("transcript survived retry")
	}
}

func TestLyingTrashIsDetected(t *testing.T) {
	e := newEnv(t)
	e.trash.lie = true
	e.root.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p", Extra: true})
	rep := e.run(e.find(fixture.ID(1)))
	o := rep.Outcomes[0]
	if o.Status != StatusFailed || !errors.Is(o.Err, ErrStillThere) {
		t.Fatalf("got %v %v", o.Status, o.Err)
	}
	if len(e.trash.Calls()) != 1 {
		t.Fatalf("kept going after a failed move: %v", e.trash.Calls())
	}
}

func TestSymlinkSwappedInAfterScanIsRefused(t *testing.T) {
	e := newEnv(t)
	id := fixture.ID(1)
	e.root.Session("-p", id, fixture.Opts{CWD: "/p", Extra: true})
	s := e.find(id)

	precious := t.TempDir()
	if err := os.WriteFile(filepath.Join(precious, "thesis.tex"), []byte("years of work"), 0o644); err != nil {
		t.Fatal(err)
	}
	fh := e.root.Join("file-history", id)
	if err := os.Rename(fh, fh+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(precious, fh); err != nil {
		t.Fatal(err)
	}
	rep := e.run(s)
	for _, c := range e.trash.Calls() {
		if c == fh || strings.HasPrefix(c, precious) {
			t.Fatalf("trash called on %s", c)
		}
	}
	if _, err := os.Stat(filepath.Join(precious, "thesis.tex")); err != nil {
		t.Fatalf("precious file affected: %v", err)
	}
	if rep.Outcomes[0].Status != StatusTrashed {
		t.Fatalf("status %v: %v", rep.Outcomes[0].Status, rep.Outcomes[0].Err)
	}
}

func TestSymlinkRaceDuringTrashIsRefused(t *testing.T) {
	e := newEnv(t)
	id := fixture.ID(1)
	e.root.Session("-p", id, fixture.Opts{CWD: "/p", Extra: true})
	precious := t.TempDir()
	evil := e.root.Join("projects", "-p")
	swapped := false
	e.trash.onTrash = func(string) {
		if swapped {
			return
		}
		swapped = true
		if err := os.Rename(evil, evil+".real"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(precious, evil); err != nil {
			t.Fatal(err)
		}
	}
	rep := e.run(e.find(id))
	o := rep.Outcomes[0]
	if o.Status != StatusFailed || !errors.Is(o.Err, layout.ErrSymlink) {
		t.Fatalf("got %v %v", o.Status, o.Err)
	}
	if ents, _ := os.ReadDir(precious); len(ents) != 0 {
		t.Fatalf("something was written through the symlink: %v", ents)
	}
	if _, err := os.Lstat(filepath.Join(evil+".real", id+".jsonl")); err != nil {
		t.Fatalf("transcript should be left in place: %v", err)
	}
	for _, c := range e.trash.Calls() {
		if strings.HasSuffix(c, ".jsonl") {
			t.Fatalf("transcript was handed to the trash: %s", c)
		}
	}
}

func TestTranscriptGoneSinceScan(t *testing.T) {
	e := newEnv(t)
	id := fixture.ID(1)
	e.root.Session("-p", id, fixture.Opts{CWD: "/p", Extra: true})
	s := e.find(id)
	if err := os.Rename(s.TranscriptPath(e.root.Path), filepath.Join(t.TempDir(), "x")); err != nil {
		t.Fatal(err)
	}
	rep := e.run(s)
	if rep.Outcomes[0].Status != StatusFailed || !errors.Is(rep.Outcomes[0].Err, ErrGone) {
		t.Fatalf("got %v %v", rep.Outcomes[0].Status, rep.Outcomes[0].Err)
	}
	if len(e.trash.Calls()) != 0 {
		t.Fatal("orphaned artifacts trashed without a transcript")
	}
}

func TestArtifactsCreatedAfterScanAreIncluded(t *testing.T) {
	e := newEnv(t)
	id := fixture.ID(1)
	e.root.Session("-p", id, fixture.Opts{CWD: "/p"})
	s := e.find(id)
	e.root.WriteFile("debug/"+id+".txt", []byte("late"))
	rep := e.run(s)
	if rep.Outcomes[0].Status != StatusTrashed || e.root.Exists("debug/"+id+".txt") {
		t.Fatalf("late artifact not trashed: %+v", rep.Outcomes[0])
	}
}

func TestSharedIDGlobalsGoWithTheLastCopy(t *testing.T) {
	e := newEnv(t)
	id := fixture.ID(1)
	e.root.Session("-a", id, fixture.Opts{CWD: "/a", Extra: true})
	e.root.Session("-b", id, fixture.Opts{CWD: "/b"})
	ss := e.scan()
	var a, b *session.Session
	for _, s := range ss {
		if s.ProjectDir == "-a" {
			a = s
		} else {
			b = s
		}
	}
	rep := e.run(a)
	if rep.Outcomes[0].Status != StatusTrashed {
		t.Fatal(rep.Outcomes[0].Err)
	}
	if !e.root.Exists("file-history/" + id) {
		t.Fatal("shared file history trashed while another copy exists")
	}
	rep = e.run(b)
	if rep.Outcomes[0].Status != StatusTrashed {
		t.Fatal(rep.Outcomes[0].Err)
	}
	if e.root.Exists("file-history/" + id) {
		t.Fatal("file history left behind after the last copy was trashed")
	}
}

func TestCancelledRunSkipsTheRest(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 3; i++ {
		e.root.Session("-p", fixture.ID(i), fixture.Opts{CWD: "/p"})
	}
	ss := e.scan()
	ctx, cancel := context.WithCancel(context.Background())
	rep := e.c.Run(ctx, ss, func(ev Event) {
		if ev.Done == 1 {
			cancel()
		}
	})
	if rep.Count(StatusTrashed) != 1 || rep.Count(StatusSkipped) != 2 {
		t.Fatalf("trashed=%d skipped=%d", rep.Count(StatusTrashed), rep.Count(StatusSkipped))
	}
	for _, o := range rep.Outcomes[1:] {
		if !errors.Is(o.Err, ErrCancelled) {
			t.Errorf("err %v", o.Err)
		}
	}
}

func TestDuplicatesAndNilsAreIgnored(t *testing.T) {
	e := newEnv(t)
	e.root.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p"})
	s := e.find(fixture.ID(1))
	rep := e.c.Run(context.Background(), []*session.Session{s, nil, s}, nil)
	if len(rep.Outcomes) != 1 || rep.Outcomes[0].Status != StatusTrashed {
		t.Fatalf("%+v", rep.Outcomes)
	}
}

func TestForgedSessionIsRejected(t *testing.T) {
	e := newEnv(t)
	cases := []*session.Session{
		{ID: "../../../etc", ProjectDir: "-p"},
		{ID: fixture.ID(1), ProjectDir: ".."},
		{ID: fixture.ID(1), ProjectDir: "a/b"},
		{ID: "", ProjectDir: ""},
	}
	rep := e.c.Run(context.Background(), cases, nil)
	for _, o := range rep.Outcomes {
		if o.Status != StatusFailed {
			t.Errorf("%+v not rejected", o.Session)
		}
	}
	if len(e.trash.Calls()) != 0 {
		t.Fatal("trash called for forged sessions")
	}
}

func TestUnresolvedRootFailsEverything(t *testing.T) {
	e := newEnv(t)
	e.root.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p"})
	s := e.find(fixture.ID(1))
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(e.root.Path, link); err != nil {
		t.Fatal(err)
	}
	e.c.Root = link
	rep := e.run(s)
	if rep.Outcomes[0].Status != StatusFailed || !errors.Is(rep.Outcomes[0].Err, ErrRootChanged) {
		t.Fatalf("got %v %v", rep.Outcomes[0].Status, rep.Outcomes[0].Err)
	}
	e.c.Root = "relative/path"
	if rep := e.run(s); rep.Outcomes[0].Status != StatusFailed {
		t.Fatal("relative root accepted")
	}
}

func TestNoTrasherFails(t *testing.T) {
	e := newEnv(t)
	e.c.Trasher = nil
	e.root.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p"})
	rep := e.run(e.find(fixture.ID(1)))
	if !errors.Is(rep.Outcomes[0].Err, ErrNoTrash) {
		t.Fatalf("got %v", rep.Outcomes[0].Err)
	}
	if !e.root.Exists("projects/-p/" + fixture.ID(1) + ".jsonl") {
		t.Fatal("transcript gone")
	}
}

func TestStatusStrings(t *testing.T) {
	for s, want := range map[Status]string{StatusTrashed: "trashed", StatusDryRun: "dry run", StatusSkipped: "skipped", StatusFailed: "failed", Status(9): "status(9)"} {
		if s.String() != want {
			t.Errorf("%d: %q", s, s.String())
		}
	}
}

func TestManySessionsMixedOutcomes(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 10; i++ {
		e.root.Session("-p", fixture.ID(i), fixture.Opts{CWD: "/p", Extra: i%2 == 0})
	}
	e.root.Registry(7, fixture.ID(3), "busy")
	e.c.Alive = func(pid int) bool { return pid == 7 }
	e.trash.failOn = func(p string) error {
		if strings.Contains(p, fixture.ID(5)) {
			return errors.New("nope")
		}
		return nil
	}
	rep := e.run(e.scan()...)
	if got := rep.Count(StatusTrashed); got != 8 {
		t.Errorf("trashed %d, want 8", got)
	}
	if rep.Count(StatusSkipped) != 1 || rep.Count(StatusFailed) != 1 {
		t.Errorf("skipped=%d failed=%d", rep.Count(StatusSkipped), rep.Count(StatusFailed))
	}
	left := e.scan()
	if len(left) != 2 {
		t.Fatalf("%d sessions left, want 2", len(left))
	}
}

func TestSymlinkedProjectDirBeforeRunMovesNothing(t *testing.T) {
	e := newEnv(t)
	id := fixture.ID(1)
	e.root.Session("-p", id, fixture.Opts{CWD: "/p", Extra: true})
	s := e.find(id)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Rename(e.root.Join("projects", "-p"), elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, e.root.Join("projects", "-p")); err != nil {
		t.Fatal(err)
	}
	rep := e.run(s)
	o := rep.Outcomes[0]
	if o.Status != StatusFailed || !errors.Is(o.Err, layout.ErrSymlink) {
		t.Fatalf("got %v %v", o.Status, o.Err)
	}
	if calls := e.trash.Calls(); len(calls) != 0 {
		t.Fatalf("validation must fail before anything moves, but trashed %v", calls)
	}
	if !e.root.Exists("file-history/" + id) {
		t.Fatal("file history moved despite the refusal")
	}
}
