// Package fixture builds fake Claude config dirs for tests.
package fixture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type Root struct {
	t    testing.TB
	Path string
}

func New(t testing.TB) *Root {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "claude")
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Root{t: t, Path: root}
}

func ID(n int) string {
	return fmt.Sprintf("%08x-0000-4000-8000-%012x", n, n)
}

func (r *Root) Join(parts ...string) string {
	return filepath.Join(append([]string{r.Path}, parts...)...)
}

func (r *Root) WriteFile(rel string, data []byte) string {
	r.t.Helper()
	p := r.Join(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		r.t.Fatal(err)
	}
	return p
}

func (r *Root) Mkdir(rel string) string {
	r.t.Helper()
	p := r.Join(rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		r.t.Fatal(err)
	}
	return p
}

type Line map[string]any

func JSONL(t testing.TB, lines ...Line) []byte {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		enc, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(enc)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func (r *Root) Transcript(project, id string, lines ...Line) string {
	r.t.Helper()
	return r.WriteFile(filepath.Join("projects", project, id+".jsonl"), JSONL(r.t, lines...))
}

type Opts struct {
	CWD     string
	Title   string
	Prompt  string
	Branch  string
	Started time.Time
	Extra   bool // also write session dir, file history, env, todo and debug log
	ModTime time.Time
}

func (r *Root) Session(project, id string, o Opts) string {
	r.t.Helper()
	if o.Started.IsZero() {
		o.Started = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	}
	if o.Prompt == "" {
		o.Prompt = "hello there"
	}
	ts := func(d time.Duration) string { return o.Started.Add(d).Format(time.RFC3339Nano) }
	lines := []Line{
		{"type": "user", "timestamp": ts(0), "cwd": o.CWD, "gitBranch": o.Branch, "version": "2.1.0",
			"sessionId": id, "message": Line{"role": "user", "content": o.Prompt}},
		{"type": "assistant", "timestamp": ts(time.Second), "cwd": o.CWD, "sessionId": id,
			"message": Line{"role": "assistant", "content": []Line{{"type": "text", "text": "hi"}}}},
		{"type": "user", "timestamp": ts(2 * time.Second), "cwd": o.CWD, "sessionId": id,
			"message": Line{"role": "user", "content": []Line{{"type": "tool_result", "content": "ok"}}}},
		{"type": "assistant", "timestamp": ts(time.Minute), "cwd": o.CWD, "sessionId": id,
			"message": Line{"role": "assistant", "content": []Line{{"type": "text", "text": "done"}}}},
	}
	if o.Title != "" {
		lines = append(lines, Line{"type": "ai-title", "aiTitle": o.Title, "sessionId": id})
	}
	p := r.Transcript(project, id, lines...)
	mt := o.ModTime
	if mt.IsZero() {
		mt = o.Started.Add(time.Minute)
	}
	if err := os.Chtimes(p, mt, mt); err != nil {
		r.t.Fatal(err)
	}
	if o.Extra {
		r.WriteFile(filepath.Join("projects", project, id, "subagents", "agent-a.jsonl"), []byte("{}\n"))
		r.WriteFile(filepath.Join("projects", project, id, "tool-results", "r.txt"), []byte("result"))
		r.WriteFile(filepath.Join("file-history", id, "abc@v1"), []byte("old file"))
		r.Mkdir(filepath.Join("session-env", id))
		r.WriteFile(filepath.Join("todos", id+"-agent-"+id+".json"), []byte("[]"))
		r.WriteFile(filepath.Join("debug", id+".txt"), []byte("debug"))
	}
	return p
}

func (r *Root) Registry(pid int, id, status string) {
	r.t.Helper()
	b, err := json.Marshal(map[string]any{"pid": pid, "sessionId": id, "status": status})
	if err != nil {
		r.t.Fatal(err)
	}
	r.WriteFile(filepath.Join("sessions", fmt.Sprintf("%d.json", pid)), b)
}

func (r *Root) Exists(rel string) bool {
	_, err := os.Lstat(r.Join(rel))
	return err == nil
}

func (r *Root) Snapshot() map[string]int64 {
	r.t.Helper()
	out := map[string]int64{}
	err := filepath.Walk(r.Path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(r.Path, p)
		out[rel] = info.Size()
		if info.IsDir() {
			out[rel] = -1
		}
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}
