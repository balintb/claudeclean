package session

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func readAll(t *testing.T, input string, maxLine int) (lines []string, skipped int) {
	t.Helper()
	lr := newLineReader(strings.NewReader(input), maxLine)
	for {
		line, skip, err := lr.next()
		if errors.Is(err, io.EOF) {
			return lines, skipped
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if skip {
			skipped++
			continue
		}
		lines = append(lines, string(line))
	}
}

func TestLineReaderBasics(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  []string
		skips int
	}{
		{"empty", "", nil, 0},
		{"single no newline", "abc", []string{"abc"}, 0},
		{"single newline", "abc\n", []string{"abc"}, 0},
		{"crlf", "a\r\nb\r\n", []string{"a", "b"}, 0},
		{"blank lines", "\n\na\n\n", []string{"", "", "a", ""}, 0},
		{"three", "1\n2\n3", []string{"1", "2", "3"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, skips := readAll(t, c.in, 100)
			if strings.Join(got, "|") != strings.Join(c.want, "|") || len(got) != len(c.want) || skips != c.skips {
				t.Fatalf("got %q (skips %d), want %q (skips %d)", got, skips, c.want, c.skips)
			}
		})
	}
}

func TestLineReaderSkipsOversizedLinesAndKeepsGoing(t *testing.T) {
	long := strings.Repeat("x", 500)
	in := "first\n" + long + "\nmiddle\n" + long + long + "\nlast"
	got, skips := readAll(t, in, 100)
	want := []string{"first", "middle", "last"}
	if strings.Join(got, "|") != strings.Join(want, "|") || skips != 2 {
		t.Fatalf("got %q skips %d", got, skips)
	}
}

func TestLineReaderOversizedFinalLine(t *testing.T) {
	got, skips := readAll(t, "ok\n"+strings.Repeat("y", 1000), 100)
	if len(got) != 1 || got[0] != "ok" || skips != 1 {
		t.Fatalf("got %q skips %d", got, skips)
	}
}

func TestLineReaderExactlyAtLimit(t *testing.T) {
	exact := strings.Repeat("z", 100)
	got, skips := readAll(t, exact+"\n"+exact+"z\n", 100)
	if len(got) != 1 || got[0] != exact || skips != 1 {
		t.Fatalf("got %d lines, %d skips", len(got), skips)
	}
}

func TestLineReaderLinesLargerThanBuffer(t *testing.T) {
	big := strings.Repeat("b", 200*1024)
	got, skips := readAll(t, big+"\nnext\n", 1<<20)
	if len(got) != 2 || got[0] != big || got[1] != "next" || skips != 0 {
		t.Fatalf("got %d lines (%d skips)", len(got), skips)
	}
}

func TestLineReaderOneByteReads(t *testing.T) {
	lr := newLineReader(iotest.OneByteReader(strings.NewReader("ab\ncd\n")), 10)
	var got []string
	for {
		line, _, err := lr.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(line))
	}
	if strings.Join(got, ",") != "ab,cd" {
		t.Fatalf("got %q", got)
	}
}

func TestLineReaderPropagatesIOErrors(t *testing.T) {
	boom := errors.New("boom")
	lr := newLineReader(iotest.ErrReader(boom), 10)
	if _, _, err := lr.next(); !errors.Is(err, boom) {
		t.Fatalf("got %v, want boom", err)
	}
}

func FuzzLineReader(f *testing.F) {
	f.Add("a\nb\r\n\nccc", 3)
	f.Add(strings.Repeat("x", 300)+"\n", 10)
	f.Fuzz(func(t *testing.T, in string, maxLine int) {
		if maxLine < 1 || maxLine > 1<<16 {
			return
		}
		lr := newLineReader(strings.NewReader(in), maxLine)
		total := 0
		for i := 0; ; i++ {
			if i > len(in)+2 {
				t.Fatal("reader did not terminate")
			}
			line, skipped, err := lr.next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if skipped && line != nil {
				t.Fatal("skipped line returned data")
			}
			if len(line) > maxLine {
				t.Fatalf("line of %d bytes exceeds cap %d", len(line), maxLine)
			}
			if strings.ContainsAny(string(line), "\n") {
				t.Fatal("line contains newline")
			}
			total++
		}
		if total > strings.Count(in, "\n")+1 {
			t.Fatalf("%d lines from %d newlines", total, strings.Count(in, "\n"))
		}
	})
}
