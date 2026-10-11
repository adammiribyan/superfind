package render

import (
	"math/rand/v2"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

var markdown = []string{
	"```", "  ```go", "```\r", "text ``` inside", "- item **bold** and `code`", "1. step", "# heading", "| a | b |", "",
	"    indented", "\u00a0```", "**open bold", "close** bold", "x\u2028```", "y\r\n```", "\t\v``` after a vertical tab",
	"\x1b[31mred\x1b[0m", "\x1c```", strings.Repeat("long words wrap ", 40), strings.Repeat("x", 3000), "héllo 日本語 🙂",
	"```\n" + strings.Repeat(strings.Repeat("y", 4000)+"\n", 30) + "```", "\v```", "\f```",
}

func reply(r *rand.Rand, size int) string {
	var b strings.Builder
	for b.Len() < size {
		b.WriteString(markdown[r.IntN(len(markdown))])
		b.WriteString([]string{"\n", "\n", "\n\n", "\r\n", "\r", "\u2029", "\v", "\f"}[r.IntN(8)])
	}
	return b.String()
}

func TestTheEndOfAReplyRendersAsTheWholeReplyDoes(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	for i := range 300 {
		text := reply(r, []int{100, 20 << 10, 70 << 10, 300 << 10}[i%4])
		if i%5 == 0 {
			text += strings.Repeat("\n", r.IntN(40))
		}
		width := []int{20, 60, 118}[i%3]
		end, whole := renderEnd(text, width, 18), render(text, width)
		for keep := range 19 {
			got := excerpt(end, keep, true, "earlier", "")
			if want := excerpt(whole, keep, true, "earlier", ""); !reflect.DeepEqual(got, want) {
				t.Fatalf("reply %d at width %d keeping %d:\n got %q\nwant %q", i, width, keep, got, want)
			}
		}
		for rows := range 40 {
			if got, want := min(18, within(end, rows)), min(18, within(whole, rows)); got != want {
				t.Fatalf("reply %d at width %d in %d rows: keeps %d, want %d", i, width, rows, got, want)
			}
		}
	}
}

func TestTheEndOfALongReplyRendersWithoutRenderingAllOfIt(t *testing.T) {
	text := "```\n" + strings.Repeat("code line\n", 1<<20) + "```\nafter the code\n"
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	lines := excerpt(renderEnd(text, 80, 18), 18, true, "earlier", "")
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 32<<20 {
		t.Errorf("rendering the end of a 10 MB reply allocated %d MB", allocated>>20)
	}
	if len(lines) != 19 || lines[17] != "    code line" || lines[18] != "  after the code" {
		t.Fatalf("got %q", lines[15:])
	}
}

func TestFindingTheCodeBlockAtTheCutTakesLinearTime(t *testing.T) {
	text := strings.Repeat(" ", 1<<20) + strings.Repeat("```", 1<<20) + "\n" + strings.Repeat("x\n", 20000)
	done := make(chan []string)
	go func() { done <- renderEnd(text, 80, 18) }()
	select {
	case lines := <-done:
		if lines[len(lines)-1] != "    x" {
			t.Fatalf("got %q", lines[len(lines)-1])
		}
	case <-time.After(10 * time.Second):
		t.Fatal("still scanning after 10 s")
	}
}

func TestTheEndOfAReplyOfLongCodeLinesWidensTheCutOnlyAsFarAsItNeeds(t *testing.T) {
	text := strings.Repeat("words here\n", 1<<20) + "```\n" + strings.Repeat(strings.Repeat("x", 862)+"\n", 40)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	lines := excerpt(renderEnd(text, 80, 18), 18, true, "earlier", "")
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 256<<20 {
		t.Errorf("rendering the end of an 11 MB reply allocated %d MB", allocated>>20)
	}
	line := "    " + strings.Repeat("x", 75) + Dim + "…" + Reset
	if len(lines) != 19 || lines[0] != "  … earlier"+Reset || lines[18] != line {
		t.Fatalf("got %d lines, %q", len(lines), lines[:2])
	}
}
