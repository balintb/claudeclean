package cleaner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/balintb/claudeclean/internal/layout"
	"github.com/balintb/claudeclean/internal/session"
	"github.com/balintb/claudeclean/internal/trash"
)

type Status int

const (
	StatusTrashed Status = iota
	StatusDryRun
	StatusSkipped // live or cancelled
	StatusFailed
)

func (s Status) String() string {
	switch s {
	case StatusTrashed:
		return "trashed"
	case StatusDryRun:
		return "dry run"
	case StatusSkipped:
		return "skipped"
	case StatusFailed:
		return "failed"
	default:
		return fmt.Sprintf("status(%d)", int(s))
	}
}

type Item struct {
	Kind      layout.Kind
	Original  string
	TrashedAs string // "" if unknown or dry run
	Size      int64
}

type Outcome struct {
	Session *session.Session
	Status  Status
	Items   []Item
	Err     error
}

type Report struct {
	Outcomes   []Outcome
	JournalErr error // trashing still happened
}

func (r Report) Count(s Status) int {
	n := 0
	for _, o := range r.Outcomes {
		if o.Status == s {
			n++
		}
	}
	return n
}

type Event struct {
	Done, Total int
	Outcome     Outcome
}

var (
	ErrLive        = errors.New("session is in use")
	ErrCancelled   = errors.New("cancelled before this session was reached")
	ErrGone        = errors.New("transcript no longer exists")
	ErrStillThere  = errors.New("item still present after moving it to the Trash")
	ErrNoTrash     = errors.New("no Trash backend configured")
	ErrRootChanged = errors.New("claude config root is not a resolved directory")
)

type Cleaner struct {
	Root    string // resolved with layout.ResolveRoot
	Trasher trash.Trasher
	DryRun  bool

	JournalPath string

	// Test hooks; nil means the real implementation.
	Now          func() time.Time
	Alive        func(int) bool
	RecentWindow time.Duration // 0 means session.DefaultRecentWindow; <0 disables
}

func (c *Cleaner) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Cleaner) window() time.Duration {
	switch {
	case c.RecentWindow == 0:
		return session.DefaultRecentWindow
	case c.RecentWindow < 0:
		return 0
	default:
		return c.RecentWindow
	}
}

func (c *Cleaner) Run(ctx context.Context, sessions []*session.Session, progress func(Event)) Report {
	var rep Report
	unique := dedupe(sessions)
	emit := func(o Outcome) {
		rep.Outcomes = append(rep.Outcomes, o)
		if progress != nil {
			progress(Event{Done: len(rep.Outcomes), Total: len(unique), Outcome: o})
		}
	}
	failAll := func(err error) Report {
		for _, s := range unique {
			emit(Outcome{Session: s, Status: StatusFailed, Err: err})
		}
		return rep
	}

	if c.Trasher == nil && !c.DryRun {
		return failAll(ErrNoTrash)
	}
	if resolved, err := layout.ResolveRoot(c.Root); err != nil || resolved != c.Root {
		return failAll(fmt.Errorf("%w: %q", ErrRootChanged, c.Root))
	}
	live, err := session.LiveSessions(c.Root, c.Alive)
	if err != nil {
		return failAll(fmt.Errorf("cannot tell which sessions are running: %w", err))
	}

	j := &journal{path: c.JournalPath}
	for _, s := range unique {
		if ctx.Err() != nil {
			emit(Outcome{Session: s, Status: StatusSkipped, Err: ErrCancelled})
			continue
		}
		emit(c.one(s, live, j))
	}
	closeErr := j.close()
	switch {
	case j.err != nil:
		rep.JournalErr = j.err
	case closeErr != nil:
		rep.JournalErr = fmt.Errorf("journal: %w", closeErr)
	}
	return rep
}

func (c *Cleaner) one(s *session.Session, live map[string]session.LiveInfo, j *journal) Outcome {
	out := Outcome{Session: s, Status: StatusFailed}
	if s == nil || !layout.IsSessionID(s.ID) || !layout.IsProjectDirName(s.ProjectDir) {
		out.Err = fmt.Errorf("invalid session")
		return out
	}

	transcript := layout.TranscriptPath(c.Root, s.ProjectDir, s.ID)
	fi, err := os.Lstat(transcript)
	if errors.Is(err, fs.ErrNotExist) {
		out.Err = ErrGone
		return out
	}
	if err != nil {
		out.Err = err
		return out
	}
	if reason := session.LiveReason(s.ID, live, fi.ModTime(), c.now(), c.window()); reason != "" {
		out.Status = StatusSkipped
		out.Err = fmt.Errorf("%w: %s", ErrLive, reason)
		return out
	}

	shared, err := c.idElsewhere(s.ProjectDir, s.ID)
	if err != nil {
		out.Err = err
		return out
	}
	arts, err := session.CollectArtifacts(c.Root, s.ProjectDir, s.ID, !shared)
	if err != nil {
		out.Err = err
		return out
	}
	ordered := transcriptLast(arts)
	if len(ordered) == 0 || ordered[len(ordered)-1].Kind != layout.KindTranscript {
		out.Err = ErrGone
		return out
	}

	// Validate everything first so a doomed session moves nothing.
	for _, a := range ordered {
		kind, err := layout.Validate(c.Root, a.Path, s.ID)
		if err != nil {
			out.Err = err
			return out
		}
		if kind != a.Kind {
			out.Err = fmt.Errorf("%s: expected %v, found %v", a.Path, a.Kind, kind)
			return out
		}
	}

	if c.DryRun {
		for _, a := range ordered {
			out.Items = append(out.Items, Item{Kind: a.Kind, Original: a.Path, Size: a.Size})
		}
		out.Status = StatusDryRun
		return out
	}

	for _, a := range ordered {
		// Re-validate right before the move to keep the race window small.
		if _, err := layout.Validate(c.Root, a.Path, s.ID); err != nil {
			out.Err = err
			return out
		}
		dest, err := c.Trasher.Trash(a.Path)
		if err != nil {
			out.Err = err
			return out
		}
		if _, err := os.Lstat(a.Path); !errors.Is(err, fs.ErrNotExist) {
			out.Err = fmt.Errorf("%w: %s", ErrStillThere, a.Path)
			return out
		}
		item := Item{Kind: a.Kind, Original: a.Path, TrashedAs: dest, Size: a.Size}
		out.Items = append(out.Items, item)
		j.write(s, item, c.now())
	}
	out.Status = StatusTrashed
	return out
}

func (c *Cleaner) idElsewhere(project, id string) (bool, error) {
	dir := filepath.Join(c.Root, layout.ProjectsDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range ents {
		if e.Name() == project || !layout.IsProjectDirName(e.Name()) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, e.Name(), id+".jsonl")); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// The transcript goes last so a partial failure leaves the session listed
// and retryable.
func transcriptLast(arts []session.Artifact) []session.Artifact {
	out := make([]session.Artifact, 0, len(arts))
	var tr []session.Artifact
	for _, a := range arts {
		if a.Kind == layout.KindTranscript {
			tr = append(tr, a)
			continue
		}
		out = append(out, a)
	}
	return append(out, tr...)
}

func dedupe(in []*session.Session) []*session.Session {
	seen := make(map[string]bool, len(in))
	out := make([]*session.Session, 0, len(in))
	for _, s := range in {
		if s == nil {
			continue
		}
		if seen[s.Key()] {
			continue
		}
		seen[s.Key()] = true
		out = append(out, s)
	}
	return out
}

// Journal failures are reported but never stop the cleanup.
type journal struct {
	path string
	mu   sync.Mutex
	f    *os.File
	err  error
}

type journalEntry struct {
	Time      time.Time `json:"time"`
	SessionID string    `json:"sessionId"`
	Project   string    `json:"project"`
	CWD       string    `json:"cwd,omitempty"`
	Title     string    `json:"title,omitempty"`
	Kind      string    `json:"kind"`
	Original  string    `json:"original"`
	TrashedAs string    `json:"trashedAs,omitempty"`
	Size      int64     `json:"size"`
}

func (j *journal) write(s *session.Session, it Item, now time.Time) {
	if j.path == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.err != nil {
		return
	}
	if j.f == nil {
		if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
			j.err = fmt.Errorf("journal: %w", err)
			return
		}
		f, err := os.OpenFile(j.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
		if err != nil {
			j.err = fmt.Errorf("journal: %w", err)
			return
		}
		j.f = f
	}
	b, err := json.Marshal(journalEntry{
		Time:      now.UTC(),
		SessionID: s.ID,
		Project:   s.ProjectDir,
		CWD:       s.CWD,
		Title:     s.Title,
		Kind:      it.Kind.String(),
		Original:  it.Original,
		TrashedAs: it.TrashedAs,
		Size:      it.Size,
	})
	if err != nil {
		j.err = fmt.Errorf("journal: %w", err)
		return
	}
	if _, err := j.f.Write(append(b, '\n')); err != nil {
		j.err = fmt.Errorf("journal: %w", err)
	}
}

func (j *journal) close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}
