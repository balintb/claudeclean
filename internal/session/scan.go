package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/balintb/claudeclean/internal/layout"
)

type Options struct {
	Root         string // must be resolved with layout.ResolveRoot
	Workers      int
	Now          func() time.Time
	Alive        func(int) bool
	RecentWindow time.Duration // 0 means DefaultRecentWindow; <0 disables
	Progress     func(done, total int)
}

func (o *Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *Options) window() time.Duration {
	switch {
	case o.RecentWindow == 0:
		return DefaultRecentWindow
	case o.RecentWindow < 0:
		return 0
	default:
		return o.RecentWindow
	}
}

type Result struct {
	Root     string
	Sessions []*Session
	Warnings []string
}

type candidate struct {
	project string
	id      string
	path    string
	modTime time.Time
	size    int64
}

func Scan(ctx context.Context, opt Options) (*Result, error) {
	root := opt.Root
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, fmt.Errorf("scan: root %q must be absolute and clean", root)
	}
	res := &Result{Root: root}

	projectsDir := filepath.Join(root, layout.ProjectsDir)
	fi, err := os.Lstat(projectsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return nil, fmt.Errorf("scan: %s is not a real directory", projectsDir)
	}

	cands, warns, err := findTranscripts(projectsDir)
	if err != nil {
		return nil, err
	}
	res.Warnings = append(res.Warnings, warns...)

	idCount := make(map[string]int, len(cands))
	for _, c := range cands {
		idCount[c.id]++
	}

	live, err := LiveSessions(root, opt.Alive)
	if err != nil {
		res.Warnings = append(res.Warnings, err.Error())
		live = map[string]LiveInfo{}
	}

	sessions := make([]*Session, len(cands))
	errs := make([]error, len(cands))
	workers := opt.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, max(len(cands), 1))

	var (
		progressMu sync.Mutex
		done       int
		wg         sync.WaitGroup
		next       = make(chan int)
		cwdCache   = newExistsCache()
	)
	for range workers {
		wg.Go(func() {
			for i := range next {
				s, err := buildSession(root, cands[i], idCount[cands[i].id] > 1, cwdCache)
				sessions[i], errs[i] = s, err
				if opt.Progress != nil {
					progressMu.Lock()
					done++
					opt.Progress(done, len(cands))
					progressMu.Unlock()
				}
			}
		})
	}
feed:
	for i := range cands {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	now := opt.now()
	window := opt.window()
	for i, s := range sessions {
		if errs[i] != nil {
			res.Warnings = append(res.Warnings, errs[i].Error())
			continue
		}
		if reason := LiveReason(s.ID, live, cands[i].modTime, now, window); reason != "" {
			s.Live, s.LiveReason = true, reason
		}
		res.Sessions = append(res.Sessions, s)
	}
	SortByRecent(res.Sessions)
	return res, nil
}

func SortByRecent(ss []*Session) {
	sort.SliceStable(ss, func(i, j int) bool {
		a, b := ss[i], ss[j]
		if !a.LastActive.Equal(b.LastActive) {
			return a.LastActive.After(b.LastActive)
		}
		return a.Key() < b.Key()
	})
}

func findTranscripts(projectsDir string) ([]candidate, []string, error) {
	projects, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("scan: %w", err)
	}
	var (
		out   []candidate
		warns []string
	)
	for _, p := range projects {
		// DirEntry types come from lstat, so symlinked projects are skipped.
		if !p.IsDir() || !layout.IsProjectDirName(p.Name()) {
			continue
		}
		dir := filepath.Join(projectsDir, p.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			warns = append(warns, fmt.Sprintf("skipping %s: %v", dir, err))
			continue
		}
		for _, e := range entries {
			id, ok := layout.TranscriptID(e.Name())
			if !ok || !e.Type().IsRegular() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue // vanished between ReadDir and stat
			}
			out = append(out, candidate{
				project: p.Name(),
				id:      id,
				path:    filepath.Join(dir, e.Name()),
				modTime: info.ModTime(),
				size:    info.Size(),
			})
		}
	}
	return out, warns, nil
}

func buildSession(root string, c candidate, shared bool, cwdCache *existsCache) (*Session, error) {
	f, err := os.Open(c.path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", c.path, err)
	}
	m, err := parseTranscript(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", c.path, err)
	}

	s := &Session{
		ID:           c.id,
		ProjectDir:   c.project,
		GitBranch:    m.gitBranch,
		Version:      m.version,
		FirstPrompt:  m.firstPrompt,
		Messages:     m.messages,
		Prompts:      m.prompts,
		Started:      m.started,
		LastActive:   m.lastActive,
		SharedID:     shared,
		SkippedLines: m.skipped,
	}
	s.LastPrompt = m.lastPrompt
	if s.LastPrompt == "" {
		s.LastPrompt = m.lastTyped
	}
	if s.LastActive.IsZero() || c.modTime.After(s.LastActive) {
		s.LastActive = c.modTime
	}
	if s.Started.IsZero() {
		s.Started = s.LastActive
	}

	s.CWD, s.CWDGuessed = pickCWD(c.project, m.cwds)
	s.CWDExists = cwdCache.isDir(s.CWD)
	s.Title, s.TitleSource = pickTitle(m)

	arts, err := CollectArtifacts(root, c.project, c.id, !shared)
	if err != nil {
		return nil, err
	}
	s.Artifacts = arts
	for _, a := range arts {
		s.Size += a.Size
	}
	return s, nil
}

func pickCWD(project string, cands []string) (string, bool) {
	for _, c := range cands {
		if layout.EncodeProjectDir(c) == project {
			return c, false
		}
	}
	if len(cands) > 0 {
		return cands[0], false
	}
	return layout.DecodeProjectDirGuess(project), true
}

func pickTitle(m meta) (string, TitleSource) {
	switch {
	case m.customTitle != "":
		return m.customTitle, TitleCustom
	case m.aiTitle != "":
		return m.aiTitle, TitleAI
	case m.summary != "":
		return m.summary, TitleSummary
	case m.agentName != "":
		return m.agentName, TitleAgent
	case m.firstPrompt != "":
		return m.firstPrompt, TitlePrompt
	case m.firstCommand != "":
		return m.firstCommand, TitleCommand
	default:
		return "Untitled session", TitleNone
	}
}

func CollectArtifacts(root, project, id string, includeGlobal bool) ([]Artifact, error) {
	if !layout.IsSessionID(id) || !layout.IsProjectDirName(project) {
		return nil, fmt.Errorf("invalid session %q in %q", id, project)
	}
	var out []Artifact
	addFile := func(kind layout.Kind, p string) {
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() {
			return
		}
		out = append(out, Artifact{Kind: kind, Path: p, Size: fi.Size(), Files: 1})
	}
	addDir := func(kind layout.Kind, p string) {
		fi, err := os.Lstat(p)
		if err != nil || !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 {
			return
		}
		size, files := dirUsage(p)
		out = append(out, Artifact{Kind: kind, Path: p, Size: size, Files: files})
	}

	addFile(layout.KindTranscript, layout.TranscriptPath(root, project, id))
	addDir(layout.KindSessionDir, layout.SessionDirPath(root, project, id))
	if !includeGlobal {
		return out, nil
	}
	addDir(layout.KindFileHistory, layout.GlobalDirPath(root, layout.KindFileHistory, id))
	addDir(layout.KindSessionEnv, layout.GlobalDirPath(root, layout.KindSessionEnv, id))
	addDir(layout.KindTasks, layout.GlobalDirPath(root, layout.KindTasks, id))

	todos := filepath.Join(root, layout.TodosDir)
	if ents, err := os.ReadDir(todos); err == nil {
		for _, e := range ents {
			if owner, ok := layout.TodoSessionID(e.Name()); ok && owner == id {
				addFile(layout.KindTodo, filepath.Join(todos, e.Name()))
			}
		}
	}
	addFile(layout.KindDebugLog, layout.DebugLogPath(root, id))
	return out, nil
}

func dirUsage(dir string) (size int64, files int) {
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				size += info.Size()
				files++
			}
		}
		return nil
	})
	return size, files
}

type existsCache struct {
	mu sync.Mutex
	m  map[string]bool
}

func newExistsCache() *existsCache { return &existsCache{m: map[string]bool{}} }

func (c *existsCache) isDir(p string) bool {
	if p == "" || !filepath.IsAbs(p) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.m[p]; ok {
		return v
	}
	fi, err := os.Stat(p)
	v := err == nil && fi.IsDir()
	c.m[p] = v
	return v
}
