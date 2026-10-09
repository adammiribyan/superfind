package agent

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
)

const bidiControls = "\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"

func Bidi(r rune) bool { return strings.ContainsRune(bidiControls, r) }

func dropped(r rune) bool {
	return (r < 0x20 && !strings.ContainsRune("\t\n\r\v\f", r)) || (r >= 0x7f && r < 0xa0) || Bidi(r)
}

var escapeSequence = regexp.MustCompile("\x1b(?:\\[[0-?]*[ -/]*[@-~]|[\\]P_^X][^\x07\x1b\n]*(?:\x07|\x1b\\\\)|[ -/]*[0-~])")

func Printable(s string) string {
	if !strings.ContainsFunc(s, dropped) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if dropped(r) {
			return -1
		}
		return r
	}, escapeSequence.ReplaceAllString(s, ""))
}

var (
	leadingTag = regexp.MustCompile(`^<([A-Za-z][\w-]*)[^<>]*>`)
	bareTag    = regexp.MustCompile(`^</?[^<>]*>$`)
)

func withoutWrappers(text string) string {
	for {
		text = strings.TrimSpace(text)
		if bareTag.MatchString(text) {
			return ""
		}
		m := leadingTag.FindStringSubmatch(text)
		if m == nil || !strings.ContainsAny(m[1], "-_") {
			return text
		}
		end := strings.Index(text, "</"+m[1]+">")
		if end < 0 {
			return ""
		}
		text = text[end+len(m[1])+3:]
	}
}

type Event string

const (
	Replied     Event = "assistant_text"
	CalledTool  Event = "tool_call"
	Interrupted Event = "interrupted"
	Prompted    Event = "user_prompt"
)

type Turn struct {
	Role string
	Text string
}

type Transcript struct {
	Cwd, Branch                      string
	CustomTitle, AITitle, LastPrompt string
	Prompts, Messages                []string
	Turns                            []Turn
	ToolCalls                        int
	Last                             Event
	Hidden, Incomplete               bool
}

type reader interface {
	feed(raw []byte) bool
	done() Transcript
}

const longestLine = 16 << 20

func errNotAFile(path string) error {
	return &os.PathError{Op: "read", Path: path, Err: errors.New("not a regular file")}
}

func openFile(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errNotAFile(path)
	}
	return os.Open(path)
}

func readSmall(path string) ([]byte, error) {
	f, err := openFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, longestLine))
}

func eachLine(path string, fn func(raw []byte) bool) error {
	f, err := openFile(path)
	if err != nil {
		return err
	}
	defer f.Close()
	lines := bufio.NewReaderSize(f, 1<<20)
	var long []byte
	tooLong := false
	for {
		raw, err := lines.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			if tooLong = tooLong || len(long)+len(raw) > longestLine; !tooLong {
				long = append(long, raw...)
			}
			continue
		}
		if long != nil || tooLong {
			if !tooLong {
				raw = append(long, raw...)
			} else {
				raw = nil
			}
			long, tooLong = nil, false
		}
		if len(raw) > 0 && !fn(raw) {
			return nil
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (t *Transcript) say(role, text string) {
	if n := len(t.Turns); n > 0 && t.Turns[n-1].Role == role {
		t.Turns[n-1].Text += "\n\n" + text
	} else {
		t.Turns = append(t.Turns, Turn{role, text})
	}
	t.Messages = append(t.Messages, text)
}

func ReadTranscript(a *Agent, path string) (Transcript, error) {
	r, err := a.reader(path)
	if err != nil {
		return Transcript{}, err
	}
	err = eachLine(path, r.feed)
	return r.done(), err
}

const firstEndingWindow = 1 << 20

func ReadEnding(a *Agent, path string, enough func([]Turn) bool) ([]Turn, error) {
	f, err := openFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	for window := int64(firstEndingWindow); ; window *= 4 {
		start := max(size-window, 0)
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if start > 0 {
			buf = buf[bytes.IndexByte(buf, '\n')+1:]
		}
		r, err := a.reader(path)
		if err != nil {
			return nil, err
		}
		for len(buf) > 0 {
			end := bytes.IndexByte(buf, '\n') + 1
			if end == 0 {
				end = len(buf)
			}
			if end <= longestLine {
				r.feed(buf[:end])
			}
			buf = buf[end:]
		}
		turns := r.done().Turns
		if start > 0 && len(turns) > 0 {
			turns = turns[1:]
		}
		if start == 0 || enough(turns) {
			return turns, nil
		}
	}
}
