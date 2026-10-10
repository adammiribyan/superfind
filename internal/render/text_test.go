package render

import (
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func nonSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func markAfterSpace(text string) bool {
	previous := 'x'
	for _, r := range text {
		if unicode.IsSpace(previous) && (unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) || unicode.Is(unicode.Variation_Selector, r)) {
			return true
		}
		previous = r
	}
	return false
}

func FuzzWrapKeepsEveryCharacterWithinTheWidth(f *testing.F) {
	for _, seed := range []string{"", "hello world", "a-very-long-hyphenated-word and more", "漢字かな交じり文 mixed text",
		"tab\tseparated\tcolumns", "👍🏽 emoji and ❤️ hearts", strings.Repeat("x", 500)} {
		f.Add(seed, 20)
	}
	f.Fuzz(func(t *testing.T, text string, width int) {
		if width < 2 || width > 200 || !utf8.ValidString(text) || strings.ContainsAny(text, boldStart+boldEnd) {
			t.Skip()
		}
		text = expandTabs(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\t' {
				return ' '
			}
			return r
		}, text))
		if markAfterSpace(text) {
			t.Skip()
		}
		lines := wrap(text, width, "", "")
		for _, line := range lines {
			if w := displayWidth(line); w > width && displayWidth(nonSpace(line)) > 1 {
				t.Fatalf("line %q is %d wide, more than %d", line, w, width)
			}
		}
		if got, want := nonSpace(strings.Join(lines, "")), nonSpace(text); got != want {
			t.Fatalf("wrapping lost or changed text:\n got %q\nwant %q", got, want)
		}
	})
}

func TestWrappingAMegabyteTokenKeepsItAll(t *testing.T) {
	token := strings.Repeat("QUJD", 1<<18)
	lines := wrap(token, 60, "", "")
	if strings.Join(lines, "") != token {
		t.Fatal("the token was not kept whole across lines")
	}
	for _, line := range lines {
		if displayWidth(line) > 60 {
			t.Fatalf("a line is %d wide", displayWidth(line))
		}
	}
}

func TestMarksFindWordStartsInAnyCase(t *testing.T) {
	cases := []struct {
		title string
		words []string
		want  []Span
	}{
		{"Reindex the Index", []string{"index"}, []Span{{"Reindex the ", false}, {"Index", true}}},
		{"Überprüfung der Rechnung", []string{"üb"}, []Span{{"Üb", true}, {"erprüfung der Rechnung", false}}},
		{"Kelvin \u212Aelvin scale", []string{"kel", "sc"}, []Span{{"Kel", true}, {"vin ", false}, {"\u212Ael", true}, {"vin ", false}, {"sc", true}, {"ale", false}}},
		{"ledger-ledger", []string{"ledger"}, []Span{{"ledger", true}, {"-", false}, {"ledger", true}}},
		{"Checkout double charge", []string{"double", "charge"}, []Span{{"Checkout ", false}, {"double", true}, {" ", false}, {"charge", true}}},
	}
	for _, c := range cases {
		if got := Marks(c.title, c.words); !slices.Equal(got, c.want) {
			t.Errorf("Marks(%q, %q) = %+v, want %+v", c.title, c.words, got, c.want)
		}
	}
}
