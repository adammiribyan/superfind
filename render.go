package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type turn struct {
	role string
	text string
}

func conversation(path string) []turn {
	var turns []turn
	eachLine(path, func(raw []byte) {
		if !bytes.Contains(raw, markUser) && !bytes.Contains(raw, markAssistant) {
			return
		}
		e := decode(raw)
		role := e.str("type")
		if role != "user" && role != "assistant" {
			return
		}
		text := strings.TrimSpace(messageText(e))
		if role == "user" {
			text = promptText(text)
		}
		if meta, _ := e["isMeta"].(bool); meta || text == "" {
			return
		}
		if n := len(turns); n > 0 && turns[n-1].role == role {
			turns[n-1].text += "\n\n" + text
		} else {
			turns = append(turns, turn{role, text})
		}
	})
	return turns
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

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func clipText(line string, width int) string {
	if runeLen(line) <= width {
		return line
	}
	r := []rune(line)
	return string(r[:max(width-1, 0)]) + ansiDim + "…" + ansiReset
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
	text = strings.NewReplacer("\t", "        ", "\n", " ", "\r", " ", "\v", " ", "\f", " ").Replace(text)
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
		room := width - runeLen(indent)
		if strings.TrimSpace(chunks[0]) == "" && len(lines) > 0 {
			chunks = chunks[1:]
			continue
		}
		var line []string
		used := 0
		for len(chunks) > 0 && used+runeLen(chunks[0]) <= room {
			used += runeLen(chunks[0])
			line = append(line, chunks[0])
			chunks = chunks[1:]
		}
		if len(chunks) > 0 && runeLen(chunks[0]) > room {
			r := []rune(chunks[0])
			cut := min(room-used, len(r))
			if room < 1 {
				cut = 1
			}
			for h := cut - 1; h > 0; h-- {
				if r[h] == '-' && strings.Trim(string(r[:h]), "-") != "" {
					cut = h + 1
					break
				}
			}
			line = append(line, string(r[:cut]))
			chunks[0] = string(r[cut:])
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

func render(text string, width int) []string {
	var lines []string
	fenced := false
	for _, line := range strings.Split(text, "\n") {
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
			indent := strings.Repeat(" ", len(line)-len(strings.TrimLeft(line, " \t")))
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
	secondary string
}

type previewText struct {
	header []string
	body   []string
}

func buildPreview(req previewRequest, cache map[string]cacheEntry, known map[string]statusEntry, db *sql.DB) previewText {
	width := req.width - 2
	var out previewText
	if e, ok := cache[req.path]; ok && e.Session != nil {
		s := *e.Session
		dirs := configDirs()
		survivor := survivingDir(s.Cwd)
		where := tilde(s.Cwd)
		if survivor != s.Cwd {
			where = "folder removed · resumes from " + tilde(survivor)
		}
		status := statusLabel(req.path, known)
		found := oneLine(explained(req.path, req.query), width)
		if found == "" {
			found = snippet(db, req.path, req.query)
		}
		out.header = []string{
			ansiBold + oneLine(s.Title, width) + ansiReset,
			req.secondary + oneLine(strings.Join(metaParts(s, configDirOf(req.path, dirs), len(dirs) > 1, status), " · "), width) + ansiReset,
			req.secondary + oneLine(where, width) + ansiReset,
			"",
		}
		if found != "" {
			out.header[3] = req.secondary + found + ansiReset
		}
	}
	turns := conversation(req.path)
	if req.recent {
		start := max(len(turns)-8, 0)
		for _, t := range turns[start:] {
			out.body = append(out.body, speaker(t.role))
			out.body = append(out.body, render(t.text, width)...)
			out.body = append(out.body, "")
		}
		return out
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
		out.body = append(out.body, speaker("user"))
		out.body = append(out.body, excerpt(render(turns[lastAsk].text, width), 10, false, "rest of your message", req.secondary)...)
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
		out.body = append(out.body, "", speaker("assistant"))
		out.body = append(out.body, excerpt(render(reply, width), 18, true, "earlier in this reply", req.secondary)...)
	case lastAsk >= 0:
		out.body = append(out.body, "", req.secondary+"  No reply yet. Claude stopped before answering."+ansiReset)
	}
	return out
}

type explainedFile struct {
	Query    string            `json:"query"`
	Passages map[string]string `json:"passages"`
}

var markdownMarks = regexp.MustCompile("[*`#]+")

func explained(path, query string) string {
	raw, err := os.ReadFile(cachePath("explained.json"))
	if err != nil {
		return ""
	}
	var saved explainedFile
	if json.Unmarshal(raw, &saved) != nil || saved.Query != query {
		return ""
	}
	return strings.Join(strings.Fields(markdownMarks.ReplaceAllString(saved.Passages[path], "")), " ")
}
