package show

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/session"
)

const DefaultMaxBytes = 60000

type Turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
	Cut  bool   `json:"cut,omitempty"`
}

type Options struct {
	Tail     int
	MaxBytes int
}

type Conversation struct {
	Turns     []Turn
	Total     int
	Omitted   int
	Truncated bool
}

func Clean(s string) string { return Mask(agent.Printable(s)) }

func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) || agent.Bidi(r) {
			return '?'
		}
		return r
	}, agent.Printable(s))
}

func Select(turns []agent.Turn, opt Options) Conversation {
	c := Conversation{Total: len(turns)}
	start := 0
	if opt.Tail > 0 && opt.Tail < len(turns) {
		start = len(turns) - opt.Tail
	}
	kept := make([]Turn, 0, len(turns)-start)
	for _, t := range turns[start:] {
		kept = append(kept, Turn{Role: t.Role, Text: Clean(t.Text)})
	}
	if opt.MaxBytes > 0 {
		used, first := 0, len(kept)
		for first > 0 && used+len(kept[first-1].Text) <= opt.MaxBytes {
			first--
			used += len(kept[first].Text)
		}
		if first == len(kept) && first > 0 {
			first--
			kept[first].Text = "…" + lastBytes(kept[first].Text, opt.MaxBytes)
			kept[first].Cut = true
			c.Truncated = true
		}
		if first > 0 {
			c.Truncated = true
		}
		kept = kept[first:]
	}
	c.Turns = kept
	c.Omitted = c.Total - len(kept)
	return c
}

func lastBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return s[cut:]
}

func speaker(role string, r session.Record) string {
	if role == "user" {
		return "user"
	}
	return r.Agent
}

func WriteText(w io.Writer, r session.Record, c Conversation) error {
	fmt.Fprintf(w, "# %s\n%s · %s · %s\n", oneLine(r.Title), oneLine(r.Agent), oneLine(r.Cwd), oneLine(r.ID))
	if c.Omitted > 0 {
		noun := "turns"
		if c.Omitted == 1 {
			noun = "turn"
		}
		fmt.Fprintf(w, "\n… %d earlier %s not shown\n", c.Omitted, noun)
	}
	for _, t := range c.Turns {
		if _, err := fmt.Fprintf(w, "\n## %s\n\n%s\n", speaker(t.Role, r), t.Text); err != nil {
			return err
		}
	}
	return nil
}

func WriteJSON(w io.Writer, r session.Record, c Conversation) error {
	turns := c.Turns
	if turns == nil {
		turns = []Turn{}
	}
	out := struct {
		session.Record
		TurnsTotal int    `json:"total_turns"`
		Omitted    int    `json:"omitted_turns"`
		Truncated  bool   `json:"truncated"`
		Turns      []Turn `json:"turns"`
	}{r, c.Total, c.Omitted, c.Truncated, turns}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}
