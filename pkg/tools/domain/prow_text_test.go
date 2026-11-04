package domain

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSelectText_TailMaxBytesCutsAtLine(t *testing.T) {
	in := "aaaa\nbbbb\ncccc\ndddd\n"
	res, err := selectText(strings.NewReader(in), textSelection{TailLines: 10, MaxBytes: 12})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "cccc\ndddd" || !res.Truncated || res.TruncateReason != "max_bytes reached" {
		t.Errorf("got %+v", res)
	}
	if res.LinesRead != 4 || res.BytesRead != int64(len(in)) {
		t.Errorf("lines/bytes read = %d/%d", res.LinesRead, res.BytesRead)
	}
}

func TestSelectText_MaxBytesKeepsLineAtExactBoundary(t *testing.T) {
	res, _ := selectText(strings.NewReader("aaaa\nbbbb\ncccc\n"), textSelection{TailLines: 10, MaxBytes: 9})
	if res.Text != "bbbb\ncccc" {
		t.Errorf("tail: got %q", res.Text)
	}
	res, _ = selectText(strings.NewReader("aaaa\nbbbb\ncccc\n"), textSelection{HeadLines: 10, MaxBytes: 9})
	if res.Text != "aaaa\nbbbb" {
		t.Errorf("head: got %q", res.Text)
	}
}

func TestSelectText_MaxBytesValidUTF8WithoutNewline(t *testing.T) {
	line := strings.Repeat("é", 100)
	for _, sel := range []textSelection{{TailLines: 1, MaxBytes: 11}, {HeadLines: 1, MaxBytes: 11}} {
		res, _ := selectText(strings.NewReader(line), sel)
		if !utf8.ValidString(res.Text) || len(res.Text) > 11 || res.Text == "" {
			t.Errorf("%+v: got %q", sel, res.Text)
		}
	}
}

func TestSelectText_HeadMaxBytesCutsAtLine(t *testing.T) {
	res, _ := selectText(strings.NewReader("aaaa\nbbbb\ncccc\n"), textSelection{HeadLines: 10, MaxBytes: 7})
	if res.Text != "aaaa" || !res.Truncated {
		t.Errorf("got %+v", res)
	}
}

func TestSelectText_HeadStopsEarly(t *testing.T) {
	res, _ := selectText(strings.NewReader("1\n2\n3\n4\n"), textSelection{HeadLines: 2, MaxBytes: 100})
	if res.Text != "1\n2" || res.LinesRead != 2 || !res.Truncated {
		t.Errorf("got %+v", res)
	}
}

func TestSelectText_HeadExactLinesNotTruncated(t *testing.T) {
	res, _ := selectText(strings.NewReader("1\n2\n"), textSelection{HeadLines: 2, MaxBytes: 100})
	if res.Text != "1\n2" || res.Truncated {
		t.Errorf("got %+v", res)
	}
}

type failingReader struct {
	data string
	done bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("connection reset")
	}
	r.done = true
	return copy(p, r.data), nil
}

func TestSelectText_ReadErrorKeepsSelection(t *testing.T) {
	res, err := selectText(&failingReader{data: "a\nerr x\nb\n"}, textSelection{Grep: regexp.MustCompile("err"), MaxMatches: 5, MaxBytes: 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "err x" || !res.Truncated || !strings.Contains(res.TruncateReason, "connection reset") {
		t.Errorf("got %+v", res)
	}
	if _, err := selectText(&failingReader{done: true}, textSelection{MaxBytes: 100}); err == nil {
		t.Error("expected an error when nothing was read")
	}
}

func TestSelectText_GrepMaxMatches(t *testing.T) {
	in := "x1\nmatch\nx2\nmatch\nx3\nmatch\n"
	res, _ := selectText(strings.NewReader(in), textSelection{Grep: regexp.MustCompile("match"), MaxMatches: 2, MaxBytes: 100})
	if res.Text != "match\n--\nmatch" || res.MatchCount != 2 || res.TruncateReason != "max_matches reached" {
		t.Errorf("got %+v", res)
	}
}

func TestSelectText_GrepOverlappingContext(t *testing.T) {
	in := "a\nm1\nb\nm2\nc\nd\ne\n"
	res, _ := selectText(strings.NewReader(in), textSelection{Grep: regexp.MustCompile("^m"), ContextLines: 1, MaxMatches: 10, MaxBytes: 100})
	if res.Text != "a\nm1\nb\nm2\nc" {
		t.Errorf("got %q", res.Text)
	}
}

func TestSelectText_StripsANSIAndClipsLongLines(t *testing.T) {
	long := strings.Repeat("x", maxOutputLineBytes+10)
	in := "\x1b[36mINFO\x1b[0m start\n" + long + "\nlast"
	res, _ := selectText(strings.NewReader(in), textSelection{TailLines: 10, MaxBytes: maxMaxBytes})
	lines := strings.Split(res.Text, "\n")
	if lines[0] != "INFO start" {
		t.Errorf("ANSI not stripped: %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], "…[line clipped]") || len(lines[1]) > maxOutputLineBytes+20 {
		t.Errorf("long line not clipped (len %d)", len(lines[1]))
	}
	if lines[2] != "last" {
		t.Errorf("last line without newline lost: %q", lines[2])
	}
}

func TestSelectText_DefaultTail(t *testing.T) {
	var b strings.Builder
	for i := 0; i < defaultTailLines+50; i++ {
		b.WriteString("line\n")
	}
	res, _ := selectText(strings.NewReader(b.String()), textSelection{MaxBytes: maxMaxBytes})
	if got := strings.Count(res.Text, "\n") + 1; got != defaultTailLines {
		t.Errorf("got %d lines, want %d", got, defaultTailLines)
	}
}

func TestLooksBinary(t *testing.T) {
	if !looksBinary("image/png", nil) || !looksBinary("text/plain", []byte("a\x00b")) || looksBinary("text/plain; charset=gzip", []byte("plain text")) {
		t.Error("unexpected looksBinary result")
	}
}

func TestExcerpt_ValidUTF8(t *testing.T) {
	s := strings.Repeat("é", 400)
	if got := excerpt(s, 601); !utf8.ValidString(got) {
		t.Errorf("excerpt is not valid UTF-8: %q", got)
	}
}

func TestExcerpt(t *testing.T) {
	s := strings.Repeat("a", 400) + "MIDDLE" + strings.Repeat("z", 400)
	got := excerpt("\x1b[31m"+s, 600)
	if !strings.HasPrefix(got, "aaa") || !strings.HasSuffix(got, "zzz") || strings.Contains(got, "MIDDLE") {
		t.Errorf("unexpected excerpt: %q", got)
	}
}
