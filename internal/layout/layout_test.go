package layout

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	id1 = "11111111-2222-4333-8444-555555555555"
	id2 = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

func tree(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mk := func(rel string, dir bool) {
		p := filepath.Join(root, rel)
		if dir {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("projects/-Users-me-code/"+id1+".jsonl", false)
	mk("projects/-Users-me-code/"+id1+"/subagents", true)
	mk("projects/-Users-me-code/memory/MEMORY.md", false)
	mk("file-history/"+id1, true)
	mk("session-env/"+id1, true)
	mk("tasks/"+id1, true)
	mk("todos/"+id1+"-agent-"+id1+".json", false)
	mk("debug/"+id1+".txt", false)
	mk("history.jsonl", false)
	mk("settings.json", false)
	return root
}

func TestIsSessionID(t *testing.T) {
	good := []string{id1, id2, "00000000-0000-0000-0000-000000000000"}
	bad := []string{
		"", "..", ".", "/", "abc",
		strings.ToUpper(id2), // uppercase hex
		id1 + "/",
		"../" + id1,
		id1 + "\x00",
		" " + id1,
		"11111111-2222-4333-8444-55555555555",   // short
		"11111111-2222-4333-8444-5555555555555", // long
		"1111111122224333844455555555555555",    // no dashes
		"g1111111-2222-4333-8444-555555555555",  // not hex
	}
	for _, s := range good {
		if !IsSessionID(s) {
			t.Errorf("IsSessionID(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if IsSessionID(s) {
			t.Errorf("IsSessionID(%q) = true, want false", s)
		}
	}
}

func TestIsProjectDirName(t *testing.T) {
	good := []string{"-Users-me", "-Users-me-code-x-y", "foo", "a"}
	bad := []string{"", ".", "..", "a/b", "a\\b", "a\x00b", ".DS_Store", ".hidden", strings.Repeat("a", 256)}
	for _, s := range good {
		if !IsProjectDirName(s) {
			t.Errorf("IsProjectDirName(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if IsProjectDirName(s) {
			t.Errorf("IsProjectDirName(%q) = true, want false", s)
		}
	}
}

func TestTranscriptAndTodoIDs(t *testing.T) {
	if id, ok := TranscriptID(id1 + ".jsonl"); !ok || id != id1 {
		t.Errorf("TranscriptID: got %q %v", id, ok)
	}
	for _, s := range []string{id1, id1 + ".json", id1 + ".jsonl.bak", "x.jsonl", ".jsonl", "agent-" + id1 + ".jsonl"} {
		if _, ok := TranscriptID(s); ok {
			t.Errorf("TranscriptID(%q) accepted", s)
		}
	}
	if id, ok := TodoSessionID(id1 + "-agent-" + id2 + ".json"); !ok || id != id1 {
		t.Errorf("TodoSessionID: got %q %v", id, ok)
	}
	for _, s := range []string{id1 + ".json", id1 + "-agent-.json", id1 + "-agent-" + id2 + ".jsonl", "x-agent-" + id2 + ".json"} {
		if _, ok := TodoSessionID(s); ok {
			t.Errorf("TodoSessionID(%q) accepted", s)
		}
	}
}

func TestPathBuildersPanicOnBadInput(t *testing.T) {
	cases := map[string]func(){
		"transcript id":      func() { TranscriptPath("/r", "-p", "../x") },
		"transcript project": func() { TranscriptPath("/r", "..", id1) },
		"session dir":        func() { SessionDirPath("/r", "a/b", id1) },
		"global id":          func() { GlobalDirPath("/r", KindFileHistory, "..") },
		"global kind":        func() { GlobalDirPath("/r", KindTranscript, id1) },
		"debug":              func() { DebugLogPath("/r", "x") },
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			f()
		})
	}
}

func TestPathBuildersRoundTripThroughClassify(t *testing.T) {
	root := "/r"
	cases := []struct {
		path string
		kind Kind
	}{
		{TranscriptPath(root, "-p", id1), KindTranscript},
		{SessionDirPath(root, "-p", id1), KindSessionDir},
		{GlobalDirPath(root, KindFileHistory, id1), KindFileHistory},
		{GlobalDirPath(root, KindSessionEnv, id1), KindSessionEnv},
		{GlobalDirPath(root, KindTasks, id1), KindTasks},
		{DebugLogPath(root, id1), KindDebugLog},
		{filepath.Join(root, TodosDir, id1+"-agent-"+id2+".json"), KindTodo},
	}
	for _, c := range cases {
		k, id, err := Classify(root, c.path)
		if err != nil || k != c.kind || id != id1 {
			t.Errorf("Classify(%q) = %v %q %v, want %v %q", c.path, k, id, err, c.kind, id1)
		}
	}
}

func TestClassifyRejects(t *testing.T) {
	root := "/r"
	bad := []string{
		"/r",
		"/r/",
		"/",
		"/etc/passwd",
		"/r2/projects/-p/" + id1 + ".jsonl",
		"/r/../r/projects/-p/" + id1 + ".jsonl",
		"/r/projects",
		"/r/projects/-p",
		"/r/projects/-p/memory",
		"/r/projects/-p/memory/MEMORY.md",
		"/r/projects/../projects/-p/" + id1 + ".jsonl",
		"/r/projects/-p/" + id1 + "/subagents",
		"/r/projects/-p/" + id1 + ".json",
		"/r/projects/./" + id1 + ".jsonl",
		"/r/projects/../" + id1 + ".jsonl",
		"/r/projects/.hidden/" + id1 + ".jsonl",
		"/r/projects/-p/x/" + id1 + ".jsonl",
		"/r/file-history",
		"/r/file-history/" + id1 + "/snapshot",
		"/r/file-history/not-a-uuid",
		"/r/session-env/..",
		"/r/todos",
		"/r/todos/" + id1 + ".json",
		"/r/debug/" + id1 + ".log",
		"/r/debug",
		"/r/history.jsonl",
		"/r/settings.json",
		"/r/sessions/123.json",
		"/r/" + id1,
		"/r/" + id1 + ".jsonl",
		"relative/projects/-p/" + id1 + ".jsonl",
		"",
	}
	for _, p := range bad {
		if k, id, err := Classify(root, p); err == nil {
			t.Errorf("Classify(%q) accepted as %v %q", p, k, id)
		}
	}
	if _, _, err := Classify("relative", "/relative/projects/-p/"+id1+".jsonl"); err == nil {
		t.Error("relative root accepted")
	}
	if _, _, err := Classify("/r/", "/r/projects/-p/"+id1+".jsonl"); err == nil {
		t.Error("unclean root accepted")
	}
}

func TestValidateAcceptsEveryRealArtifact(t *testing.T) {
	root := tree(t)
	paths := map[string]Kind{
		TranscriptPath(root, "-Users-me-code", id1):              KindTranscript,
		SessionDirPath(root, "-Users-me-code", id1):              KindSessionDir,
		GlobalDirPath(root, KindFileHistory, id1):                KindFileHistory,
		GlobalDirPath(root, KindSessionEnv, id1):                 KindSessionEnv,
		GlobalDirPath(root, KindTasks, id1):                      KindTasks,
		filepath.Join(root, TodosDir, id1+"-agent-"+id1+".json"): KindTodo,
		DebugLogPath(root, id1):                                  KindDebugLog,
	}
	for p, want := range paths {
		got, err := Validate(root, p, id1)
		if err != nil || got != want {
			t.Errorf("Validate(%q) = %v, %v; want %v", p, got, err, want)
		}
	}
}

func TestValidateRejectsWrongSession(t *testing.T) {
	root := tree(t)
	_, err := Validate(root, TranscriptPath(root, "-Users-me-code", id1), id2)
	if !errors.Is(err, ErrWrongSession) {
		t.Fatalf("got %v, want ErrWrongSession", err)
	}
	_, err = Validate(root, TranscriptPath(root, "-Users-me-code", id1), "../../etc")
	if !errors.Is(err, ErrWrongSession) {
		t.Fatalf("got %v, want ErrWrongSession for invalid id", err)
	}
}

func TestValidateRejectsMissing(t *testing.T) {
	root := tree(t)
	_, err := Validate(root, TranscriptPath(root, "-Users-me-code", id2), id2)
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("got %v, want ErrMissing", err)
	}
	_, err = Validate(root, TranscriptPath(root, "-Nope", id1), id1)
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("got %v, want ErrMissing for missing project dir", err)
	}
}

func TestValidateRejectsWrongFileType(t *testing.T) {
	root := tree(t)
	p := TranscriptPath(root, "-Users-me-code", id2)
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(root, p, id2); !errors.Is(err, ErrWrongType) {
		t.Errorf("dir as transcript: got %v, want ErrWrongType", err)
	}
	p = GlobalDirPath(root, KindFileHistory, id2)
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(root, p, id2); !errors.Is(err, ErrWrongType) {
		t.Errorf("file as dir: got %v, want ErrWrongType", err)
	}
	p = DebugLogPath(root, id2)
	if err := mkfifo(p); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	if _, err := Validate(root, p, id2); !errors.Is(err, ErrWrongType) {
		t.Errorf("fifo as file: got %v, want ErrWrongType", err)
	}
}

func TestValidateRejectsLeafSymlink(t *testing.T) {
	root := tree(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(target, []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := TranscriptPath(root, "-Users-me-code", id2)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(root, link, id2); !errors.Is(err, ErrSymlink) {
		t.Fatalf("got %v, want ErrSymlink", err)
	}
	dirLink := GlobalDirPath(root, KindFileHistory, id2)
	if err := os.Symlink(outside, dirLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(root, dirLink, id2); !errors.Is(err, ErrSymlink) {
		t.Fatalf("dir link: got %v, want ErrSymlink", err)
	}
}

func TestValidateRejectsParentSymlinkEscape(t *testing.T) {
	root := tree(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, id2+".jsonl"), []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ProjectsDir, "-Evil")); err != nil {
		t.Fatal(err)
	}
	p := TranscriptPath(root, "-Evil", id2)
	if _, err := Validate(root, p, id2); !errors.Is(err, ErrSymlink) {
		t.Fatalf("got %v, want ErrSymlink", err)
	}

	root2 := tree(t)
	fh := filepath.Join(root2, FileHistoryDir)
	if err := os.Rename(fh, fh+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fh+".real", fh); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(root2, GlobalDirPath(root2, KindFileHistory, id1), id1); !errors.Is(err, ErrSymlink) {
		t.Fatalf("top-level link: got %v, want ErrSymlink", err)
	}
}

func TestValidateRejectsSymlinkedRoot(t *testing.T) {
	real := tree(t)
	link := filepath.Join(t.TempDir(), "claude-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(link, TranscriptPath(link, "-Users-me-code", id1), id1); !errors.Is(err, ErrSymlink) {
		t.Fatalf("got %v, want ErrSymlink (callers must pass the resolved root)", err)
	}
	resolved, err := ResolveRoot(link)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != real {
		t.Fatalf("ResolveRoot = %q, want %q", resolved, real)
	}
	if _, err := Validate(resolved, TranscriptPath(resolved, "-Users-me-code", id1), id1); err != nil {
		t.Fatalf("resolved root rejected: %v", err)
	}
}

func TestResolveRootErrors(t *testing.T) {
	if _, err := ResolveRoot(""); err == nil {
		t.Error("empty accepted")
	}
	if _, err := ResolveRoot(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing accepted")
	}
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRoot(f); err == nil {
		t.Error("file accepted")
	}
}

func TestEncodeProjectDir(t *testing.T) {
	cases := map[string]string{
		"/Users/me":                        "-Users-me",
		"/Users/me/code/theydo/journey-ai": "-Users-me-code-theydo-journey-ai",
		"/Users/me/my.app/x_y":             "-Users-me-my-app-x-y",
		"/Users/me/Ünïcode dir":            "-Users-me--n-code-dir",
	}
	for in, want := range cases {
		if got := EncodeProjectDir(in); got != want {
			t.Errorf("EncodeProjectDir(%q) = %q, want %q", in, got, want)
		}
	}
	if got := DecodeProjectDirGuess("-Users-me-code"); got != "/Users/me/code" {
		t.Errorf("DecodeProjectDirGuess = %q", got)
	}
	if got := DecodeProjectDirGuess("plain"); got != "plain" {
		t.Errorf("DecodeProjectDirGuess(plain) = %q", got)
	}
}

func TestKindProperties(t *testing.T) {
	for k := KindTranscript; k <= KindDebugLog; k++ {
		if strings.HasPrefix(k.String(), "kind(") {
			t.Errorf("kind %d has no label", k)
		}
	}
	if Kind(99).String() != "kind(99)" {
		t.Error("unknown kind label")
	}
	if KindTranscript.IsGlobal() || KindSessionDir.IsGlobal() {
		t.Error("project-scoped kinds reported global")
	}
	if !KindFileHistory.IsGlobal() || !KindTodo.IsGlobal() {
		t.Error("global kinds not reported global")
	}
}

func FuzzClassify(f *testing.F) {
	seeds := []string{
		"projects/-p/" + id1 + ".jsonl",
		"projects/-p/" + id1,
		"file-history/" + id1,
		"todos/" + id1 + "-agent-" + id2 + ".json",
		"debug/" + id1 + ".txt",
		"../etc/passwd",
		"projects/../../" + id1 + ".jsonl",
		"projects//" + id1,
		"",
		".",
		"projects/-p/memory",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	const root = "/claude/root"
	f.Fuzz(func(t *testing.T, rel string) {
		p := root + "/" + rel
		kind, id, err := Classify(root, p)
		if err != nil {
			return
		}
		if !strings.HasPrefix(p, root+"/") {
			t.Fatalf("accepted path outside root: %q", p)
		}
		if filepath.Clean(p) != p {
			t.Fatalf("accepted unclean path: %q", p)
		}
		for _, e := range strings.Split(rel, "/") {
			if e == "" || e == "." || e == ".." {
				t.Fatalf("accepted path with bad element: %q", p)
			}
		}
		if !IsSessionID(id) || !strings.Contains(rel, id) {
			t.Fatalf("accepted %q with bad id %q", p, id)
		}
		if kind < KindTranscript || kind > KindDebugLog {
			t.Fatalf("unknown kind %v", kind)
		}
		depth := strings.Count(rel, "/") + 1
		if depth != 2 && depth != 3 {
			t.Fatalf("accepted depth %d: %q", depth, p)
		}
	})
}
