package main

import (
	"database/sql"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

func conversation(path string) []turn {
	t, _ := readTranscript(path)
	return t.turns
}

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
	return head + ansiDim + "…" + ansiReset
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
			line = ansiBold + line
		}
		line = strings.ReplaceAll(strings.ReplaceAll(line, boldStart, ansiBold), boldEnd, ansiReset)
		if (strings.Count(w, boldStart)+strings.Count(w, boldEnd))%2 == 1 {
			inside = !inside
		}
		if inside {
			line += ansiReset
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
			lines = append(lines, "  "+strings.ReplaceAll(cells, "|", ansiDim+"|"+ansiReset))
		case strings.HasPrefix(stripped, "#"):
			lines = append(lines, "  "+ansiBold+clipText(strings.TrimSpace(strings.TrimLeft(stripped, "#")), width-2)+ansiReset)
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
				lines = append(lines, "  "+codeSpan.ReplaceAllString(w, ansiDim+"`"+ansiReset+"$1"+ansiDim+"`"+ansiReset))
			}
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func speaker(role string) string {
	if role == "user" {
		return ansiAccent + ansiBold + "you" + ansiReset
	}
	return ansiBold + "claude" + ansiReset
}

func excerpt(lines []string, keep int, fromEnd bool, note, secondary string) []string {
	if len(lines) <= keep {
		return lines
	}
	marker := "  " + secondary + "… " + note + ansiReset
	if fromEnd {
		return append([]string{marker}, lines[len(lines)-keep:]...)
	}
	return append(append([]string{}, lines[:keep]...), marker)
}

type previewRequest struct {
	path      string
	width     int
	recent    bool
	query     string
	explained string
	secondary string
}

func previewHeader(req previewRequest, cache map[string]cacheEntry, known map[string]statusEntry, db *sql.DB) []string {
	width := req.width - 2
	e, ok := cache[req.path]
	if !ok || e.Session == nil {
		return nil
	}
	s := *e.Session
	dirs := configDirs()
	where := tilde(s.Cwd)
	if survivor := survivingDir(s.Cwd); survivor != s.Cwd {
		where = "folder removed · resumes from " + tilde(survivor)
	}
	found := oneLine(plainPassage(req.explained), width)
	if found == "" && db != nil {
		found = snippet(db, req.path, req.query)
	}
	header := []string{
		ansiBold + oneLine(s.Title, width) + ansiReset,
		req.secondary + oneLine(strings.Join(metaParts(s, configDirOf(req.path, dirs), len(dirs) > 1, statusLabel(req.path, known)), " · "), width) + ansiReset,
		req.secondary + oneLine(where, width) + ansiReset,
		"",
	}
	if found != "" {
		header[3] = req.secondary + found + ansiReset
	}
	return header
}

func previewBody(req previewRequest) []string {
	width := req.width - 2
	turns := conversation(req.path)
	var body []string
	if req.recent {
		for _, t := range turns[max(len(turns)-8, 0):] {
			body = append(body, speaker(t.role))
			body = append(body, render(t.text, width)...)
			body = append(body, "")
		}
		return body
	}
	lastAny, lastSubstantial := -1, -1
	for i, t := range turns {
		if t.role == "user" {
			lastAny = i
			if len(strings.Fields(t.text)) > 3 {
				lastSubstantial = i
			}
		}
	}
	lastAsk := lastSubstantial
	if lastAsk < 0 {
		lastAsk = lastAny
	}
	if lastAsk >= 0 {
		body = append(body, speaker("user"))
		body = append(body, excerpt(render(turns[lastAsk].text, width), 10, false, "rest of your message", req.secondary)...)
	}
	reply := ""
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].role == "assistant" {
			reply = turns[i].text
			break
		}
	}
	switch {
	case reply != "" && (lastAsk < 0 || turns[len(turns)-1].role == "assistant"):
		body = append(body, "", speaker("assistant"))
		body = append(body, excerpt(render(reply, width), 18, true, "earlier in this reply", req.secondary)...)
	case lastAsk >= 0:
		body = append(body, "", req.secondary+"  No reply yet. Claude stopped before answering."+ansiReset)
	}
	return body
}

var markdownMarks = regexp.MustCompile("[*`#]+")

func plainPassage(passage string) string {
	return strings.Join(strings.Fields(markdownMarks.ReplaceAllString(passage, "")), " ")
}
