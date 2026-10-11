package render

import (
	"math/rand/v2"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var markdown = []string{
	"```", "  ```go", "```\r", "text ``` inside", "- item **bold** and `code`", "1. step", "# heading", "| a | b |", "",
	"    indented", "\u00a0```", "**open bold", "close** bold", "x\u2028```", "y\r\n```", "\t\v``` after a vertical tab",
	"\x1b[31mred\x1b[0m", "\x1c```", strings.Repeat("long words wrap ", 40), strings.Repeat("x", 3000), "héllo 日本語 🙂",
}

func reply(r *rand.Rand, size int) string {
	var b strings.Builder
	for b.Len() < size {
		b.WriteString(markdown[r.IntN(len(markdown))])
		b.WriteString([]string{"\n", "\n", "\n\n", "\r\n", "\r", "\u2029"}[r.IntN(6)])
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
		got := excerpt(renderEnd(text, width, 18), 18, true, "earlier", "")
		if want := excerpt(render(text, width), 18, true, "earlier", ""); !reflect.DeepEqual(got, want) {
			t.Fatalf("reply %d at width %d:\n got %q\nwant %q", i, width, got, want)
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
