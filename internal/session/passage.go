package session

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"runtime"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const (
	passageTokens   = 10
	wholeRunes      = 2048
	leadingRunes    = 256
	widestWindow    = 16384
	denseHits       = 32
	windowsPerShard = 64
	maxShards       = 4
)

type phrase struct {
	word   string
	prefix bool
}

func phrasesOf(query string) []phrase {
	var out []phrase
	for _, w := range wordPattern.FindAllString(strings.ToLower(query), -1) {
		out = append(out, phrase{w, len(w) > 1})
	}
	return out
}

func (p phrase) String() string {
	if p.prefix {
		return `"` + p.word + `"*`
	}
	return `"` + p.word + `"`
}

func joined(phrases []phrase, sep string) string {
	parts := make([]string, len(phrases))
	for i, p := range phrases {
		parts[i] = p.String()
	}
	return strings.Join(parts, sep)
}

type window struct {
	path          string
	text          string
	before, after bool
}

type term struct {
	low, high   string
	count, docs int
}

func (t term) dense() bool { return t.count > denseHits*max(t.docs, 1) }

type phraseTerms []term

func (p phraseTerms) rarest() int {
	best := 0
	for j, t := range p {
		if t.count < p[best].count {
			best = j
		}
	}
	return best
}

type charClass uint8

const (
	separator charClass = iota
	joiner
	letter
)

func Passages(db *sql.DB, query string) map[string]string {
	found, _ := marked(db, phrasesOf(query), false, passageTokens, "")
	for path, text := range found {
		found[path] = unmarked(text, "", "")
	}
	return found
}

func Snippet(db *sql.DB, path, query, before, after string) string {
	found, _ := marked(db, phrasesOf(query), false, passageTokens, path)
	return unmarked(found[path], before, after)
}

func PassagesOfAny(db *sql.DB, words []string, tokens int) (map[string]string, error) {
	found, err := marked(db, phrasesOf(strings.Join(words, " ")), true, tokens, "")
	for path, text := range found {
		found[path] = unmarked(text, "", "")
	}
	return found, err
}

func unmarked(text, before, after string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\x01", before, "\x02", after).Replace(text)), " ")
}

func marked(db *sql.DB, phrases []phrase, anyWord bool, tokens int, only string) (map[string]string, error) {
	if len(phrases) == 0 || db == nil {
		return nil, nil
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return nil, err
	}
	defer conn.ExecContext(ctx, "ROLLBACK")
	either := joined(phrases, " OR ")
	match := joined(phrases, " ")
	if anyWord {
		match = either
	}
	heads, long, err := headsOf(ctx, conn, match, only)
	if err != nil {
		return nil, err
	}
	first := map[int64]window{}
	for id, w := range heads {
		first[id] = w
		if w.after && len(phrases) == 1 {
			first[id] = leading(w)
		}
	}
	if len(long) == 0 {
		found, err := snippets(ctx, first, either, tokens, true)
		if err != nil {
			return nil, err
		}
		return withOpening(ctx, conn, found, heads, only, tokens)
	}
	words, done, err := scratch(ctx, "CREATE VIRTUAL TABLE words USING fts5(word, tokenize='porter unicode61')",
		"CREATE VIRTUAL TABLE word_terms USING fts5vocab(words, 'instance')")
	if err != nil {
		return nil, err
	}
	defer done()
	terms, err := termsOf(ctx, conn, words, phrases)
	if err != nil {
		return nil, err
	}
	span := 2*tokens + len(phrases)
	sparse := slices.DeleteFunc(slices.Clone(terms), func(p phraseTerms) bool { return p[p.rarest()].dense() })
	starts, err := bestStarts(ctx, conn, sparse, long, tokens)
	if err != nil {
		return nil, err
	}
	anchored, err := windowsAt(ctx, conn, words, heads, starts, tokens, span)
	if err != nil {
		return nil, err
	}
	maps.Copy(first, anchored)
	found, err := snippets(ctx, first, either, tokens, true)
	if err != nil {
		return nil, err
	}
	missing := func() []int64 {
		return slices.DeleteFunc(slices.Clone(long), func(id int64) bool { return found[heads[id].path] != "" })
	}
	if ids := missing(); len(ids) > 0 {
		prefixes := map[int64]window{}
		for _, id := range ids {
			if first[id] != heads[id] {
				prefixes[id] = heads[id]
			}
		}
		more, err := snippets(ctx, prefixes, either, tokens, true)
		if err != nil {
			return nil, err
		}
		maps.Copy(found, more)
	}
	if ids := missing(); len(ids) > 0 {
		starts, err := firstStarts(ctx, conn, terms, ids)
		if err != nil {
			return nil, err
		}
		located, err := windowsAt(ctx, conn, words, heads, starts, tokens, span)
		if err != nil {
			return nil, err
		}
		more, err := snippets(ctx, located, either, tokens, true)
		if err != nil {
			return nil, err
		}
		maps.Copy(found, more)
	}
	if ids := missing(); len(ids) > 0 {
		whole := map[int64]window{}
		for _, id := range ids {
			var body string
			if err := conn.QueryRowContext(ctx, "SELECT body FROM docs WHERE rowid = ?", id).Scan(&body); err != nil {
				return nil, err
			}
			whole[id] = window{path: heads[id].path, text: body}
		}
		more, err := snippets(ctx, whole, either, tokens, true)
		if err != nil {
			return nil, err
		}
		maps.Copy(found, more)
	}
	return withOpening(ctx, conn, found, heads, only, tokens)
}

func withOpening(ctx context.Context, conn *sql.Conn, found map[string]string, heads map[int64]window, only string, tokens int) (map[string]string, error) {
	if only == "" || found[only] != "" {
		return found, nil
	}
	openings := map[int64]window{}
	for id, w := range heads {
		if err := conn.QueryRowContext(ctx, "SELECT body FROM docs WHERE rowid = ?", id).Scan(&w.text); err != nil {
			return nil, err
		}
		w.after = false
		openings[id] = w
	}
	return snippets(ctx, openings, "tag : x", tokens, false)
}

func scratch(ctx context.Context, statements ...string) (*sql.Conn, func(), error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	done := func() {
		conn.Close()
		db.Close()
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			done()
			return nil, nil, err
		}
	}
	return conn, done, nil
}

func headsOf(ctx context.Context, conn *sql.Conn, match, only string) (map[int64]window, []int64, error) {
	query := "SELECT rowid, path, substr(body, 1, ?) FROM docs WHERE docs MATCH ?"
	args := []any{wholeRunes + 1, match}
	if only != "" {
		query += " AND path = ?"
		args = append(args, only)
	}
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	heads := map[int64]window{}
	var long []int64
	for rows.Next() {
		var id int64
		var w window
		if err := rows.Scan(&id, &w.path, &w.text); err != nil {
			return nil, nil, err
		}
		if utf8.RuneCountInString(w.text) > wholeRunes {
			w.text, w.after = clipAtSpace(w.text, wholeRunes)
			long = append(long, id)
		}
		heads[id] = w
	}
	return heads, long, rows.Err()
}

func leading(w window) window {
	w.text, _ = clipAtSpace(w.text, leadingRunes)
	return w
}

func snippets(ctx context.Context, windows map[int64]window, match string, tokens int, hitsOnly bool) (map[string]string, error) {
	found := map[string]string{}
	if len(windows) == 0 {
		return found, nil
	}
	ids := slices.Sorted(maps.Keys(windows))
	shards := min(runtime.GOMAXPROCS(0), maxShards, (len(ids)+windowsPerShard-1)/windowsPerShard)
	results := make([]map[string]string, shards)
	errs := make([]error, shards)
	var wg sync.WaitGroup
	for i := range shards {
		wg.Go(func() {
			results[i], errs[i] = snippetsOf(ctx, ids[i*len(ids)/shards:(i+1)*len(ids)/shards], windows, match, tokens, hitsOnly)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	for _, r := range results {
		maps.Copy(found, r)
	}
	return found, nil
}

func snippetsOf(ctx context.Context, ids []int64, windows map[int64]window, match string, tokens int, hitsOnly bool) (map[string]string, error) {
	pairs := make([][2]any, len(ids))
	for i, id := range ids {
		pairs[i] = [2]any{id, windows[id].text}
	}
	encoded, err := json.Marshal(pairs)
	if err != nil {
		return nil, err
	}
	conn, done, err := scratch(ctx, "CREATE VIRTUAL TABLE windows USING fts5(body, tag, tokenize='porter unicode61')")
	if err != nil {
		return nil, err
	}
	defer done()
	if _, err := conn.ExecContext(ctx, "INSERT INTO windows(rowid, body, tag) SELECT value->>0, value->>1, 'x' FROM json_each(?)", string(encoded)); err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, "SELECT rowid, snippet(windows, 0, char(1), char(2), '…', ?) FROM windows WHERE windows MATCH ?", tokens, match)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]string{}
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			return nil, err
		}
		if hitsOnly && !strings.Contains(text, "\x01") {
			continue
		}
		w := windows[id]
		if w.before && !strings.HasPrefix(text, "…") {
			text = "…" + text
		}
		if w.after && !strings.HasSuffix(text, "…") {
			text += "…"
		}
		found[w.path] = text
	}
	return found, rows.Err()
}

func termsOf(ctx context.Context, conn, words *sql.Conn, phrases []phrase) ([]phraseTerms, error) {
	for _, vocabulary := range []string{"terms USING fts5vocab(main, docs, 'row')", "instances USING fts5vocab(main, docs, 'instance')"} {
		if _, err := conn.ExecContext(ctx, "CREATE VIRTUAL TABLE IF NOT EXISTS temp."+vocabulary); err != nil {
			return nil, err
		}
	}
	entries := make([][2]any, len(phrases))
	for i, p := range phrases {
		entries[i] = [2]any{i, p.word}
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	if _, err := words.ExecContext(ctx, "INSERT INTO words(rowid, word) SELECT value->>0, value->>1 FROM json_each(?)", string(encoded)); err != nil {
		return nil, err
	}
	defer words.ExecContext(ctx, "DELETE FROM words")
	rows, err := words.QueryContext(ctx, "SELECT doc, term FROM word_terms ORDER BY doc, offset")
	if err != nil {
		return nil, err
	}
	byPhrase := make([]phraseTerms, len(phrases))
	for rows.Next() {
		var i int
		var text string
		if err := rows.Scan(&i, &text); err != nil {
			rows.Close()
			return nil, err
		}
		byPhrase[i] = append(byPhrase[i], term{low: text, high: text})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []phraseTerms
	for i, p := range byPhrase {
		if len(p) == 0 {
			continue
		}
		if phrases[i].prefix {
			p[len(p)-1].high += string(utf8.MaxRune)
		}
		for j := range p {
			if err := conn.QueryRowContext(ctx, "SELECT coalesce(sum(cnt), 0), coalesce(max(doc), 0) FROM temp.terms WHERE term >= ? AND term <= ?",
				p[j].low, p[j].high).Scan(&p[j].count, &p[j].docs); err != nil {
				return nil, err
			}
		}
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(a, b phraseTerms) int { return cmp.Compare(a[a.rarest()].count, b[b.rarest()].count) })
	return out, nil
}

func positions(ctx context.Context, conn *sql.Conn, t term, ids []int64) (map[int64][]int, error) {
	only, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, "SELECT doc, offset FROM temp.instances WHERE term >= ? AND term <= ? AND col = 'body' AND doc IN (SELECT value FROM json_each(?))",
		t.low, t.high, string(only))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]int{}
	for rows.Next() {
		var id int64
		var offset int
		if err := rows.Scan(&id, &offset); err != nil {
			return nil, err
		}
		out[id] = append(out[id], offset)
	}
	for _, offsets := range out {
		slices.Sort(offsets)
	}
	return out, rows.Err()
}

func bestStarts(ctx context.Context, conn *sql.Conn, phrases []phraseTerms, ids []int64, tokens int) (map[int64]int, error) {
	type hit struct{ offset, phrase int }
	hits := map[int64][]hit{}
	for pi, p := range phrases {
		anchor := p.rarest()
		candidates, err := positions(ctx, conn, p[anchor], ids)
		if err != nil {
			return nil, err
		}
		for j, t := range p {
			if j == anchor {
				continue
			}
			others, err := positions(ctx, conn, t, ids)
			if err != nil {
				return nil, err
			}
			for id, offsets := range candidates {
				candidates[id] = slices.DeleteFunc(offsets, func(o int) bool {
					_, found := slices.BinarySearch(others[id], o-anchor+j)
					return !found
				})
			}
		}
		for id, offsets := range candidates {
			for _, o := range offsets {
				hits[id] = append(hits[id], hit{max(0, o-anchor), pi})
			}
		}
	}
	starts := map[int64]int{}
	for id, hs := range hits {
		slices.SortFunc(hs, func(a, b hit) int { return cmp.Compare(a.offset, b.offset) })
		best := -1
		for i, h := range hs {
			seen := make([]bool, len(phrases))
			score := 0
			for _, in := range hs[i:] {
				if in.offset >= h.offset+tokens {
					break
				}
				if seen[in.phrase] {
					score++
				} else {
					seen[in.phrase] = true
					score += 1000
				}
			}
			if score > best {
				best, starts[id] = score, h.offset
			}
		}
	}
	return starts, nil
}

func firstStarts(ctx context.Context, conn *sql.Conn, phrases []phraseTerms, ids []int64) (map[int64]int, error) {
	starts := map[int64]int{}
	for _, p := range phrases {
		unseen := slices.DeleteFunc(slices.Clone(ids), func(id int64) bool {
			_, seen := starts[id]
			return seen
		})
		if len(unseen) == 0 {
			break
		}
		only, err := json.Marshal(unseen)
		if err != nil {
			return nil, err
		}
		anchor := p.rarest()
		rows, err := conn.QueryContext(ctx, "SELECT doc, min(offset) FROM temp.instances WHERE term >= ? AND term <= ? AND col = 'body' AND doc IN (SELECT value FROM json_each(?)) GROUP BY doc",
			p[anchor].low, p[anchor].high, string(only))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var offset int
			if err := rows.Scan(&id, &offset); err != nil {
				rows.Close()
				return nil, err
			}
			starts[id] = max(0, offset-anchor)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return starts, nil
}

func windowsAt(ctx context.Context, conn, words *sql.Conn, heads map[int64]window, starts map[int64]int, tokens, span int) (map[int64]window, error) {
	out := map[int64]window{}
	if len(starts) == 0 {
		return out, nil
	}
	bodies := map[int64]string{}
	for id := range starts {
		var body string
		if err := conn.QueryRowContext(ctx, "SELECT body FROM docs WHERE rowid = ?", id).Scan(&body); err != nil {
			return nil, err
		}
		bodies[id] = body
	}
	targets := map[int64][]int{}
	for id, start := range starts {
		targets[id] = []int{max(0, start-tokens), start, start + span}
	}
	offsets, err := byteOffsets(ctx, words, bodies, targets)
	if err != nil {
		return nil, err
	}
	for id, at := range offsets {
		body := bodies[id]
		from, hit, to := at[0], at[1], at[2]
		if to-from > widestWindow {
			from, to = hit, min(to, hit+widestWindow)
			for to < len(body) && !utf8.RuneStart(body[to]) {
				to--
			}
		}
		out[id] = window{path: heads[id].path, text: body[from:to], before: from > 0, after: to < len(body)}
	}
	return out, nil
}

func byteOffsets(ctx context.Context, words *sql.Conn, texts map[int64]string, numbers map[int64][]int) (map[int64][]int, error) {
	classes := map[rune]charClass{}
	for {
		out := map[int64][]int{}
		var unknown []rune
		for id, text := range texts {
			at, guessed := tokenBytes(text, numbers[id], classes)
			out[id] = at
			unknown = append(unknown, guessed...)
		}
		if len(unknown) == 0 {
			return out, nil
		}
		if err := classify(ctx, words, unknown, classes); err != nil {
			return nil, err
		}
	}
}

func classify(ctx context.Context, words *sql.Conn, runes []rune, classes map[rune]charClass) error {
	var probes [][2]any
	var asked []rune
	for _, r := range runes {
		if _, known := classes[r]; !known {
			classes[r] = separator
			probes = append(probes, [2]any{2 * len(asked), "q" + string(r) + "q"}, [2]any{2*len(asked) + 1, string(r)})
			asked = append(asked, r)
		}
	}
	if len(probes) == 0 {
		return nil
	}
	encoded, err := json.Marshal(probes)
	if err != nil {
		return err
	}
	if _, err := words.ExecContext(ctx, "INSERT INTO words(rowid, word) SELECT value->>0, value->>1 FROM json_each(?)", string(encoded)); err != nil {
		return err
	}
	defer words.ExecContext(ctx, "DELETE FROM words")
	rows, err := words.QueryContext(ctx, "SELECT doc, count(*) FROM word_terms GROUP BY doc")
	if err != nil {
		return err
	}
	defer rows.Close()
	tokensIn := map[int]int{}
	for rows.Next() {
		var doc, count int
		if err := rows.Scan(&doc, &count); err != nil {
			return err
		}
		tokensIn[doc] = count
	}
	for i, r := range asked {
		switch {
		case tokensIn[2*i+1] == 1:
			classes[r] = letter
		case tokensIn[2*i] == 1:
			classes[r] = joiner
		}
	}
	return rows.Err()
}

func tokenBytes(body string, numbers []int, classes map[rune]charClass) ([]int, []rune) {
	at := make([]int, len(numbers))
	for i := range at {
		at[i] = len(body)
	}
	var guessed []rune
	next, count := 0, 0
	inside, counted := false, false
	runStart := 0
	for i, r := range body {
		class := separator
		if r < utf8.RuneSelf {
			if '0' <= r && r <= '9' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' {
				class = letter
			}
		} else if known, ok := classes[r]; ok {
			class = known
		} else {
			guessed = append(guessed, r)
			switch {
			case unicode.IsLetter(r) || unicode.IsNumber(r):
				class = letter
			case unicode.Is(unicode.Mn, r):
				class = joiner
			}
		}
		if class == separator {
			inside = false
			continue
		}
		if !inside {
			inside, counted, runStart = true, false, i
		}
		if class == letter && !counted {
			counted = true
			for next < len(numbers) && numbers[next] <= count {
				at[next] = runStart
				next++
			}
			if next == len(numbers) {
				return at, guessed
			}
			count++
		}
	}
	return at, guessed
}

func clipAtSpace(text string, runes int) (string, bool) {
	for i := range text {
		if runes == 0 {
			if space := strings.LastIndexFunc(text[:i], unicode.IsSpace); space > 0 {
				return text[:space], true
			}
			return text[:i], true
		}
		runes--
	}
	return text, false
}
