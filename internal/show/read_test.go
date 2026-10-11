package show

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/adammiribyan/superfind/internal/agent"
)

func selectAll(turns []agent.Turn, opt Options) Conversation {
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

var spice = []string{
	"password=hunter2hunter2", "sk-ant-api03-" + strings.Repeat("a", 40), "\x1b[31mred\x1b[0m", "bell\x07", "\u202eevil\u202c",
	"postgres://app:s3cretpass@db.internal:5432/x", "Bearer " + strings.Repeat("j7", 15), `"api_key": "abcd1234efgh5678"`,
	"héllo", "日本語", "🙂", "\ttab", "\rcr", "\x00", "-----BEGIN RSA PRIVATE KEY-----", "-----END RSA PRIVATE KEY-----",
	"-----BEGIN OPENSSH PRIVATE KEY-----\nb3Bl", "x-----END OPENSSH PRIVATE KEY-----y", "password=\"", "\x1b]0;title\x07",
	"secret: abc", "(", "[", "ghp_" + strings.Repeat("e", 36),
}

var words = strings.Fields("index cache turn session parser stream window tail join mask render picker folder agent token query")

func prose(r *rand.Rand, size int) string {
	var b strings.Builder
	for b.Len() < size {
		switch n := r.IntN(100); {
		case n < 8:
			b.WriteString(spice[r.IntN(len(spice))])
		case n < 12:
			b.WriteString("\n")
		default:
			b.WriteString(words[r.IntN(len(words))])
		}
		if r.IntN(4) > 0 {
			b.WriteString(" ")
		}
	}
	return b.String()
}

func line(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

type said struct {
	user bool
	text string
}

func writeSession(t *testing.T, vendor string, lines []string) (*agent.Agent, string) {
	t.Helper()
	dir := t.TempDir()
	var path string
	switch vendor {
	case "claude", "peers":
		path = filepath.Join(dir, ".claude", "projects", "-w", "s.jsonl")
		vendor = "claude"
	case "codex":
		path = filepath.Join(dir, ".codex", "sessions", "2026", "10", "10", "rollout-s.jsonl")
	case "grok":
		path = filepath.Join(dir, ".grok", "sessions", url.PathEscape("/w"), "s", "chat_history.jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return agent.Named(vendor), path
}

func transcript(vendor string, script []said, r *rand.Rand) []string {
	var lines []string
	switch vendor {
	case "codex":
		lines = append(lines, line(map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": "/w"}}))
	case "peers":
		lines = append(lines, line(map[string]any{"type": "assistant", "message": map[string]any{
			"content": []map[string]string{{"type": "text", "text": "before anyone asked"}}}}))
	}
	for i, s := range script {
		if i%9 == 4 {
			lines = append(lines, `{"type":`, "", "not json")
		}
		switch {
		case vendor == "claude" && s.user:
			lines = append(lines, line(map[string]any{"type": "user", "cwd": "/w", "message": map[string]any{"content": s.text}}))
		case vendor == "peers" && s.user:
			lines = append(lines, line(map[string]any{"type": "user", "cwd": "/w", "origin": map[string]string{"kind": "peer", "body": s.text},
				"message": map[string]any{"content": "Another Claude session sent a message"}}))
		case vendor == "claude" || vendor == "peers":
			lines = append(lines, line(map[string]any{"type": "assistant", "message": map[string]any{
				"content": []map[string]string{{"type": "text", "text": s.text}, {"type": "tool_use"}}}}))
			if r.IntN(2) == 0 {
				lines = append(lines, line(map[string]any{"type": "user", "message": map[string]any{
					"content": []map[string]any{{"type": "tool_result", "content": "output"}}}}))
			}
		case vendor == "codex" && s.user:
			lines = append(lines, line(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user",
				"content": []map[string]string{{"type": "input_text", "text": s.text}}}}))
		case vendor == "codex":
			lines = append(lines, line(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant",
				"content": []map[string]string{{"type": "output_text", "text": s.text}}}}),
				line(map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "shell"}}))
		case s.user:
			lines = append(lines, line(map[string]any{"type": "user", "content": "<user_query>" + s.text + "</user_query>"}))
		default:
			lines = append(lines, line(map[string]any{"type": "assistant", "content": s.text, "tool_calls": []string{"x"}}),
				line(map[string]any{"type": "tool_result", "content": "output"}))
		}
	}
	return lines
}

func scripts(r *rand.Rand) map[string][]said {
	var turns, giant, keys []said
	for range 60 {
		turns = append(turns, said{r.IntN(3) == 0, "start " + prose(r, r.IntN(2000))})
	}
	giant = append(giant, said{true, "go"})
	for range 200 {
		giant = append(giant, said{false, prose(r, r.IntN(800))})
	}
	for i := range 160 {
		text := prose(r, r.IntN(300))
		switch i % 11 {
		case 2:
			text += "\n-----BEGIN RSA PRIVATE KEY-----\nMIIE"
		case 5:
			text = "-----END RSA PRIVATE KEY-----\n" + text
		case 7:
			text += "-----END RSA PRIVATE KEY----- after -----BEGIN EC PRIVATE KEY----- again"
		}
		keys = append(keys, said{i%23 == 0, text})
	}
	keys = append(keys, said{false, "tail -----BEGIN PRIVATE KEY----- never closed"})
	return map[string][]said{"turns": turns, "giant": giant, "keys": keys}
}

func TestShowMatchesReadingTheWholeTranscript(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	all := scripts(r)
	for _, shape := range []string{"turns", "giant", "keys"} {
		for _, vendor := range []string{"claude", "peers", "codex", "grok"} {
			lines := transcript(vendor, all[shape], r)
			t.Run(shape+"/"+vendor, func(t *testing.T) {
				t.Parallel()
				matchesTheWholeTranscript(t, vendor, lines)
			})
		}
	}
}

func matchesTheWholeTranscript(t *testing.T, vendor string, lines []string) {
	a, path := writeSession(t, vendor, lines)
	whole, err := agent.ReadTranscript(a, path)
	if err != nil {
		t.Fatal(err)
	}
	if vendor == "peers" && !whole.PeerDriven {
		t.Fatal("the peers transcript is not peer driven")
	}
	for _, tail := range []int{0, 1, 5} {
		for _, maxBytes := range []int{0, 7, 300, 2500, 60000} {
			opt := Options{Tail: tail, MaxBytes: maxBytes}
			want := selectAll(whole.Turns, opt)
			for _, every := range []int{1, 3000, trimEvery} {
				got, err := read(a, path, opt, every)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("every=%d %+v:\n got %s\nwant %s", every, opt, summary(got), summary(want))
				}
			}
		}
	}
}

func summary(c Conversation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "total=%d omitted=%d truncated=%v", c.Total, c.Omitted, c.Truncated)
	for _, t := range c.Turns {
		fmt.Fprintf(&b, "\n  %s cut=%v %d %q", t.Role, t.Cut, len(t.Text), t.Text[max(len(t.Text)-80, 0):])
	}
	return b.String()
}

func TestShowKeepsTheEndOfAGiantTurnWithoutHoldingAllOfIt(t *testing.T) {
	k := &keeper{opt: Options{Tail: 10, MaxBytes: DefaultMaxBytes}, every: trimEvery}
	k.say("user", "go")
	piece := strings.Repeat("abcdefg.", 8<<10)
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := range 1024 {
		k.say("assistant", fmt.Sprintf("%05d", i)+strings.Clone(piece))
	}
	var after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&after)
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 16<<20 {
		t.Fatalf("holding %d MB after reading 64 MB", grew>>20)
	}
	c := k.conversation()
	if c.Total != 2 || c.Omitted != 1 || !c.Truncated || len(c.Turns) != 1 || !c.Turns[0].Cut {
		t.Fatalf("got %s", summary(c))
	}
	if want := "…" + piece[len(piece)-DefaultMaxBytes:]; c.Turns[0].Text != want {
		t.Fatalf("the cut turn ends with %q", c.Turns[0].Text[len(c.Turns[0].Text)-40:])
	}
	runtime.KeepAlive(k)
}

func TestShowCutsAGiantTurnOnItsLastBytesAcrossItsParts(t *testing.T) {
	k := &keeper{opt: Options{MaxBytes: 10}, every: 1}
	for _, part := range []string{"ab", "cd", "efghi"} {
		k.say("assistant", part)
	}
	c := k.conversation()
	if len(c.Turns) != 1 || c.Turns[0].Text != "…\ncd\n\nefghi" || !c.Turns[0].Cut || !c.Truncated || c.Omitted != 0 {
		t.Fatalf("got %s", summary(c))
	}
}

func TestShowMasksAPrivateKeyThatSpansJoinedMessages(t *testing.T) {
	k := &keeper{opt: Options{MaxBytes: 60}, every: 1}
	for _, part := range []string{"here -----BEGIN RSA PRIVATE KEY-----", "MIIEow", "-----END RSA PRIVATE KEY----- password=\"x", "done"} {
		k.say("assistant", part)
	}
	c := k.conversation()
	if len(c.Turns) != 1 || c.Turns[0].Text != "here [masked private key] password=\"x\n\ndone" {
		t.Fatalf("got %s", summary(c))
	}
}

func TestShowKeepsTheMessageAfterAPrivateKeyThatEndsItsOwnMessage(t *testing.T) {
	k := &keeper{opt: Options{MaxBytes: 1000}, every: 1}
	k.say("assistant", "-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----")
	k.say("assistant", "hello")
	if c := k.conversation(); len(c.Turns) != 1 || c.Turns[0].Text != "[masked private key]\n\nhello" {
		t.Fatalf("got %s", summary(c))
	}
}

func TestShowHoldsLittleOfAPrivateKeyReopenedInEveryMessage(t *testing.T) {
	k := &keeper{opt: Options{Tail: 1, MaxBytes: 1}, every: 1 << 20}
	k.say("assistant", "-----BEGIN PRIVATE KEY-----")
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range 64 {
		k.say("assistant", "-----END PRIVATE KEY-----\n"+strings.Repeat(".", 256<<10)+"\n-----BEGIN PRIVATE KEY-----")
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 4<<20 {
		t.Fatalf("holding %d MB after reading 16 MB", grew>>20)
	}
	if c := k.conversation(); len(c.Turns) != 1 || c.Turns[0].Text != "…]" {
		t.Fatalf("got %s", summary(c))
	}
	runtime.KeepAlive(k)
}

func TestShowTakesTheLargestTail(t *testing.T) {
	c := pick(turns("one", "two"), Options{Tail: math.MaxInt, MaxBytes: DefaultMaxBytes})
	if strings.Join(texts(c), ",") != "one,two" || c.Omitted != 0 || c.Truncated {
		t.Fatalf("got %s", summary(c))
	}
}

func TestShowKeepsATurnThatFitsExactlyAfterAMessageThatCleansToNothing(t *testing.T) {
	k := &keeper{opt: Options{MaxBytes: 5}, every: 1}
	k.say("assistant", "\x00")
	k.say("assistant", "abc")
	if c := k.conversation(); len(c.Turns) != 1 || c.Turns[0].Text != "\n\nabc" || c.Turns[0].Cut || c.Truncated {
		t.Fatalf("got %s", summary(c))
	}
}
