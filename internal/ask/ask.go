package ask

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
	"github.com/adammiribyan/superfind/internal/typesafe"
)

const (
	claudeTimeout     = 90 * time.Second
	jevChunk          = 120
	cardPassageTokens = 40
	jevShortlist      = 15
	ExplainTop        = 5
)

const askPrompt = "You rank a developer's past coding sessions by how relevant they are to a search. Each session is one " +
	"numbered line: title | project | age | last request | a passage from the conversation. Judge by meaning, " +
	"not shared words. Reply with only a JSON array of the numbers of up to 15 relevant sessions, best first, " +
	"or [] if none are relevant."

func askCards(db *sql.DB, query string, rows []session.Row) ([]session.Row, []string, error) {
	var kept []session.Row
	for _, r := range session.RecentFirst(rows) {
		if !r.Session.Throwaway() {
			kept = append(kept, r)
		}
	}
	passages := map[string]string{}
	openings, err := session.QueryPairs(db, "SELECT path, substr(body, 1, 240) FROM docs")
	if err != nil {
		return nil, nil, err
	}
	for _, pair := range openings {
		passages[pair[0]] = pair[1]
	}
	matched, err := session.PassagesOfAny(db, session.MeaningfulWords(query), cardPassageTokens)
	if err != nil {
		return nil, nil, err
	}
	maps.Copy(passages, matched)
	cards := make([]string, len(kept))
	for i, r := range kept {
		s := r.Session
		cards[i] = fmt.Sprintf("%s | %s | %s | %s | %s", s.Title, s.Project(), render.Ago(s.Mtime),
			render.OneLine(s.LastPrompt, 160), render.OneLine(passages[r.Path], 300))
	}
	return kept, cards, nil
}

func jevRank(ctx context.Context, key, query string, rows []session.Row, cards []string) ([]string, error) {
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
			answers, err := typesafe.Jev(ctx, key, map[string]any{"search": query}, map[string]any{"which": map[string]any{
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
			none, answered := probabilities["none"]
			var picks []int
			for _, k := range slices.Sorted(maps.Keys(probabilities)) {
				p := probabilities[k]
				if k == "none" || (answered && p < none) {
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
	if len(first) == 0 {
		return nil, nil
	}
	second, err := typesafe.Jev(ctx, key, map[string]any{"search": query, "sessions": sessions}, questions)
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

func claudeRank(ctx context.Context, query string, kept []session.Row, cards []string) ([]string, error) {
	var request strings.Builder
	request.WriteString("Search: " + query + "\n\nSessions:\n")
	for i, c := range cards {
		fmt.Fprintf(&request, "%d. %s\n", i, c)
	}
	out, err := runClaude(ctx, []string{"-p", "--model", "haiku", "--no-session-persistence", "--tools", "",
		"--setting-sources=", "--strict-mcp-config", "--output-format", "json", "--system-prompt", askPrompt}, request.String())
	if err != nil {
		return nil, err
	}
	var reply struct {
		Result string `json:"result"`
	}
	var ranked []int
	if json.Unmarshal(out, &reply) != nil || json.Unmarshal([]byte(rankList.FindString(reply.Result)), &ranked) != nil {
		return nil, errNoAnswer
	}
	var paths []string
	for _, i := range ranked {
		if i >= 0 && i < len(kept) {
			paths = append(paths, kept[i].Path)
		}
	}
	return paths, nil
}

func runClaude(ctx context.Context, args []string, input string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, claudeTimeout)
	defer cancel()
	dir, err := os.MkdirTemp("", "superfind-ask-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
	cmd.Stdin = strings.NewReader(input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

type Failure struct{ reason string }

func (a Failure) Error() string { return "Ask failed: " + a.reason + "." }

func Ask(ctx context.Context, db *sql.DB, query string, rows []session.Row) ([]string, error) {
	kept, cards, err := askCards(db, query, rows)
	if err != nil {
		return nil, Failure{"the search index couldn't be read (" + err.Error() + ")"}
	}
	if len(cards) == 0 {
		return nil, nil
	}
	if key := typesafe.Key(); key != "" {
		ranked, err := jevRank(ctx, key, query, kept, cards)
		if err != nil && ctx.Err() == nil {
			return nil, Failure{err.Error()}
		}
		return ranked, ctx.Err()
	}
	paths, err := claudeRank(ctx, query, kept, cards)
	if err != nil && ctx.Err() == nil {
		return nil, Failure{"Claude Code didn't answer"}
	}
	return paths, ctx.Err()
}

var paragraphBreak = regexp.MustCompile(`\n\s*\n`)

const passageRunes = 400

func windows(text string) []string {
	var out []string
	r := []rune(text)
	for len(r) > passageRunes {
		cut := passageRunes
		for i := passageRunes; i > passageRunes/2; i-- {
			if r[i] == ' ' {
				cut = i
				break
			}
		}
		out = append(out, string(r[:cut]))
		for r = r[cut:]; len(r) > 0 && r[0] == ' '; r = r[1:] {
		}
	}
	return append(out, string(r))
}

func passagesOf(body, query string) []string {
	var pieces []string
	current := ""
	for _, paragraph := range paragraphBreak.Split(body, -1) {
		for _, part := range windows(strings.Join(strings.Fields(paragraph), " ")) {
			if current != "" && utf8.RuneCountInString(current)+1+utf8.RuneCountInString(part) > passageRunes {
				pieces = append(pieces, current)
				current = ""
			}
			current = strings.TrimSpace(current + " " + part)
		}
	}
	if current != "" {
		pieces = append(pieces, current)
	}
	var words []string
	for _, w := range session.MeaningfulWords(query) {
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
	return pieces[:min(len(pieces), 120)]
}

func Explain(ctx context.Context, db *sql.DB, key, query string, paths []string) map[string]string {
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
			answers, err := typesafe.Jev(ctx, key, map[string]any{"search": query}, map[string]any{"which": map[string]any{
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
