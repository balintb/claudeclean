package format

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

func Bytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB", "EB"}
	v := float64(n) / unit
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < 0 {
		if d > -time.Minute {
			return "just now"
		}
		return "in future"
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d/(7*24*time.Hour)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d/(30*24*time.Hour)))
	default:
		return fmt.Sprintf("%dy ago", int(d/(365*24*time.Hour)))
	}
}

func Timestamp(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return ansi.Truncate(s, width, "…")
}

func TruncateLeft(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	// Cutting through a wide character can leave one cell too many.
	cut := w - width + 1
	tail := ansi.TruncateLeft(s, cut, "")
	for ansi.StringWidth(tail) > width-1 && cut < w {
		cut++
		tail = ansi.TruncateLeft(s, cut, "")
	}
	return "…" + tail
}

func TildePath(p, home string) string {
	if home == "" || p == "" {
		return p
	}
	home = filepath.Clean(home)
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

func Plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Printable strips control, zero-width and bidi characters so session text
// can't drive the terminal.
func Printable(s string) string {
	clean := true
	for _, r := range s {
		if !printableRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteByte(' ')
		case printableRune(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func printableRune(r rune) bool {
	switch {
	case r == utf8.RuneError:
		return false
	case r < 0x20 || r == 0x7f:
		return false
	case r >= 0x80 && r < 0xa0:
		return false
	case r == 0x200b || r == 0x200e || r == 0x200f || r == 0x2028 || r == 0x2029:
		return false
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return false
	case r == 0xfeff:
		return false
	}
	return true
}
