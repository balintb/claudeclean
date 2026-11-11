// Command claudeclean lists Claude Code sessions and moves chosen ones to the Trash.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/balintb/claudeclean/internal/cleaner"
	"github.com/balintb/claudeclean/internal/format"
	"github.com/balintb/claudeclean/internal/layout"
	"github.com/balintb/claudeclean/internal/session"
	"github.com/balintb/claudeclean/internal/trash"
	"github.com/balintb/claudeclean/internal/ui"
)

// Stamped by GoReleaser from the release tag; empty otherwise.
var version = ""

// resolveVersion prefers the stamped tag, then the module version recorded by
// `go install`, then the VCS revision of a checkout build.
func resolveVersion() string {
	v := version
	if v == "" {
		v = versionFromBuild()
	}
	if v == "" {
		return "dev"
	}
	if v[0] >= '0' && v[0] <= '9' && strings.Contains(v, ".") {
		v = "v" + v
	}
	return v
}

func isPseudoVersion(v string) bool {
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '-' || r == '.' || r == '+' })
	for _, part := range parts {
		if len(part) != 14 {
			continue
		}
		if strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
			return true
		}
	}
	return false
}

func versionFromBuild() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" && !isPseudoVersion(v) {
		return v
	}
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return ""
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("claudeclean", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		rootFlag = fs.String("root", "", "Claude config directory (default: $CLAUDE_CONFIG_DIR or ~/.claude)")
		list     = fs.Bool("list", false, "print sessions as a table and exit")
		dryRun   = fs.Bool("dry-run", false, "never move anything; show what would be trashed")
		logPath  = fs.String("log", "", "where to record trashed items (default: ~/Library/Logs/claudeclean/trash-log.jsonl)")
		showVer  = fs.Bool("version", false, "print version and exit")
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "claudeclean: browse Claude Code sessions and move unwanted ones to the Trash.\n\n")
		fmt.Fprintf(stderr, "Usage:\n  claudeclean [flags]\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(stderr, "\nNothing is ever deleted permanently: items go to the macOS Trash.\n")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "claudeclean: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return 2
	}
	if *showVer {
		fmt.Fprintf(stdout, "claudeclean %s\n", resolveVersion())
		return 0
	}

	root, err := resolveRoot(*rootFlag)
	if err != nil {
		fmt.Fprintf(stderr, "claudeclean: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	loader := func(ctx context.Context) (*session.Result, error) {
		return session.Scan(ctx, session.Options{Root: root})
	}

	if *list {
		res, err := loader(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "claudeclean: %v\n", err)
			return 1
		}
		printList(stdout, res, time.Now())
		return 0
	}

	if !term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintln(stderr, "claudeclean: the interactive UI needs a terminal; use --list for plain output")
		return 1
	}

	journal := *logPath
	if journal == "" {
		journal = defaultJournalPath()
	}
	c := &cleaner.Cleaner{
		Root:        root,
		Trasher:     trash.New(),
		DryRun:      *dryRun,
		JournalPath: journal,
	}
	m := ui.New(ui.Options{
		Root:    root,
		Load:    loader,
		Deleter: c,
		DryRun:  *dryRun,
		Journal: journal,
		Home:    homeDir(),
	})
	p := tea.NewProgram(m, tea.WithContext(ctx))
	if _, err := p.Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "claudeclean: %v\n", err)
		return 1
	}
	return 0
}

func resolveRoot(flagValue string) (string, error) {
	dir := flagValue
	if dir == "" {
		dir = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot find home directory: %w", err)
		}
		dir = filepath.Join(home, ".claude")
	}
	if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(homeDir(), dir[2:])
	}
	root, err := layout.ResolveRoot(dir)
	if err != nil {
		return "", fmt.Errorf("claude config directory: %w", err)
	}
	return root, nil
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func defaultJournalPath() string {
	return filepath.Join(homeDir(), "Library", "Logs", "claudeclean", "trash-log.jsonl")
}

func printList(w io.Writer, res *session.Result, now time.Time) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "LAST ACTIVE\tSIZE\tMSGS\tSTATE\tFOLDER\tTITLE\tID")
	home := homeDir()
	var total int64
	for _, s := range res.Sessions {
		state := "-"
		switch {
		case s.Live:
			state = "live"
		case !s.CWDExists:
			state = "no-folder"
		}
		total += s.Size
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
			format.Ago(s.LastActive, now),
			format.Bytes(s.Size),
			s.Messages,
			state,
			format.Truncate(format.TildePath(s.CWD, home), 50),
			format.Truncate(s.Title, 60),
			s.ID,
		)
	}
	tw.Flush()
	fmt.Fprintf(w, "\n%d sessions, %s\n", len(res.Sessions), format.Bytes(total))
	for _, warn := range res.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warn)
	}
}
