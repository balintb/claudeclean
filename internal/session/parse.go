package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxLineBytes = 16 << 20

const maxPromptRunes = 4000

type envelope struct {
	Type         string `json:"type"`
	Timestamp    string `json:"timestamp"`
	CWD          string `json:"cwd"`
	GitBranch    string `json:"gitBranch"`
	Version      string `json:"version"`
	IsSidechain  bool   `json:"isSidechain"`
	IsMeta       bool   `json:"isMeta"`
	AITitle      string `json:"aiTitle"`
	CustomTitle  string `json:"customTitle"`
	Summary      string `json:"summary"`
	LastPrompt   string `json:"lastPrompt"`
	AgentName    string `json:"agentName"`
	RelocatedCWD string `json:"relocatedCwd"`
}

type userLine struct {
	Message struct {
		Content promptContent `json:"content"`
	} `json:"message"`
}

type promptContent struct {
	text          string
	hasText       bool
	hasToolResult bool
}

func (p *promptContent) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		p.text, p.hasText = s, true
		return nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &blocks); err != nil {
		return err
	}
	var parts []string
	for _, bl := range blocks {
		switch bl.Type {
		case "text":
			if bl.Text != "" {
				parts = append(parts, bl.Text)
			}
		case "tool_result":
			p.hasToolResult = true
		}
	}
	if len(parts) > 0 {
		p.text, p.hasText = strings.Join(parts, "\n"), true
	}
	return nil
}

type meta struct {
	cwds         []string // candidate working directories in priority order
	gitBranch    string
	version      string
	customTitle  string
	aiTitle      string
	summary      string
	agentName    string
	firstPrompt  string
	firstCommand string
	lastPrompt   string // from the most recent last-prompt record
	lastTyped    string // most recent prompt seen in a user record
	started      time.Time
	lastActive   time.Time
	messages     int
	prompts      int
	skipped      int
}

func parseTranscript(r io.Reader) (meta, error) {
	var m meta
	firstCWD, relocated := "", ""
	lr := newLineReader(r, MaxLineBytes)
	for {
		line, skipped, err := lr.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return m, err
		}
		if skipped {
			m.skipped++
			continue
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e envelope
		if err := json.Unmarshal(line, &e); err != nil {
			// A type mismatch on one field leaves the rest usable.
			var te *json.UnmarshalTypeError
			if !errors.As(err, &te) {
				m.skipped++
				continue
			}
		}

		if ts, ok := parseTime(e.Timestamp); ok {
			if m.started.IsZero() || ts.Before(m.started) {
				m.started = ts
			}
			if ts.After(m.lastActive) {
				m.lastActive = ts
			}
		}

		switch e.Type {
		case "custom-title":
			if t := cleanTitle(e.CustomTitle); t != "" {
				m.customTitle = t
			}
			continue
		case "ai-title":
			if t := cleanTitle(e.AITitle); t != "" {
				m.aiTitle = t
			}
			continue
		case "summary":
			if t := cleanTitle(e.Summary); t != "" {
				m.summary = t
			}
			continue
		case "agent-name":
			if t := cleanTitle(e.AgentName); t != "" {
				m.agentName = t
			}
			continue
		case "last-prompt":
			if p, kind := classifyPrompt(e.LastPrompt); kind == promptReal {
				m.lastPrompt = p
			}
			continue
		case "relocated":
			if e.RelocatedCWD != "" {
				relocated = e.RelocatedCWD
			}
			continue
		}

		if e.IsSidechain {
			continue
		}
		if e.Type != "user" && e.Type != "assistant" {
			continue
		}
		m.messages++
		if e.CWD != "" && firstCWD == "" {
			firstCWD = e.CWD
		}
		if e.GitBranch != "" && e.GitBranch != "HEAD" {
			m.gitBranch = e.GitBranch
		}
		if e.Version != "" {
			m.version = e.Version
		}
		if e.Type != "user" || e.IsMeta {
			continue
		}

		var u userLine
		if err := json.Unmarshal(line, &u); err != nil {
			continue
		}
		c := u.Message.Content
		if !c.hasText || c.hasToolResult {
			continue
		}
		p, kind := classifyPrompt(c.text)
		switch kind {
		case promptReal:
			m.prompts++
			if m.firstPrompt == "" {
				m.firstPrompt = p
			}
			m.lastTyped = p
		case promptCommand:
			if m.firstCommand == "" {
				m.firstCommand = p
			}
		}
	}
	if relocated != "" {
		m.cwds = append(m.cwds, relocated)
	}
	if firstCWD != "" {
		m.cwds = append(m.cwds, firstCWD)
	}
	return m, nil
}

func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() < 2000 || t.Year() > 3000 {
		return time.Time{}, false
	}
	return t, true
}

type promptKind int

const (
	promptNone promptKind = iota
	promptReal
	promptCommand
)

var (
	commandNameRe = regexp.MustCompile(`(?s)<command-name>\s*(.*?)\s*</command-name>`)
	commandArgsRe = regexp.MustCompile(`(?s)<command-args>\s*(.*?)\s*</command-args>`)
	leadingTagRe  = regexp.MustCompile(`^<[a-zA-Z][a-zA-Z0-9_-]*[\s>/]`)
)

var noisePrefixes = []string{
	"[Request interrupted",
	"This session is being continued from a previous conversation",
	"Caveat: The messages below were generated",
	"API Error:",
}

func classifyPrompt(text string) (string, promptKind) {
	s := strings.TrimSpace(text)
	if s == "" {
		return "", promptNone
	}
	if strings.HasPrefix(s, "<command-name>") || strings.HasPrefix(s, "<command-message>") {
		nm := commandNameRe.FindStringSubmatch(s)
		if nm == nil {
			return "", promptNone
		}
		cmd := strings.TrimSpace(nm[1])
		if am := commandArgsRe.FindStringSubmatch(s); am != nil && strings.TrimSpace(am[1]) != "" {
			cmd += " " + am[1]
		}
		return oneLine(cmd), promptCommand
	}
	if leadingTagRe.MatchString(s) {
		return "", promptNone
	}
	for _, p := range noisePrefixes {
		if strings.HasPrefix(s, p) {
			return "", promptNone
		}
	}
	return oneLine(s), promptReal
}

// oneLine also strips control and bidi characters so transcripts can't
// inject terminal escapes.
func oneLine(s string) string {
	var b strings.Builder
	b.Grow(min(len(s), maxPromptRunes))
	space := false
	n := 0
	for _, r := range s {
		if n >= maxPromptRunes {
			break
		}
		switch {
		case r == utf8.RuneError:
			continue
		case r == '\n' || r == '\r' || r == '\t' || r == ' ' || r == '\u00a0':
			space = true
			continue
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			continue
		case r == '\u200b' || r == '\u200e' || r == '\u200f' || (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069'):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
			n++
		}
		space = false
		b.WriteRune(r)
		n++
	}
	return b.String()
}

func cleanTitle(s string) string {
	return oneLine(s)
}
