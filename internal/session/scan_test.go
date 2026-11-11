package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/balintb/claudeclean/internal/fixture"
	"github.com/balintb/claudeclean/internal/layout"
)

var (
	now     = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	noProcs = func(int) bool { return false }
)

func scan(t *testing.T, root string) *Result {
	t.Helper()
	res, err := Scan(context.Background(), Options{Root: root, Now: nowFunc, Alive: noProcs})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func byID(res *Result) map[string]*Session {
	m := map[string]*Session{}
	for _, s := range res.Sessions {
		m[s.ID] = s
	}
	return m
}

func TestScanFindsSessionsAndArtifacts(t *testing.T) {
	r := fixture.New(t)
	work := t.TempDir()
	proj := layout.EncodeProjectDir(work)
	r.Session(proj, fixture.ID(1), fixture.Opts{CWD: work, Title: "First", Branch: "main", Extra: true})
	r.Session(proj, fixture.ID(2), fixture.Opts{CWD: work, Prompt: "second prompt", Started: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)})
	r.Session("-gone-folder", fixture.ID(3), fixture.Opts{CWD: "/gone/folder"})

	res := scan(t, r.Path)
	if len(res.Sessions) != 3 {
		t.Fatalf("got %d sessions, want 3", len(res.Sessions))
	}
	s := byID(res)
	one := s[fixture.ID(1)]
	if one.Title != "First" || one.TitleSource != TitleAI || one.GitBranch != "main" {
		t.Errorf("session 1 metadata: %+v", one)
	}
	if one.CWD != work || !one.CWDExists || one.CWDGuessed {
		t.Errorf("session 1 cwd: %q exists=%v guessed=%v", one.CWD, one.CWDExists, one.CWDGuessed)
	}
	if one.Prompts != 1 || one.Messages != 4 {
		t.Errorf("session 1 counts: prompts=%d messages=%d", one.Prompts, one.Messages)
	}
	kinds := map[layout.Kind]bool{}
	var sum int64
	for _, a := range one.Artifacts {
		kinds[a.Kind] = true
		sum += a.Size
		if _, err := layout.Validate(r.Path, a.Path, one.ID); err != nil {
			t.Errorf("collected artifact fails validation: %v", err)
		}
	}
	for _, k := range []layout.Kind{layout.KindTranscript, layout.KindSessionDir, layout.KindFileHistory, layout.KindSessionEnv, layout.KindTodo, layout.KindDebugLog} {
		if !kinds[k] {
			t.Errorf("missing artifact kind %v", k)
		}
	}
	if one.Artifacts[0].Kind != layout.KindTranscript {
		t.Errorf("transcript should be listed first")
	}
	if one.Size != sum || one.Size == 0 {
		t.Errorf("size %d, artifacts sum %d", one.Size, sum)
	}
	if one.FileCount() < 5 {
		t.Errorf("FileCount = %d", one.FileCount())
	}

	two := s[fixture.ID(2)]
	if two.Title != "second prompt" || two.TitleSource != TitlePrompt || len(two.Artifacts) != 1 {
		t.Errorf("session 2: %q %v %d artifacts", two.Title, two.TitleSource, len(two.Artifacts))
	}
	three := s[fixture.ID(3)]
	if three.CWDExists {
		t.Errorf("session 3 folder should not exist")
	}
	if res.Sessions[0].ID != fixture.ID(2) {
		t.Errorf("not sorted newest first: %s", res.Sessions[0].ID)
	}
}

func TestScanIgnoresJunk(t *testing.T) {
	r := fixture.New(t)
	r.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p"})
	r.WriteFile("projects/-p/notes.jsonl", []byte(`{"type":"user"}`))
	r.WriteFile("projects/-p/"+strings.ToUpper("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")+".jsonl", []byte(`{}`))
	r.WriteFile("projects/-p/"+fixture.ID(9)+".jsonl.bak", []byte(`{}`))
	r.WriteFile("projects/-p/memory/MEMORY.md", []byte("x"))
	r.WriteFile("projects/.DS_Store", []byte("x"))
	r.WriteFile("projects/stray-file.jsonl", []byte("x"))
	r.Mkdir("projects/.hidden")
	r.Session(".hidden", fixture.ID(5), fixture.Opts{CWD: "/h"})
	r.Mkdir("projects/-p/" + fixture.ID(6) + ".jsonl")

	outside := t.TempDir()
	target := filepath.Join(outside, fixture.ID(7)+".jsonl")
	if err := os.WriteFile(target, fixture.JSONL(t, L{"type": "user", "message": L{"content": "x"}}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, r.Join("projects", "-p", fixture.ID(7)+".jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, r.Join("projects", "-linked")); err != nil {
		t.Fatal(err)
	}

	res := scan(t, r.Path)
	if len(res.Sessions) != 1 || res.Sessions[0].ID != fixture.ID(1) {
		var ids []string
		for _, s := range res.Sessions {
			ids = append(ids, s.ProjectDir+"/"+s.ID)
		}
		t.Fatalf("got %v, want only %s", ids, fixture.ID(1))
	}
}

func TestScanSymlinkedArtifactsAreNotCollected(t *testing.T) {
	r := fixture.New(t)
	id := fixture.ID(1)
	r.Session("-p", id, fixture.Opts{CWD: "/p"})
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "precious"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Mkdir("file-history")
	if err := os.Symlink(outside, r.Join("file-history", id)); err != nil {
		t.Fatal(err)
	}
	r.Mkdir("debug")
	if err := os.Symlink(filepath.Join(outside, "precious"), r.Join("debug", id+".txt")); err != nil {
		t.Fatal(err)
	}
	s := scan(t, r.Path).Sessions[0]
	for _, a := range s.Artifacts {
		if a.Kind == layout.KindFileHistory || a.Kind == layout.KindDebugLog {
			t.Fatalf("symlinked %v collected: %s", a.Kind, a.Path)
		}
	}
}

func TestScanSessionDirSizeDoesNotFollowSymlinks(t *testing.T) {
	r := fixture.New(t)
	id := fixture.ID(1)
	r.Session("-p", id, fixture.Opts{CWD: "/p"})
	r.WriteFile("projects/-p/"+id+"/small.txt", []byte("12345"))
	big := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(big, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(big, r.Join("projects", "-p", id, "link")); err != nil {
		t.Fatal(err)
	}
	s := scan(t, r.Path).Sessions[0]
	for _, a := range s.Artifacts {
		if a.Kind == layout.KindSessionDir && (a.Size != 5 || a.Files != 1) {
			t.Fatalf("session dir size %d files %d, want 5 and 1", a.Size, a.Files)
		}
	}
}

func TestScanSharedIDKeepsGlobalArtifacts(t *testing.T) {
	r := fixture.New(t)
	id := fixture.ID(1)
	r.Session("-a", id, fixture.Opts{CWD: "/a", Extra: true})
	r.Session("-b", id, fixture.Opts{CWD: "/b"})
	res := scan(t, r.Path)
	if len(res.Sessions) != 2 {
		t.Fatalf("got %d sessions", len(res.Sessions))
	}
	for _, s := range res.Sessions {
		if !s.SharedID {
			t.Errorf("%s not marked shared", s.Key())
		}
		for _, a := range s.Artifacts {
			if a.Kind.IsGlobal() {
				t.Errorf("%s: global artifact %v collected for shared id", s.Key(), a.Kind)
			}
		}
	}
	if res.Sessions[0].Key() == res.Sessions[1].Key() {
		t.Error("keys collide")
	}
}

func TestScanLiveDetection(t *testing.T) {
	r := fixture.New(t)
	r.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p"})
	r.Session("-p", fixture.ID(2), fixture.Opts{CWD: "/p"})
	r.Session("-p", fixture.ID(3), fixture.Opts{CWD: "/p", ModTime: now.Add(-30 * time.Second)})
	r.Session("-p", fixture.ID(4), fixture.Opts{CWD: "/p"})
	r.Registry(100, fixture.ID(1), "busy")
	r.Registry(200, fixture.ID(2), "idle") // dead process
	r.WriteFile("sessions/garbage.json", []byte("{not json"))
	r.WriteFile("sessions/300.json", []byte(`{"pid":300,"sessionId":"../../etc"}`))
	r.WriteFile("sessions/readme.txt", []byte("ignored"))

	alive := func(pid int) bool { return pid == 100 || pid == 300 }
	res, err := Scan(context.Background(), Options{Root: r.Path, Now: nowFunc, Alive: alive})
	if err != nil {
		t.Fatal(err)
	}
	s := byID(res)
	if !s[fixture.ID(1)].Live || !strings.Contains(s[fixture.ID(1)].LiveReason, "pid 100") {
		t.Errorf("registered live session not live: %+v", s[fixture.ID(1)].LiveReason)
	}
	if s[fixture.ID(2)].Live {
		t.Error("dead process marked live")
	}
	if !s[fixture.ID(3)].Live {
		t.Error("recently written session not live")
	}
	if s[fixture.ID(4)].Live {
		t.Error("old session marked live")
	}

	res, err = Scan(context.Background(), Options{Root: r.Path, Now: nowFunc, Alive: noProcs, RecentWindow: -1})
	if err != nil {
		t.Fatal(err)
	}
	if byID(res)[fixture.ID(3)].Live {
		t.Error("recency check not disabled")
	}
}

func TestScanEmptyAndMissing(t *testing.T) {
	r := fixture.New(t)
	if res := scan(t, r.Path); len(res.Sessions) != 0 {
		t.Fatalf("empty root returned %d sessions", len(res.Sessions))
	}
	bare := t.TempDir()
	bare, _ = filepath.EvalSymlinks(bare)
	if res := scan(t, bare); len(res.Sessions) != 0 {
		t.Fatalf("root without projects/ returned sessions")
	}
}

func TestScanRefusesBadRoots(t *testing.T) {
	if _, err := Scan(context.Background(), Options{Root: "relative"}); err == nil {
		t.Error("relative root accepted")
	}
	if _, err := Scan(context.Background(), Options{Root: "/tmp/../tmp"}); err == nil {
		t.Error("unclean root accepted")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, "projects")); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(context.Background(), Options{Root: root}); err == nil {
		t.Error("symlinked projects dir accepted")
	}
}

func TestScanUnreadableTranscriptBecomesWarning(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permissions not enforced")
	}
	r := fixture.New(t)
	r.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p"})
	p := r.Session("-p", fixture.ID(2), fixture.Opts{CWD: "/p"})
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	res := scan(t, r.Path)
	if len(res.Sessions) != 1 || len(res.Warnings) != 1 {
		t.Fatalf("sessions=%d warnings=%v", len(res.Sessions), res.Warnings)
	}
}

func TestScanCancelled(t *testing.T) {
	r := fixture.New(t)
	for i := range 20 {
		r.Session("-p", fixture.ID(i+1), fixture.Opts{CWD: "/p"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, Options{Root: r.Path, Alive: noProcs}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestScanProgressAndParallelism(t *testing.T) {
	r := fixture.New(t)
	const n = 50
	for i := range n {
		r.Session("-p", fixture.ID(i+1), fixture.Opts{CWD: "/p", Extra: i%3 == 0})
	}
	var calls, last int
	res, err := Scan(context.Background(), Options{
		Root: r.Path, Alive: noProcs, Workers: 8,
		Progress: func(done, total int) {
			calls++
			if total != n || done != last+1 {
				t.Errorf("progress %d/%d after %d", done, total, last)
			}
			last = done
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != n || calls != n {
		t.Fatalf("sessions=%d progress calls=%d", len(res.Sessions), calls)
	}
}

func TestScanIsReadOnly(t *testing.T) {
	r := fixture.New(t)
	r.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p", Extra: true})
	r.Registry(1, fixture.ID(1), "idle")
	before := r.Snapshot()
	_ = scan(t, r.Path)
	after := r.Snapshot()
	if len(before) != len(after) {
		t.Fatalf("scan changed the tree: %d -> %d entries", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("scan changed %s", k)
		}
	}
}

func TestCollectArtifactsRejectsBadInput(t *testing.T) {
	if _, err := CollectArtifacts("/r", "-p", "../../x", true); err == nil {
		t.Error("bad id accepted")
	}
	if _, err := CollectArtifacts("/r", "..", fixture.ID(1), true); err == nil {
		t.Error("bad project accepted")
	}
}

func TestLiveSessionsMissingRegistry(t *testing.T) {
	r := fixture.New(t)
	live, err := LiveSessions(r.Path, nil)
	if err != nil || len(live) != 0 {
		t.Fatalf("got %v, %v", live, err)
	}
}

func TestLiveSessionsOversizedEntryIgnored(t *testing.T) {
	r := fixture.New(t)
	big := `{"pid":1,"sessionId":"` + fixture.ID(1) + `","pad":"` + strings.Repeat("x", 2<<20) + `"}`
	r.WriteFile("sessions/1.json", []byte(big))
	live, err := LiveSessions(r.Path, func(int) bool { return true })
	if err != nil || len(live) != 0 {
		t.Fatalf("got %v, %v", live, err)
	}
}

func TestProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Error("own process not alive")
	}
	for _, pid := range []int{0, -1, -12345} {
		if ProcessAlive(pid) {
			t.Errorf("pid %d reported alive", pid)
		}
	}
}

func TestLiveReason(t *testing.T) {
	live := map[string]LiveInfo{fixture.ID(1): {PID: 9, Status: "busy"}, fixture.ID(2): {PID: 8}}
	if r := LiveReason(fixture.ID(1), live, time.Time{}, now, time.Minute); !strings.Contains(r, "pid 9, busy") {
		t.Errorf("reason %q", r)
	}
	if r := LiveReason(fixture.ID(2), live, time.Time{}, now, time.Minute); !strings.Contains(r, "pid 8") {
		t.Errorf("reason %q", r)
	}
	if r := LiveReason(fixture.ID(3), live, now.Add(-10*time.Second), now, time.Minute); r == "" {
		t.Error("recent write not flagged")
	}
	if r := LiveReason(fixture.ID(3), live, now.Add(10*time.Second), now, time.Minute); r == "" {
		t.Error("slightly-future mtime (clock skew) not flagged")
	}
	if r := LiveReason(fixture.ID(3), live, now.Add(-2*time.Minute), now, time.Minute); r != "" {
		t.Errorf("old write flagged: %q", r)
	}
	if r := LiveReason(fixture.ID(3), live, now, now, 0); r != "" {
		t.Errorf("disabled window flagged: %q", r)
	}
}
