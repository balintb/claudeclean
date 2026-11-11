package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/balintb/claudeclean/internal/layout"
)

// Sessions written to within this window are treated as running.
const DefaultRecentWindow = 2 * time.Minute

type LiveInfo struct {
	PID    int
	Status string
}

type registryEntry struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
}

func LiveSessions(root string, alive func(int) bool) (map[string]LiveInfo, error) {
	if alive == nil {
		alive = ProcessAlive
	}
	dir := filepath.Join(root, layout.SessionsDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]LiveInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session registry: %w", err)
	}
	out := make(map[string]LiveInfo)
	for _, ent := range entries {
		if !ent.Type().IsRegular() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		b, err := readSmallFile(filepath.Join(dir, ent.Name()), 1<<20)
		if err != nil {
			continue
		}
		var r registryEntry
		if json.Unmarshal(b, &r) != nil {
			continue
		}
		if !layout.IsSessionID(r.SessionID) || !alive(r.PID) {
			continue
		}
		out[r.SessionID] = LiveInfo{PID: r.PID, Status: r.Status}
	}
	return out, nil
}

func LiveReason(id string, live map[string]LiveInfo, transcriptMod, now time.Time, window time.Duration) string {
	if info, ok := live[id]; ok {
		if info.Status != "" {
			return fmt.Sprintf("open in Claude Code (pid %d, %s)", info.PID, info.Status)
		}
		return fmt.Sprintf("open in Claude Code (pid %d)", info.PID)
	}
	if window > 0 && !transcriptMod.IsZero() {
		age := now.Sub(transcriptMod)
		if age < window && age > -window {
			return "written to moments ago"
		}
	}
	return ""
}

func readSmallFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: too large", path)
	}
	return b, nil
}
