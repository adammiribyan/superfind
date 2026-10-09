package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
)

type event string

const (
	replied     event = "assistant_text"
	calledTool  event = "tool_call"
	interrupted event = "interrupted"
	prompted    event = "user_prompt"
)

type turn struct {
	role string
	text string
}

type transcript struct {
	cwd, branch                      string
	customTitle, aiTitle, lastPrompt string
	prompts, messages                []string
	turns                            []turn
	toolCalls                        int
	last                             event
}

type reader interface {
	feed(raw []byte) bool
	done() transcript
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

func (t *transcript) say(role, text string) {
	if n := len(t.turns); n > 0 && t.turns[n-1].role == role {
		t.turns[n-1].text += "\n\n" + text
	} else {
		t.turns = append(t.turns, turn{role, text})
	}
	t.messages = append(t.messages, text)
}

func readTranscript(a *agent, path string) (transcript, error) {
	r, err := a.reader(path)
	if err != nil {
		return transcript{}, err
	}
	err = eachLine(path, r.feed)
	return r.done(), err
}

const firstEndingWindow = 1 << 20

func readEnding(a *agent, path string, enough func([]turn) bool) ([]turn, error) {
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
		turns := r.done().turns
		if start > 0 && len(turns) > 0 {
			turns = turns[1:]
		}
		if start == 0 || enough(turns) {
			return turns, nil
		}
	}
}
