package domain

import (
	"bufio"
	"bytes"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Limits for reading artifact text. Objects are gzip-encoded in GCS and
// gcsweb ignores Range requests, so a tail has to stream the whole object.
const (
	defaultTailLines    = 200
	defaultContextLines = 3
	defaultMaxMatches   = 50
	defaultMaxBytes     = 64 << 10
	maxMaxBytes         = 1 << 20
	maxDownloadBytes    = 256 << 20
	maxLineBytes        = 64 << 10
	maxOutputLineBytes  = 4 << 10
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// outputLine strips terminal color codes and clips very long lines.
func outputLine(s string) string {
	s = ansiEscape.ReplaceAllString(s, "")
	if len(s) > maxOutputLineBytes {
		return strings.ToValidUTF8(s[:maxOutputLineBytes], "") + " …[line clipped]"
	}
	return s
}

type textSelection struct {
	HeadLines    int
	TailLines    int
	Grep         *regexp.Regexp
	ContextLines int
	MaxMatches   int
	MaxBytes     int
}

type textResult struct {
	Text           string `json:"text"`
	Mode           string `json:"mode"`
	LinesRead      int    `json:"lines_read"`
	BytesRead      int64  `json:"bytes_read"`
	MatchCount     int    `json:"match_count,omitempty"`
	Truncated      bool   `json:"truncated"`
	TruncateReason string `json:"truncate_reason,omitempty"`
}

// selectText streams r and keeps the requested lines: grep matches with
// context, the first HeadLines lines, or the last TailLines lines.
func selectText(r io.Reader, sel textSelection) (textResult, error) {
	counter := &countingReader{r: io.LimitReader(r, maxDownloadBytes+1)}
	br := bufio.NewReaderSize(counter, 64<<10)
	var res textResult
	var out []string

	switch {
	case sel.Grep != nil:
		res.Mode = "grep"
	case sel.HeadLines > 0:
		res.Mode = "head"
	default:
		res.Mode = "tail"
		if sel.TailLines <= 0 {
			sel.TailLines = defaultTailLines
		}
	}

	var before []string // grep context before the next match, or the tail window
	afterLeft := 0
	lastEmitted := -1
	lineNo := 0
	for {
		line, err := readLine(br)
		if line == nil && err != nil {
			if err == io.EOF {
				break
			}
			if lineNo == 0 {
				return res, err
			}
			// Keep what was selected so far, e.g. on a timeout in a huge log.
			res.Truncated = true
			res.TruncateReason = "read error after " + strconv.Itoa(lineNo) + " lines: " + err.Error()
			break
		}
		lineNo++
		text := outputLine(string(line))
		switch res.Mode {
		case "head":
			out = append(out, text)
			if len(out) >= sel.HeadLines {
				if _, err := br.Peek(1); err == nil {
					res.Truncated = true
					res.TruncateReason = "head_lines reached"
				}
				goto done
			}
		case "tail":
			before = append(before, text)
			if len(before) > sel.TailLines {
				before = before[1:]
			}
		case "grep":
			if sel.Grep.MatchString(text) {
				res.MatchCount++
				if res.MatchCount > sel.MaxMatches {
					res.MatchCount = sel.MaxMatches
					res.Truncated = true
					res.TruncateReason = "max_matches reached"
					goto done
				}
				if lastEmitted >= 0 && lineNo-len(before) > lastEmitted+1 {
					out = append(out, "--")
				}
				out = append(out, before...)
				out = append(out, text)
				before = before[:0]
				afterLeft = sel.ContextLines
				lastEmitted = lineNo
			} else if afterLeft > 0 {
				out = append(out, text)
				afterLeft--
				lastEmitted = lineNo
			} else if sel.ContextLines > 0 {
				before = append(before, text)
				if len(before) > sel.ContextLines {
					before = before[1:]
				}
			}
		}
		if err == io.EOF {
			break
		}
	}
	if counter.n > maxDownloadBytes {
		res.Truncated = true
		res.TruncateReason = "download limit reached; only the beginning of the file was read"
	}
	if res.Mode == "tail" {
		out = before
	}
done:
	res.LinesRead = lineNo
	res.BytesRead = min(counter.n, maxDownloadBytes)
	res.Text = strings.Join(out, "\n")
	if len(res.Text) > sel.MaxBytes {
		res.Truncated = true
		res.TruncateReason = "max_bytes reached"
		// Cut at a line boundary, or at a rune boundary when the kept
		// part has no newline, so no partial line or rune is returned.
		if res.Mode == "tail" {
			start := len(res.Text) - sel.MaxBytes
			if res.Text[start-1] != '\n' {
				if i := strings.IndexByte(res.Text[start:], '\n'); i >= 0 {
					start += i + 1
				} else {
					for start < len(res.Text) && !utf8.RuneStart(res.Text[start]) {
						start++
					}
				}
			}
			res.Text = res.Text[start:]
		} else {
			end := sel.MaxBytes
			if res.Text[end] != '\n' {
				if i := strings.LastIndexByte(res.Text[:end], '\n'); i >= 0 {
					end = i
				} else {
					for end > 0 && !utf8.RuneStart(res.Text[end]) {
						end--
					}
				}
			}
			res.Text = res.Text[:end]
		}
	}
	return res, nil
}

// readLine returns the next line without its newline, cut at maxLineBytes.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := br.ReadLine()
		if len(line) < maxLineBytes {
			line = append(line, chunk[:min(len(chunk), maxLineBytes-len(line))]...)
		}
		if err != nil {
			if line != nil {
				return line, err
			}
			return nil, err
		}
		if !isPrefix {
			if line == nil {
				line = []byte{}
			}
			return line, nil
		}
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// looksBinary reports whether the content type or the first bytes show a
// non-text object.
func looksBinary(contentType string, head []byte) bool {
	ct := strings.ToLower(contentType)
	for _, p := range []string{"image/", "video/", "audio/", "application/octet-stream", "application/zip", "application/gzip", "application/x-tar", "application/pdf"} {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	return bytes.IndexByte(head, 0) >= 0
}
