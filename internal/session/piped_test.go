package session

import (
	"strings"
	"testing"
)

func TestPipedReadsTheSessionOnTheFirstLineSuperfindPrints(t *testing.T) {
	id := "01990001-a751-7860-aac9-738072c82895"
	path := "/h/.claude/projects/-tmp/" + id + ".jsonl"
	for _, c := range []struct{ in, want string }{
		{id + "\n", id},
		{`"` + id + `"` + "\n", `"` + id + `"`},
		{path, path},
		{path + "\t/tmp\t" + id + "\t/h/.claude\tCheckout\t Checkout  tmp\n", path},
		{`{"schema":1,"id":"` + id + `","path":"` + path + `","resume":"rm -rf /"}` + "\n", path},
		{`{"id":"` + id + `"}`, id},
		{"\n  \r\n" + id + "  \n", id},
		{"", ""},
		{"\n \n", ""},
		{id + "\n01990002-a751-7860-aac9-738072c82895\n", id},
	} {
		got, err := Piped(strings.NewReader(c.in))
		if err != nil || got != c.want {
			t.Errorf("Piped(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestPipedSaysWhatIsWrongWithInputThatNamesNoSession(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`{"title":"Checkout","resume":"claude --resume x"}`, "the JSON line on standard input has no path or id"},
		{`{"id":`, "the JSON line on standard input couldn't be read: unexpected end of JSON input"},
	} {
		got, err := Piped(strings.NewReader(c.in))
		if err == nil || err.Error() != c.want {
			t.Errorf("Piped(%q) = %q, %v; want error %q", c.in, got, err, c.want)
		}
	}
}
