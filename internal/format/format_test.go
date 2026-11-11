package format

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestBytes(t *testing.T) {
	cases := map[int64]string{
		-5:              "0 B",
		0:               "0 B",
		1023:            "1023 B",
		1024:            "1.0 KB",
		1536:            "1.5 KB",
		100 * 1024:      "100 KB",
		5 * 1024 * 1024: "5.0 MB",
		1 << 30:         "1.0 GB",
		1 << 40:         "1.0 TB",
		1<<62 + 1<<61:   "6.0 EB",
	}
	for in, want := range cases {
		if got := Bytes(in); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "just now"},
		{30 * time.Second, "just now"},
		{-30 * time.Second, "just now"},
		{-2 * time.Hour, "in future"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{50 * time.Hour, "2d ago"},
		{15 * 24 * time.Hour, "2w ago"},
		{70 * 24 * time.Hour, "2mo ago"},
		{800 * 24 * time.Hour, "2y ago"},
	}
	for _, c := range cases {
		if got := Ago(now.Add(-c.d), now); got != c.want {
			t.Errorf("Ago(-%v) = %q, want %q", c.d, got, c.want)
		}
	}
	if Ago(time.Time{}, now) != "never" {
		t.Error("zero time")
	}
	if len(Ago(now.Add(-3000*24*time.Hour), now)) > 8 {
		t.Error("Ago output wider than its 8-cell column")
	}
}

func TestTimestamp(t *testing.T) {
	if Timestamp(time.Time{}) != "unknown" {
		t.Error("zero")
	}
	if got := Timestamp(time.Date(2026, 1, 2, 3, 4, 0, 0, time.Local)); got != "2026-01-02 03:04" {
		t.Errorf("got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hell…"},
		{"hello", 1, "…"},
		{"hello", 0, ""},
		{"hello", -3, ""},
		{"日本語テキスト", 5, "日本…"},
	}
	for _, c := range cases {
		got := Truncate(c.in, c.w)
		if got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
		if c.w > 0 && ansi.StringWidth(got) > c.w {
			t.Errorf("Truncate(%q, %d) is %d cells wide", c.in, c.w, ansi.StringWidth(got))
		}
	}
	styled := "\x1b[31mredredred\x1b[0m"
	if w := ansi.StringWidth(Truncate(styled, 4)); w != 4 {
		t.Errorf("styled truncate width %d", w)
	}
}

func TestTruncateLeft(t *testing.T) {
	if got := TruncateLeft("/a/b/c/project", 9); got != "…/project" {
		t.Errorf("got %q", got)
	}
	if got := TruncateLeft("short", 10); got != "short" {
		t.Errorf("got %q", got)
	}
	if TruncateLeft("abc", 1) != "…" || TruncateLeft("abc", 0) != "" {
		t.Error("edge widths")
	}
	for w := 1; w < 20; w++ {
		if got := TruncateLeft("~/code/日本/project-name", w); ansi.StringWidth(got) > w {
			t.Errorf("width %d: %q is %d wide", w, got, ansi.StringWidth(got))
		}
	}
}

func TestTildePath(t *testing.T) {
	cases := []struct{ p, home, want string }{
		{"/Users/me/code", "/Users/me", "~/code"},
		{"/Users/me", "/Users/me", "~"},
		{"/Users/me", "/Users/me/", "~"},
		{"/Users/meow/code", "/Users/me", "/Users/meow/code"},
		{"/etc", "/Users/me", "/etc"},
		{"", "/Users/me", ""},
		{"/x", "", "/x"},
	}
	for _, c := range cases {
		if got := TildePath(c.p, c.home); got != c.want {
			t.Errorf("TildePath(%q, %q) = %q, want %q", c.p, c.home, got, c.want)
		}
	}
}

func TestPlural(t *testing.T) {
	if Plural(1, "file", "files") != "1 file" || Plural(0, "file", "files") != "0 files" || Plural(2, "file", "files") != "2 files" {
		t.Error("plural")
	}
}

func TestPrintable(t *testing.T) {
	cases := map[string]string{
		"plain":                  "plain",
		"tab\there":              "tab here",
		"line\nbreak":            "line break",
		"esc \x1b[31mred\x1b[0m": "esc [31mred[0m",
		"osc \x1b]0;title\x07":   "osc ]0;title",
		"c1 \u009b31m":           "c1 31m",
		"bidi \u202eevil\u202c":  "bidi evil",
		"zw\u200bsp":             "zwsp",
		"bom\ufeff":              "bom",
		"bad \xff utf8":          "bad  utf8",
		"emoji 🎉 and 日本":         "emoji 🎉 and 日本",
		"del\x7f":                "del",
	}
	for in, want := range cases {
		if got := Printable(in); got != want {
			t.Errorf("Printable(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzPrintable(f *testing.F) {
	f.Add("hello \x1b[2J world")
	f.Add("\u202e\u2066x")
	f.Fuzz(func(t *testing.T, s string) {
		out := Printable(s)
		if strings.ContainsAny(out, "\x1b\x07\r\n\t\x00") {
			t.Fatalf("control bytes survived: %q", out)
		}
		for _, r := range out {
			if !printableRune(r) {
				t.Fatalf("unprintable rune %U survived", r)
			}
		}
		if Printable(out) != out {
			t.Fatal("not idempotent")
		}
	})
}
