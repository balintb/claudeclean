package session

import (
	"time"

	"github.com/balintb/claudeclean/internal/layout"
)

type Artifact struct {
	Kind  layout.Kind
	Path  string
	Size  int64
	Files int
}

type TitleSource int

const (
	TitleNone TitleSource = iota
	TitleCustom
	TitleAI
	TitleSummary
	TitleAgent
	TitlePrompt
	TitleCommand
)

type Session struct {
	ID         string
	ProjectDir string // directory name under projects/

	CWD        string
	CWDGuessed bool
	CWDExists  bool

	Title       string
	TitleSource TitleSource
	FirstPrompt string
	LastPrompt  string
	GitBranch   string
	Version     string

	Started    time.Time
	LastActive time.Time

	Messages int // user and assistant records on the main thread
	Prompts  int // prompts typed by a person

	Live       bool
	LiveReason string

	// Same ID exists in another project dir, so global artifacts are left alone.
	SharedID bool

	Artifacts []Artifact
	Size      int64

	SkippedLines int
}

func (s *Session) Key() string { return s.ProjectDir + "/" + s.ID }

func (s *Session) TranscriptPath(root string) string {
	return layout.TranscriptPath(root, s.ProjectDir, s.ID)
}

func (s *Session) FileCount() int {
	n := 0
	for _, a := range s.Artifacts {
		n += a.Files
	}
	return n
}
