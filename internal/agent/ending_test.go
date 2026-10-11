package agent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func readEndingFromBuffers(a *Agent, path string, enough func([]Turn) bool) ([]Turn, error) {
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
		r, err := a.reader(path, Transcript{Last: Replied})
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
		read := r.done()
		turns := read.Turns
		if start > 0 && len(turns) > 0 {
			turns = turns[1:]
		}
		if start == 0 || (enough(turns) && !read.PeerDriven) {
			return turns, nil
		}
	}
}

func TestTheEndOfASessionReadsTheSameAsFromWholeWindows(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	dir := t.TempDir()
	for file := range 6 {
		var b strings.Builder
		for b.Len() < 6<<20 {
			text := strings.Repeat("word ", r.IntN(2000))
			switch n := r.IntN(20); {
			case n == 0:
				fmt.Fprintf(&b, `{"type":"user","message":{"content":[{"type":"tool_result","content":"%s"}]}}`, strings.Repeat("o", r.IntN(3<<20)))
			case n < 4:
				fmt.Fprintf(&b, `{"type":"user","message":{"content":"ask %d %s"}}`, n, text)
			case n == 4:
				b.WriteString(`{"type":"user","origin":{"kind":"peer","body":"from a peer"},"message":{"content":"x"}}`)
			case n == 5:
				b.WriteString(`{"type":`)
			default:
				fmt.Fprintf(&b, `{"type":"assistant","message":{"content":[{"type":"text","text":"reply %s"}]}}`, text)
			}
			if file%3 != 0 || b.Len() < 6<<20 {
				b.WriteString("\n")
			}
		}
		if file == 5 {
			fmt.Fprintf(&b, `{"type":"assistant","message":{"content":[{"type":"text","text":"%s"}]}}`+"\n", strings.Repeat("z", longestLine))
		}
		path := filepath.Join(dir, fmt.Sprintf("s%d.jsonl", file))
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, need := range []int{1, 3, 40, 1000} {
			enough := func(turns []Turn) bool { return len(turns) >= need }
			got, err := ReadEnding(Named("claude"), path, enough)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := readEndingFromBuffers(Named("claude"), path, enough)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("file %d, %d turns: got %d turns, want %d", file, need, len(got), len(want))
			}
		}
	}
}

func TestThePreviewOfALongRunReadsItWithoutHoldingTheFile(t *testing.T) {
	var b strings.Builder
	b.WriteString(typed + "\n")
	output := strings.Repeat("o", 64<<10)
	for range 1024 {
		fmt.Fprintf(&b, `{"type":"user","message":{"content":[{"type":"tool_result","content":"%s"}]}}`+"\n", output)
	}
	b.WriteString(reply + "\n")
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var peak atomic.Uint64
	done := make(chan struct{})
	go func() {
		var m runtime.MemStats
		for {
			select {
			case <-done:
				return
			case <-time.After(time.Millisecond):
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak.Load() {
					peak.Store(m.HeapAlloc)
				}
			}
		}
	}()
	turns, err := ReadEnding(Named("claude"), path, func(turns []Turn) bool { return len(turns) >= 2 })
	close(done)
	if err != nil || len(turns) != 2 || turns[0].Text != "fix the login bug" {
		t.Fatalf("got %+v, %v", turns, err)
	}
	if peak.Load() > 32<<20 {
		t.Fatalf("reading a 64 MB session held %d MB at once", peak.Load()>>20)
	}
}
