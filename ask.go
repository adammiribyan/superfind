package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/adammiribyan/hop/internal/typesafe"
)

const (
	claudeTimeout = 90 * time.Second
	jevChunk      = 120
	jevShortlist  = 15
	explainTop    = 5
)

const askPrompt = "You rank a developer's past coding sessions by how relevant they are to a search. Each session is one " +
	"numbered line: title | project | age | last request | a passage from the conversation. Judge by meaning, " +
	"not shared words. Reply with only a JSON array of the numbers of up to 15 relevant sessions, best first, " +
	"or [] if none are relevant."

func askCards(db *sql.DB, query string, rows []Row) ([]Row, []string, error) {
	var kept []Row
	for _, r := range recentFirst(rows) {
		if !r.Session.Throwaway() {
			kept = append(kept, r)
		}
	}
	passages := map[string]string{}
	openings, err := queryPairs(db, "SELECT path, substr(body, 1, 240) FROM docs")
	if err != nil {
		return nil, nil, err
	}
	var anyWord []string
	for _, w := range meaningfulWords(query) {
		anyWord = append(anyWord, `"`+w+`"*`)
	}
	var matched [][2]string
	if len(anyWord) > 0 {
		matched, err = queryPairs(db, "SELECT path, snippet(docs, 2, '', '', '…', 40) FROM docs WHERE docs MATCH ?",
			strings.Join(anyWord, " OR "))
		if err != nil {
			return nil, nil, err
		}
	}
	for _, pair := range append(openings, matched...) {
		passages[pair[0]] = pair[1]
	}
	cards := make([]string, len(kept))
	for i, r := range kept {
		s := r.Session
		cards[i] = fmt.Sprintf("%s | %s | %s | %s | %s", s.Title, s.Project(), ago(s.Mtime),
			oneLine(s.LastPrompt, 160), oneLine(passages[r.Path], 300))
	}
	return kept, cards, nil
}

func jevRank(key, query string, rows []Row, cards []string) ([]string, error) {
	if len(cards) == 0 {
		return nil, nil
	}
	first := map[int]float64{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var failure error
	for start := 0; start < len(cards); start += jevChunk {
		end := min(start+jevChunk, len(cards))
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			var criteria typesafe.Options
			for i := start; i < end; i++ {
				criteria = append(criteria, typesafe.Option{Key: "s" + strconv.Itoa(i), Meaning: cards[i]})
			}
			criteria = append(criteria, typesafe.Option{Key: "none", Meaning: "None of these sessions is where the developer did this work."})
			answers, err := typesafe.Jev(key, map[string]any{"search": query}, map[string]any{"which": map[string]any{
				"type": "choice",
				"instructions": "A developer is searching their past AI coding sessions with `search`. " +
					"Which session is the one where they did the work the search describes?",
				"criteria": criteria,
			}})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failure = err
				return
			}
			probabilities := answers["which"].Probabilities
			if len(probabilities) == 0 {
				failure = errors.New("TypeSafe returned no shortlist")
				return
			}
			var picks []int
			for k, p := range probabilities {
				if k == "none" {
					continue
				}
				i, err := strconv.Atoi(strings.TrimPrefix(k, "s"))
				if !strings.HasPrefix(k, "s") || err != nil || i < start || i >= end || math.IsNaN(p) {
					failure = fmt.Errorf("TypeSafe returned an unknown session %q", k)
					return
				}
				picks = append(picks, i)
			}
			sort.Slice(picks, func(a, b int) bool {
				pa, pb := probabilities["s"+strconv.Itoa(picks[a])], probabilities["s"+strconv.Itoa(picks[b])]
				if pa != pb {
					return pa > pb
				}
				return picks[a] < picks[b]
			})
			for _, i := range picks[:min(jevShortlist, len(picks))] {
				first[i] = probabilities["s"+strconv.Itoa(i)]
			}
		}(start, end)
	}
	wg.Wait()
	if failure != nil {
		return nil, failure
	}
	sessions := map[string]string{}
	questions := map[string]any{}
	for i := range first {
		id := "s" + strconv.Itoa(i)
		sessions[id] = cards[i]
		questions[id] = map[string]any{
			"type":         "noul",
			"instructions": "Is session `sessions." + id + "` the one where the developer did the work described by `search`?",
			"criteria": map[string]string{
				"true":  "The session's title, requests, or passage show this specific work.",
				"false": "The session is only related in topic, or covers a different task.",
			},
		}
	}
	second, err := typesafe.Jev(key, map[string]any{"search": query, "sessions": sessions}, questions)
	if err != nil {
		return nil, err
	}
	order := make([]int, 0, len(first))
	for i := range first {
		if _, ok := second["s"+strconv.Itoa(i)]; !ok {
			return nil, fmt.Errorf("TypeSafe skipped session s%d", i)
		}
		order = append(order, i)
	}
	score := func(i int) float64 { return first[i] + second["s"+strconv.Itoa(i)].Noul }
	sort.Slice(order, func(a, b int) bool {
		if score(order[a]) != score(order[b]) {
			return score(order[a]) > score(order[b])
		}
		return order[a] < order[b]
	})
	paths := make([]string, len(order))
	for n, i := range order {
		paths[n] = rows[i].Path
	}
	return paths, nil
}

var errNoAnswer = errors.New("no answer")

var rankList = regexp.MustCompile(`\[[\d,\s]*\]`)

func claudeRank(query string, kept []Row, cards []string) ([]string, error) {
	var request strings.Builder
	request.WriteString("Search: " + query + "\n\nSessions:\n")
	for i, c := range cards {
		fmt.Fprintf(&request, "%d. %s\n", i, c)
	}
	base := []string{"-p", "--model", "haiku", "--no-session-persistence", "--tools", "",
		"--output-format", "json", "--system-prompt", askPrompt}
	for _, args := range [][]string{append(append([]string{}, base...), "--bare"), base} {
		out, err := runClaude(args, request.String())
		if err != nil {
			continue
		}
		var reply struct {
			Result string `json:"result"`
		}
		if json.Unmarshal(out, &reply) != nil {
			continue
		}
		var ranked []int
		if json.Unmarshal([]byte(rankList.FindString(reply.Result)), &ranked) != nil {
			continue
		}
		var paths []string
		for _, i := range ranked {
			if i >= 0 && i < len(kept) {
				paths = append(paths, kept[i].Path)
			}
		}
		return paths, nil
	}
	return nil, errNoAnswer
}

func runClaude(args []string, input string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Stdin = strings.NewReader(input)
	return cmd.Output()
}

type askFailure struct{ reason string }

func (a askFailure) Error() string { return "Deep search failed: " + a.reason + "." }

func ask(db *sql.DB, query string, rows []Row) ([]string, error) {
	kept, cards, err := askCards(db, query, rows)
	if err != nil {
		return nil, askFailure{"the search index couldn't be read (" + err.Error() + ")"}
	}
	key := typesafe.Key()
	if key != "" {
		if ranked, err := jevRank(key, query, kept, cards); err == nil && len(ranked) > 0 {
			return ranked, nil
		}
	}
	paths, err := claudeRank(query, kept, cards)
	if err == nil {
		return paths, nil
	}
	if key != "" {
		return nil, askFailure{"TypeSafe and Claude didn't answer"}
	}
	return nil, askFailure{"Claude Code didn't answer"}
}

var paragraphBreak = regexp.MustCompile(`\n\s*\n`)

func passagesOf(body, query string) []string {
	var pieces []string
	current := ""
	for _, paragraph := range paragraphBreak.Split(body, -1) {
		paragraph = strings.Join(strings.Fields(paragraph), " ")
		if current != "" && utf8.RuneCountInString(current)+1+utf8.RuneCountInString(paragraph) > 400 {
			pieces = append(pieces, current)
			current = ""
		}
		current = strings.TrimSpace(current + " " + paragraph)
	}
	if current != "" {
		pieces = append(pieces, current)
	}
	var words []string
	for _, w := range meaningfulWords(query) {
		if utf8.RuneCountInString(w) > 2 {
			words = append(words, w)
		}
	}
	mentions := func(piece string) bool {
		lower := strings.ToLower(piece)
		for _, w := range words {
			if strings.Contains(lower, w) {
				return true
			}
		}
		return false
	}
	sort.SliceStable(pieces, func(a, b int) bool { return mentions(pieces[a]) && !mentions(pieces[b]) })
	pieces = pieces[:min(len(pieces), 120)]
	for i, p := range pieces {
		if r := []rune(p); len(r) > 400 {
			pieces[i] = string(r[:400])
		}
	}
	return pieces
}

func explain(db *sql.DB, key, query string, paths []string) map[string]string {
	bodies := map[string]string{}
	for _, p := range paths {
		var body string
		if db.QueryRow("SELECT body FROM docs WHERE path = ?", p).Scan(&body) == nil {
			bodies[p] = body
		}
	}
	found := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range paths {
		pieces := passagesOf(bodies[p], query)
		if len(pieces) == 0 {
			continue
		}
		wg.Add(1)
		go func(path string, pieces []string) {
			defer wg.Done()
			var criteria typesafe.Options
			for i, piece := range pieces {
				criteria = append(criteria, typesafe.Option{Key: "p" + strconv.Itoa(i), Meaning: piece})
			}
			criteria = append(criteria, typesafe.Option{Key: "none", Meaning: "No passage shows this work."})
			answers, err := typesafe.Jev(key, map[string]any{"search": query}, map[string]any{"which": map[string]any{
				"type":         "choice",
				"instructions": "Which passage from this coding session best shows the work described by `search`?",
				"criteria":     criteria,
			}})
			if err != nil || answers["which"].Choice == "none" {
				return
			}
			for _, opt := range criteria {
				if opt.Key == answers["which"].Choice {
					mu.Lock()
					found[path] = opt.Meaning
					mu.Unlock()
				}
			}
		}(p, pieces)
	}
	wg.Wait()
	return found
}
