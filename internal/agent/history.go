package agent

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const historyFile = "history.jsonl"

const Deleted Event = "transcript_deleted"

type PromptsOnly struct {
	Path  string
	ID    FileID
	Mtime float64
	lines [][]byte
}

func (p PromptsOnly) Read() (Transcript, Trail, error) {
	r := &prompts{t: Transcript{}}
	for _, raw := range p.lines {
		r.feed(raw)
	}
	return r.done(), Trail{}, nil
}

func TranscriptGone(path string) bool {
	return filepath.Base(filepath.Dir(path)) == historyFile && validID(filepath.Base(path))
}

func GoneNote(home string) string {
	return "Claude Code deleted this session's transcript, so only your prompts are left. " + keepLonger(home)
}

type NoResume struct{ Home, ID string }

func (e NoResume) Error() string {
	return "Claude Code deleted this session's transcript, so Claude can't resume it. superfind show " + ShellQuote(e.ID) +
		" prints your prompts. " + keepLonger(e.Home)
}

func keepLonger(home string) string {
	settings := filepath.Join(home, "settings.json")
	if rel, err := filepath.Rel(Resolve(Home()), Resolve(home)); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		settings = filepath.Join("~", rel, "settings.json")
	}
	return "To keep future transcripts longer, set cleanupPeriodDays in " + settings + "."
}

var sessionID = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_-]*$`)

func validID(id string) bool { return sessionID.MatchString(id) }

var slashOnly = regexp.MustCompile(`^/[\w:-]+(\s|$)`)

type historyLine struct {
	Display        string                  `json:"display"`
	PastedContents map[string]historyPaste `json:"pastedContents"`
	Timestamp      int64                   `json:"timestamp"`
	Project        string                  `json:"project"`
	SessionID      string                  `json:"sessionId"`
}

type historyPaste struct {
	ID      int    `json:"id"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

var (
	idKey      = []byte(`"sessionId":"`)
	timeKey    = []byte(`"timestamp":`)
	displayKey = []byte(`"display":"`)
)

func historyKeys(raw []byte) (id []byte, ms int64, slash bool) {
	i, j, k := bytes.LastIndex(raw, idKey), bytes.LastIndex(raw, timeKey), bytes.Index(raw, displayKey)
	if i >= 0 && j >= 0 && k >= 0 {
		id = raw[i+len(idKey):]
		end := bytes.IndexByte(id, '"')
		digits, n := raw[j+len(timeKey):], 0
		for ; n < len(digits) && n < 18 && digits[n] >= '0' && digits[n] <= '9'; n++ {
			ms = ms*10 + int64(digits[n]-'0')
		}
		if slash, sure := slashAt(raw[k+len(displayKey):]); sure && end >= 0 && n > 0 {
			return id[:end], ms, slash
		}
	}
	var l historyLine
	if json.Unmarshal(raw, &l) != nil {
		return nil, 0, false
	}
	return []byte(l.SessionID), l.Timestamp, slashOnly.MatchString(l.Display)
}

func slashAt(display []byte) (slash, sure bool) {
	if len(display) == 0 || display[0] != '/' {
		return false, true
	}
	n := 1
	for n < len(display) && (display[n] == '_' || display[n] == ':' || display[n] == '-' ||
		'0' <= display[n] && display[n] <= '9' || 'a' <= display[n]|0x20 && display[n]|0x20 <= 'z') {
		n++
	}
	switch {
	case n == 1:
		return false, true
	case n == len(display) || display[n] == '\\':
		return false, false
	}
	return display[n] == '"' || display[n] == ' ', true
}

func (a *Agent) PromptsOnly(home string) ([]PromptsOnly, error) {
	if !a.history {
		return nil, nil
	}
	file := filepath.Join(home, historyFile)
	type found struct {
		PromptsOnly
		spans [][2]int
	}
	byID := map[string]*found{}
	var order []*found
	var held []byte
	if info, err := os.Stat(file); err == nil {
		held = make([]byte, 0, info.Size())
	}
	err := eachLine(file, func(raw []byte) bool {
		id, ms, slash := historyKeys(raw)
		s := byID[string(id)]
		if s == nil {
			if !sessionID.Match(id) {
				return true
			}
			s = &found{PromptsOnly: PromptsOnly{Path: filepath.Join(file, string(id))}}
			byID[string(id)] = s
			order = append(order, s)
		}
		s.ID.Size += int64(len(raw))
		if !slash {
			s.ID.ModNanos = max(s.ID.ModNanos, ms*1e6)
			s.spans = append(s.spans, [2]int{len(held), len(held) + len(raw)})
			held = append(held, raw...)
		}
		return true
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []PromptsOnly
	for _, s := range order {
		if len(s.spans) == 0 {
			continue
		}
		s.Mtime = float64(s.ID.ModNanos) / 1e9
		for _, span := range s.spans {
			s.lines = append(s.lines, held[span[0]:span[1]:span[1]])
		}
		out = append(out, s.PromptsOnly)
	}
	return out, nil
}

func readPrompts(path string, start Transcript) (Transcript, error) {
	file, id := filepath.Dir(path), filepath.Base(path)
	r := &prompts{t: start}
	held := false
	err := eachLine(file, func(raw []byte) bool {
		if got, _, _ := historyKeys(raw); string(got) == id {
			held = true
			r.feed(raw)
		}
		return true
	})
	if err == nil && !held {
		err = &os.PathError{Op: "read", Path: path, Err: fmt.Errorf("no prompts left in %s: %w", historyFile, fs.ErrNotExist)}
	}
	if err != nil {
		return Transcript{}, err
	}
	return r.done(), nil
}

type prompts struct{ t Transcript }

func (r *prompts) feed(raw []byte) bool {
	var l historyLine
	if json.Unmarshal(raw, &l) != nil || slashOnly.MatchString(l.Display) {
		return true
	}
	text := strings.TrimSpace(l.Display)
	for _, p := range slices.SortedFunc(maps.Values(l.PastedContents), func(a, b historyPaste) int { return cmp.Compare(a.ID, b.ID) }) {
		if p.Type == "text" && strings.TrimSpace(p.Content) != "" {
			text += "\n\n" + strings.TrimSpace(p.Content)
		}
	}
	if text = strings.TrimSpace(text); text == "" {
		return true
	}
	r.t.Cwd = cmp.Or(r.t.Cwd, l.Project)
	r.t.prompt(text, time.UnixMilli(l.Timestamp))
	return true
}

func (r *prompts) done() Transcript {
	r.t.join()
	r.t.Last = Deleted
	return r.t
}
