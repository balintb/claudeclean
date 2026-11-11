// Package layout knows where Claude Code keeps session data and which paths
// may be moved to the Trash.
package layout

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Kind int

const (
	KindTranscript  Kind = iota // projects/<project>/<id>.jsonl
	KindSessionDir              // projects/<project>/<id>/ (subagents, tool results)
	KindFileHistory             // file-history/<id>/
	KindSessionEnv              // session-env/<id>/
	KindTasks                   // tasks/<id>/
	KindTodo                    // todos/<id>-agent-<uuid>.json
	KindDebugLog                // debug/<id>.txt
)

func (k Kind) String() string {
	switch k {
	case KindTranscript:
		return "transcript"
	case KindSessionDir:
		return "session data"
	case KindFileHistory:
		return "file history"
	case KindSessionEnv:
		return "session env"
	case KindTasks:
		return "tasks"
	case KindTodo:
		return "todos"
	case KindDebugLog:
		return "debug log"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

func (k Kind) IsDir() bool {
	switch k {
	case KindSessionDir, KindFileHistory, KindSessionEnv, KindTasks:
		return true
	default:
		return false
	}
}

func (k Kind) IsGlobal() bool {
	switch k {
	case KindFileHistory, KindSessionEnv, KindTasks, KindTodo, KindDebugLog:
		return true
	default:
		return false
	}
}

const (
	ProjectsDir    = "projects"
	FileHistoryDir = "file-history"
	SessionEnvDir  = "session-env"
	TasksDir       = "tasks"
	TodosDir       = "todos"
	DebugDir       = "debug"
	SessionsDir    = "sessions" // live-process registry, read only
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func IsSessionID(s string) bool {
	return uuidRe.MatchString(s)
}

func IsProjectDirName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if len(name) > 255 {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	return !strings.HasPrefix(name, ".")
}

func TranscriptID(fileName string) (string, bool) {
	id, ok := strings.CutSuffix(fileName, ".jsonl")
	if !ok || !IsSessionID(id) {
		return "", false
	}
	return id, true
}

var todoRe = regexp.MustCompile(`^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})-agent-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.json$`)

func TodoSessionID(fileName string) (string, bool) {
	m := todoRe.FindStringSubmatch(fileName)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Path builders panic on invalid input; callers validate first.
func mustID(id string) {
	if !IsSessionID(id) {
		panic(fmt.Sprintf("layout: invalid session id %q", id))
	}
}

func mustProject(p string) {
	if !IsProjectDirName(p) {
		panic(fmt.Sprintf("layout: invalid project dir name %q", p))
	}
}

func TranscriptPath(root, project, id string) string {
	mustProject(project)
	mustID(id)
	return filepath.Join(root, ProjectsDir, project, id+".jsonl")
}

func SessionDirPath(root, project, id string) string {
	mustProject(project)
	mustID(id)
	return filepath.Join(root, ProjectsDir, project, id)
}

func GlobalDirPath(root string, kind Kind, id string) string {
	mustID(id)
	switch kind {
	case KindFileHistory:
		return filepath.Join(root, FileHistoryDir, id)
	case KindSessionEnv:
		return filepath.Join(root, SessionEnvDir, id)
	case KindTasks:
		return filepath.Join(root, TasksDir, id)
	default:
		panic(fmt.Sprintf("layout: %v is not a global directory kind", kind))
	}
}

func DebugLogPath(root, id string) string {
	mustID(id)
	return filepath.Join(root, DebugDir, id+".txt")
}

var (
	ErrNotAbsolute  = errors.New("path is not absolute and clean")
	ErrOutsideRoot  = errors.New("path is outside the Claude config root")
	ErrShape        = errors.New("path is not a recognised session artifact")
	ErrWrongSession = errors.New("path belongs to a different session")
	ErrSymlink      = errors.New("path or one of its parents is a symlink")
	ErrWrongType    = errors.New("path has the wrong file type")
	ErrMissing      = errors.New("path does not exist")
)

func Classify(root, path string) (Kind, string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return 0, "", fmt.Errorf("%w: root %q", ErrNotAbsolute, root)
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return 0, "", fmt.Errorf("%w: %q", ErrNotAbsolute, path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return 0, "", fmt.Errorf("%w: %q", ErrOutsideRoot, path)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return 0, "", fmt.Errorf("%w: %q", ErrShape, path)
		}
	}

	switch {
	case len(parts) == 3 && parts[0] == ProjectsDir && IsProjectDirName(parts[1]):
		if id, ok := TranscriptID(parts[2]); ok {
			return KindTranscript, id, nil
		}
		if IsSessionID(parts[2]) {
			return KindSessionDir, parts[2], nil
		}
	case len(parts) == 2 && parts[0] == FileHistoryDir && IsSessionID(parts[1]):
		return KindFileHistory, parts[1], nil
	case len(parts) == 2 && parts[0] == SessionEnvDir && IsSessionID(parts[1]):
		return KindSessionEnv, parts[1], nil
	case len(parts) == 2 && parts[0] == TasksDir && IsSessionID(parts[1]):
		return KindTasks, parts[1], nil
	case len(parts) == 2 && parts[0] == TodosDir:
		if id, ok := TodoSessionID(parts[1]); ok {
			return KindTodo, id, nil
		}
	case len(parts) == 2 && parts[0] == DebugDir:
		if id, ok := strings.CutSuffix(parts[1], ".txt"); ok && IsSessionID(id) {
			return KindDebugLog, id, nil
		}
	}
	return 0, "", fmt.Errorf("%w: %q", ErrShape, path)
}

// Validate is the gate before anything is trashed. path must be a known
// artifact shape for wantID strictly inside root, and neither it nor any
// directory between root and it may be a symlink.
func Validate(root, path, wantID string) (Kind, error) {
	if !IsSessionID(wantID) {
		return 0, fmt.Errorf("%w: invalid session id %q", ErrWrongSession, wantID)
	}
	kind, id, err := Classify(root, path)
	if err != nil {
		return 0, err
	}
	if id != wantID {
		return 0, fmt.Errorf("%w: %q is for %s, not %s", ErrWrongSession, path, id, wantID)
	}

	rel, _ := filepath.Rel(root, path) // cannot fail, Classify succeeded
	dir := root
	if err := requireRealDir(dir); err != nil {
		return 0, err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for _, p := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, p)
		if err := requireRealDir(dir); err != nil {
			return 0, err
		}
	}

	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("%w: %q", ErrMissing, path)
	}
	if err != nil {
		return 0, fmt.Errorf("stat %q: %w", path, err)
	}
	mode := fi.Mode()
	if mode&fs.ModeSymlink != 0 {
		return 0, fmt.Errorf("%w: %q", ErrSymlink, path)
	}
	if kind.IsDir() && !mode.IsDir() {
		return 0, fmt.Errorf("%w: %q should be a directory", ErrWrongType, path)
	}
	if !kind.IsDir() && !mode.IsRegular() {
		return 0, fmt.Errorf("%w: %q should be a regular file", ErrWrongType, path)
	}
	return kind, nil
}

func requireRealDir(dir string) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %q", ErrMissing, dir)
	}
	if err != nil {
		return fmt.Errorf("stat %q: %w", dir, err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: %q", ErrSymlink, dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: %q should be a directory", ErrWrongType, dir)
	}
	return nil
}

func ResolveRoot(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("empty Claude config directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory", real)
	}
	return filepath.Clean(real), nil
}

var projectEncodeRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

func EncodeProjectDir(cwd string) string {
	return projectEncodeRe.ReplaceAllString(cwd, "-")
}

// DecodeProjectDirGuess is lossy: dashes in folder names look like separators.
func DecodeProjectDirGuess(name string) string {
	if !strings.HasPrefix(name, "-") {
		return name
	}
	return strings.ReplaceAll(name, "-", "/")
}
