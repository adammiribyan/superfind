package render

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/adammiribyan/hop/internal/agent"
	"github.com/adammiribyan/hop/internal/session"
)

const (
	boldStart = "\x01"
	boldEnd   = "\x02"
)

var (
	boldSpan     = regexp.MustCompile(`\*\*(.+?)\*\*`)
	codeSpan     = regexp.MustCompile("`([^`]+)`")
	numberedItem = regexp.MustCompile(`^\d+\. `)
)

func displayWidth(s string) int { return ansi.StringWidth(s) }

func cutToWidth(s string, width int) (string, string) {
	used := 0
	for i, r := range s {
		w := ansi.StringWidth(string(r))
		if used+w > width {
			if i == 0 {
				return string(r), s[utf8.RuneLen(r):]
			}
			return s[:i], s[i:]
		}
		used += w
	}
	return s, ""
}

func clipText(line string, width int) string {
	if displayWidth(line) <= width {
		return line
	}
	head, _ := cutToWidth(line, max(width-1, 0))
	return head + Dim + "…" + Reset
}

func OneLine(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if displayWidth(text) <= width {
		return text
	}
	head, _ := cutToWidth(text, max(width-1, 0))
	return head + "…"
}

func expandTabs(text string) string {
	if !strings.Contains(text, "\t") {
		return text
	}
	var out strings.Builder
	column := 0
	for _, r := range text {
		switch r {
		case '\t':
			spaces := 8 - column%8
			out.WriteString(strings.Repeat(" ", spaces))
			column += spaces
		case '\n', '\r':
			out.WriteRune(r)
			column = 0
		default:
			out.WriteRune(r)
			column += ansi.StringWidth(string(r))
		}
	}
	return out.String()
}

func isWordLetter(r rune) bool { return r == '_' || unicode.IsLetter(r) }

func breaksAfterHyphen(r []rune, i int) bool {
	letter := func(j int) bool { return j >= 0 && j < len(r) && isWordLetter(r[j]) }
	before := letter(i-1) && (letter(i-2) || (i >= 3 && r[i-2] == '-' && letter(i-3)))
	after := letter(i+1) && (letter(i+2) || (i+3 < len(r) && r[i+2] == '-' && letter(i+3)))
	return before && after
}

func isWordChar(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func dashRunEnd(r []rune, i int) int {
	if i == 0 || !(isWordChar(r[i-1]) || strings.ContainsRune(`!"'&.,?`, r[i-1])) {
		return -1
	}
	j := i
	for j < len(r) && r[j] == '-' {
		j++
	}
	if j-i < 2 || j >= len(r) || !isWordChar(r[j]) {
		return -1
	}
	return j
}

func hyphenPieces(word string) []string {
	r := []rune(word)
	var pieces []string
	start := 0
	for i := 0; i < len(r); i++ {
		if r[i] != '-' {
			continue
		}
		if end := dashRunEnd(r, i); end > 0 {
			if i > start {
				pieces = append(pieces, string(r[start:i]))
			}
			pieces = append(pieces, string(r[i:end]))
			start, i = end, end-1
		} else if breaksAfterHyphen(r, i) {
			pieces = append(pieces, string(r[start:i+1]))
			start = i + 1
		}
	}
	return append(pieces, string(r[start:]))
}

func chunksOf(text string) []string {
	text = strings.NewReplacer("\n", " ", "\r", " ", "\v", " ", "\f", " ").Replace(expandTabs(text))
	var chunks []string
	for len(text) > 0 {
		spaceRun := text[0] == ' '
		end := strings.IndexFunc(text, func(r rune) bool { return (r == ' ') != spaceRun })
		if end < 0 {
			end = len(text)
		}
		piece := text[:end]
		text = text[end:]
		if spaceRun {
			chunks = append(chunks, piece)
			continue
		}
		chunks = append(chunks, hyphenPieces(piece)...)
	}
	return chunks
}

func wrap(text string, width int, first, rest string) []string {
	chunks := chunksOf(text)
	var lines []string
	for len(chunks) > 0 {
		indent := rest
		if len(lines) == 0 {
			indent = first
		}
		room := width - displayWidth(indent)
		if strings.TrimSpace(chunks[0]) == "" && len(lines) > 0 {
			chunks = chunks[1:]
			continue
		}
		var line []string
		used := 0
		for len(chunks) > 0 && used+displayWidth(chunks[0]) <= room {
			used += displayWidth(chunks[0])
			line = append(line, chunks[0])
			chunks = chunks[1:]
		}
		if len(chunks) > 0 && displayWidth(chunks[0]) > room {
			space := room - used
			if room < 1 {
				space = 1
			}
			head, tail := cutToWidth(chunks[0], space)
			if space <= 0 {
				head, tail = "", chunks[0]
			}
			if h := strings.LastIndex(head, "-"); h > 0 && strings.Trim(head[:h], "-") != "" {
				head, tail = chunks[0][:h+1], chunks[0][h+1:]
			}
			line = append(line, head)
			chunks[0] = tail
		}
		if len(line) > 0 && strings.TrimSpace(line[len(line)-1]) == "" {
			line = line[:len(line)-1]
		}
		if len(line) > 0 {
			lines = append(lines, indent+strings.Join(line, ""))
		}
	}
	return lines
}

func boldAcrossLines(wrapped []string) []string {
	var out []string
	inside := false
	for _, w := range wrapped {
		line := w
		if inside {
			line = Bold + line
		}
		line = strings.ReplaceAll(strings.ReplaceAll(line, boldStart, Bold), boldEnd, Reset)
		if (strings.Count(w, boldStart)+strings.Count(w, boldEnd))%2 == 1 {
			inside = !inside
		}
		if inside {
			line += Reset
		}
		out = append(out, line)
	}
	return out
}

var lineBreak = regexp.MustCompile(`\r\n|[\n\r\v\f\x1c\x1d\x1e\x{85}\x{2028}\x{2029}]`)

func splitLines(text string) []string {
	lines := lineBreak.Split(text, -1)
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func render(text string, width int) []string {
	var lines []string
	fenced := false
	for _, line := range splitLines(text) {
		stripped := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(stripped, "```"):
			fenced = !fenced
		case fenced:
			lines = append(lines, "    "+clipText(line, width-4))
		case strings.HasPrefix(stripped, "|"):
			cells := clipText(stripped, width-2)
			lines = append(lines, "  "+strings.ReplaceAll(cells, "|", Dim+"|"+Reset))
		case strings.HasPrefix(stripped, "#"):
			lines = append(lines, "  "+Bold+clipText(strings.TrimSpace(strings.TrimLeft(stripped, "#")), width-2)+Reset)
		default:
			indent := strings.Repeat(" ", utf8.RuneCountInString(line)-utf8.RuneCountInString(strings.TrimLeftFunc(line, unicode.IsSpace)))
			hang := indent
			if strings.HasPrefix(stripped, "- ") || strings.HasPrefix(stripped, "* ") || numberedItem.MatchString(stripped) {
				hang += "  "
			}
			marked := boldSpan.ReplaceAllString(stripped, boldStart+"$1"+boldEnd)
			wrapped := wrap(marked, width-2, indent, hang)
			if len(wrapped) == 0 {
				wrapped = []string{""}
			}
			for _, w := range boldAcrossLines(wrapped) {
				lines = append(lines, "  "+codeSpan.ReplaceAllString(w, Dim+"`"+Reset+"$1"+Dim+"`"+Reset))
			}
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func tilde(path string) string {
	h := agent.Home()
	if path == h || strings.HasPrefix(path, h+"/") {
		return "~" + path[len(h):]
	}
	return path
}

func Ago(mtime float64) string {
	seconds := session.Now() - mtime
	switch {
	case seconds < 60:
		return "just now"
	case seconds < 3600:
		return fmt.Sprintf("%dm ago", int(seconds/60))
	case seconds < 86400:
		return fmt.Sprintf("%dh ago", int(seconds/3600))
	case seconds < 7*86400:
		return fmt.Sprintf("%dd ago", int(seconds/86400))
	}
	t := time.Unix(int64(mtime), 0)
	if t.Year() == time.Now().Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}

func when(s session.Session) string {
	if s.Active() {
		return "active now"
	}
	return Ago(s.Mtime)
}
