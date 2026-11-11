package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/balintb/claudeclean/internal/fixture"
	"github.com/balintb/claudeclean/internal/session"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersionAndHelp(t *testing.T) {
	code, out, _ := runCLI(t, "--version")
	if code != 0 || !strings.HasPrefix(out, "claudeclean ") {
		t.Fatalf("version: %d %q", code, out)
	}
	code, _, errOut := runCLI(t, "--help")
	if code != 0 || !strings.Contains(errOut, "-dry-run") || !strings.Contains(errOut, "Trash") {
		t.Fatalf("help: %d %q", code, errOut)
	}
}

func TestBadArguments(t *testing.T) {
	if code, _, _ := runCLI(t, "--nope"); code != 2 {
		t.Errorf("unknown flag exit %d", code)
	}
	if code, _, errOut := runCLI(t, "delete-everything"); code != 2 || !strings.Contains(errOut, "unexpected argument") {
		t.Errorf("positional arg exit %d %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "--root", filepath.Join(t.TempDir(), "missing"), "--list"); code != 1 || !strings.Contains(errOut, "claude config directory") {
		t.Errorf("missing root exit %d %q", code, errOut)
	}
}

func TestListPrintsSessions(t *testing.T) {
	r := fixture.New(t)
	work := t.TempDir()
	r.Session("-proj", fixture.ID(1), fixture.Opts{CWD: work, Title: "Listed session", Extra: true})
	r.Session("-gone", fixture.ID(2), fixture.Opts{CWD: "/definitely/not/here", Prompt: "orphan prompt"})
	before := r.Snapshot()

	code, out, errOut := runCLI(t, "--root", r.Path, "--list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"LAST ACTIVE", "Listed session", "orphan prompt", "no-folder", fixture.ID(1), "2 sessions"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if after := r.Snapshot(); len(after) != len(before) {
		t.Fatal("--list modified the config directory")
	}
}

func TestListHonoursEnvAndTilde(t *testing.T) {
	r := fixture.New(t)
	r.Session("-p", fixture.ID(1), fixture.Opts{CWD: "/p", Title: "From env"})
	t.Setenv("CLAUDE_CONFIG_DIR", r.Path)
	code, out, errOut := runCLI(t, "--list")
	if code != 0 || !strings.Contains(out, "From env") {
		t.Fatalf("env root: %d %q %q", code, out, errOut)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Symlink(r.Path, filepath.Join(home, "claude-link")); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runCLI(t, "--root", "~/claude-link", "--list")
	if code != 0 || !strings.Contains(out, "From env") {
		t.Fatalf("tilde root: %d %q %q", code, out, errOut)
	}
}

func TestInteractiveNeedsTerminal(t *testing.T) {
	r := fixture.New(t)
	code, _, errOut := runCLI(t, "--root", r.Path)
	if code != 1 || !strings.Contains(errOut, "needs a terminal") {
		t.Fatalf("exit %d %q", code, errOut)
	}
}

func TestPrintListStates(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	res := &session.Result{
		Sessions: []*session.Session{
			{ID: fixture.ID(1), Title: "live one", Live: true, CWDExists: true, LastActive: now, CWD: "/a"},
			{ID: fixture.ID(2), Title: "normal", CWDExists: true, LastActive: now.Add(-time.Hour), CWD: "/b"},
		},
		Warnings: []string{"skipping something"},
	}
	var b bytes.Buffer
	printList(&b, res, now)
	out := b.String()
	if !strings.Contains(out, "live") || !strings.Contains(out, "warning: skipping something") || !strings.Contains(out, "1h ago") {
		t.Fatalf("output:\n%s", out)
	}
}
