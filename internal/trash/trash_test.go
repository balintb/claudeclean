package trash

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckPathRejectsDangerousInput(t *testing.T) {
	for _, p := range []string{"", "relative/file", "./x", "/", "/a/../b", "/a/b/", "/a//b", "/a/\x00b"} {
		if err := checkPath(p); err == nil {
			t.Errorf("checkPath(%q) accepted", p)
		}
	}
	if err := checkPath("/Users/me/.claude/projects/x.jsonl"); err != nil {
		t.Errorf("valid path rejected: %v", err)
	}
}

func TestBackendRejectsBadPathsBeforeDoingAnything(t *testing.T) {
	tr := New()
	for _, p := range []string{"", "relative", "/", "/x/../y"} {
		if _, err := tr.Trash(p); err == nil {
			t.Errorf("Trash(%q) succeeded", p)
		}
	}
}

func TestBackendFailsForMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := New().Trash(missing); err == nil {
		t.Fatal("trashing a missing path succeeded")
	}
}

func TestRealTrash(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	if os.Getenv("CLAUDECLEAN_TEST_REAL_TRASH") != "1" {
		t.Skip("set CLAUDECLEAN_TEST_REAL_TRASH=1 to run")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "claudeclean-trash-test.txt")
	sub := filepath.Join(dir, "claudeclean-trash-test-dir")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sub, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{file, sub} {
		dest, err := New().Trash(p)
		if err != nil {
			t.Fatalf("Trash(%s): %v", p, err)
		}
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after trashing (err=%v)", p, err)
		}
		t.Logf("%s -> %s", p, dest)
		if dest != "" && !strings.Contains(dest, ".Trash") {
			t.Errorf("unexpected destination %q", dest)
		}
	}
}
