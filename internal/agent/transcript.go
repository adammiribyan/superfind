package agent

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const bidiControls = "\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"

func Bidi(r rune) bool { return strings.ContainsRune(bidiControls, r) }

func dropped(r rune) bool {
	return (r < 0x20 && !strings.ContainsRune("\t\n\r\v\f", r)) || (r >= 0x7f && r < 0xa0) || Bidi(r)
}

func Printable(s string) string {
	if !strings.ContainsFunc(s, dropped) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if dropped(r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
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

func eachLine(path string, fn func(raw []byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	lines := bufio.NewReaderSize(f, 1<<20)
	for {
		raw, err := lines.ReadBytes('\n')
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
	f, err := os.Open(path)
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
			r.feed(buf[:end])
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
