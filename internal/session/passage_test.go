package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

type doc struct{ title, body string }

func searchOf(t *testing.T, docs map[string]doc) *sql.DB {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	db, err := OpenSearch()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for path, d := range docs {
		if _, err := db.Exec("INSERT INTO docs (path, title, body) VALUES (?, ?, ?)", path, d.title, d.body); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func bodies(texts map[string]string) map[string]doc {
	out := map[string]doc{}
	for path, body := range texts {
		out[path] = doc{"Billing work", body}
	}
	return out
}

func filler(words int) string {
	vocabulary := strings.Fields("we checked a ledger entry and moved on to it while one more run went by")
	out := make([]string, words)
	for i := range out {
		out[i] = vocabulary[i%len(vocabulary)]
	}
	return strings.Join(out, " ")
}

var oldWords = regexp.MustCompile(`[\p{L}\p{N}_]+`)

func oldMatch(query, sep string, everyPrefix bool) string {
	var parts []string
	for _, w := range oldWords.FindAllString(strings.ToLower(query), -1) {
		if len(w) == 1 && !everyPrefix {
			parts = append(parts, `"`+w+`"`)
		} else {
			parts = append(parts, `"`+w+`"*`)
		}
	}
	return strings.Join(parts, sep)
}

func oldSnippet(t *testing.T, db *sql.DB, path, match string, tokens int, before, after string, hitsOnly bool) string {
	t.Helper()
	var text string
	err := db.QueryRow("SELECT snippet(docs, 2, char(1), char(2), '…', ?) FROM docs WHERE docs MATCH ? AND path = ?", tokens, match, path).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) || hitsOnly && err == nil && !strings.Contains(text, "\x01") {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(strings.NewReplacer("\x01", before, "\x02", after).Replace(text)), " ")
}

func oldPassage(t *testing.T, db *sql.DB, path, query string) string {
	return oldSnippet(t, db, path, oldMatch(query, " ", false), passageTokens, "", "", true)
}

func boundedWindows(t *testing.T, db *sql.DB, query string, tokens int) map[string]string {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	phrases := phrasesOf(query)
	heads, long, err := headsOf(ctx, conn, joined(phrases, " OR "), "")
	if err != nil {
		t.Fatal(err)
	}
	words, done, err := scratch(ctx, "CREATE VIRTUAL TABLE words USING fts5(word, tokenize='porter unicode61')",
		"CREATE VIRTUAL TABLE word_terms USING fts5vocab(words, 'instance')")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	terms, err := termsOf(ctx, conn, words, phrases)
	if err != nil {
		t.Fatal(err)
	}
	starts, err := bestStarts(ctx, conn, terms, long, tokens)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := windowsAt(ctx, conn, words, heads, starts, tokens, 2*tokens+len(phrases))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, w := range windows {
		out[w.path] = w.text
	}
	return out
}

func TestShortSessionsKeepTheExactPassageAndPreviewTheyHadBefore(t *testing.T) {
	texts := map[string]string{
		"/a": "the checkout double-charges on slow networks. Use an idempotency key derived from the cart id. Stripe keeps it for 24 hours.",
		"/b": filler(300) + " the idempotency key is new. " + filler(30),
		"/c": "fix the bug where the bug tracker drops the bug label. " + filler(250),
		"/d": "café naïve 東京 the ledger_sync job retried. " + filler(20),
	}
	for i := range 150 {
		texts[fmt.Sprintf("/many/%03d", i)] = filler(10+i) + fmt.Sprintf(" bug %d fixed. ", i) + filler(i%7)
	}
	db := searchOf(t, bodies(texts))
	for _, query := range []string{"the", "idempotency", "key the", "bug", "billing", "fix the bug", "stripe 24", "ledger_sync", "a", "work bug"} {
		got := Passages(db, query)
		for path := range texts {
			if want := oldPassage(t, db, path, query); got[path] != want {
				t.Errorf("%s for %q: got %q, want %q", path, query, got[path], want)
			}
			if path < "/e" || path == "/many/007" {
				want := oldSnippet(t, db, path, oldMatch(query, " ", false), passageTokens, "[", "]", false)
				if preview := Snippet(db, path, query, "[", "]"); preview != want {
					t.Errorf("preview %s for %q: got %q, want %q", path, query, preview, want)
				}
			}
		}
	}
}

func TestADeepWordGetsThePassageFts5GivesFromABoundedWindow(t *testing.T) {
	cases := []struct {
		name, body, query, word string
	}{
		{"plain", filler(5000) + " then the reconciliation of the March ledger failed twice. " + filler(5000), "reconciliation", "reconciliation"},
		{"after unicode", strings.Repeat("Ünïcode café 東京 ﬁx x² 🙂 ✅ q̀ ᦰ ⸗ ", 400) + filler(40) + " the reconciliation failed. " + filler(5000), "reconciliation", "reconciliation"},
		{"after lone marks", "alpha " + strings.Repeat("x ́ ", 1000) + "alpha beta " + strings.Repeat("x ", 1000), "alpha beta", "alpha beta"},
		{"after long words", "alpha " + strings.Repeat(strings.Repeat("x", 300)+" ", 10) + "alpha beta " + strings.Repeat("x ", 100), "alpha beta", "alpha beta"},
		{"stem", filler(5000) + " it charges cards through payout_sync now. " + filler(5000), "charging", "charges"},
		{"underscore", strings.Repeat("x ", 1500) + "refund payout payout " + strings.Repeat("x ", 1000) + "payout_sync refund " + strings.Repeat("x ", 1500), "payout_sync refund", "payout_sync refund"},
		{"underscore beside its parts", strings.Repeat("payout ", 6) + strings.Repeat("x ", 1500) + "refund sync sync " + strings.Repeat("x ", 1000) + "payout_sync refund " + strings.Repeat("x ", 1500), "payout_sync refund", "payout_sync refund"},
		{"underscore beside a common part", strings.Repeat("payout ", 40) + strings.Repeat("x ", 1500) + "refund sync sync " + strings.Repeat("x ", 1000) + "payout_sync refund " + strings.Repeat("x ", 1500), "payout_sync refund", "payout_sync refund"},
		{"underscore prefix", filler(5000) + " it charges cards through payout_sync now. " + filler(5000), "payout_sy", "payout_sync"},
		{"together", filler(3000) + " we deploy " + filler(3000) + " then the payout webhook deploy retried. " + filler(3000), "deploy webhook", "webhook deploy"},
	}
	for _, c := range cases {
		db := searchOf(t, bodies(map[string]string{"/a": c.body}))
		got, want := Passages(db, c.query)["/a"], oldPassage(t, db, "/a", c.query)
		if got != want || !strings.Contains(got, c.word) {
			t.Errorf("%s: got %q, want fts5's %q, holding %q", c.name, got, want, c.word)
		}
		if window := boundedWindows(t, db, c.query, passageTokens)["/a"]; !strings.Contains(window, c.word) || len(window) >= len(c.body) {
			t.Errorf("%s: the bounded window %q does not hold %q", c.name, window, c.word)
		}
	}
}

func TestAWordLongerThanTheWidestWindowIsCutBetweenCharacters(t *testing.T) {
	db := searchOf(t, bodies(map[string]string{"/a": strings.Repeat("x ", 1500) + strings.Repeat("界", 6000) + " " + filler(100)}))
	got := Passages(db, "界")["/a"]
	if !utf8.ValidString(got) || strings.Contains(got, "\uFFFD") || !strings.HasPrefix(got, "…界界") || len(got) > widestWindow+10 {
		t.Fatalf("got %d bytes starting %q", len(got), got[:min(len(got), 12)])
	}
}

func TestACommonWordInALongSessionComesFromItsStart(t *testing.T) {
	db := searchOf(t, bodies(map[string]string{
		"/a": "Ship the invoice export today. " + strings.Repeat("the ledger and the entry. ", 2000),
		"/b": strings.Repeat("longidentifierabcdefghijklmnop ", 100) + strings.Repeat("the ", 5000),
	}))
	got := Passages(db, "the")
	if want := "Ship the invoice export today. the ledger and the entry…"; got["/a"] != want {
		t.Errorf("got %q, want %q", got["/a"], want)
	}
	if want := oldPassage(t, db, "/b", "the"); got["/b"] != want || want == "" {
		t.Errorf("got %q, want fts5's %q", got["/b"], want)
	}
}

func TestALongSessionThatMatchesOnlyByTitleHasNoPassageButKeepsItsPreview(t *testing.T) {
	db := searchOf(t, bodies(map[string]string{"/a": filler(5000), "/b": strings.Repeat("x", 3000) + " " + filler(5000)}))
	if got := Passages(db, "billing"); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
	for _, path := range []string{"/a", "/b"} {
		want := oldSnippet(t, db, path, oldMatch("billing", " ", false), passageTokens, "[", "]", false)
		if got := Snippet(db, path, "billing", "[", "]"); got != want || got == "" {
			t.Errorf("preview %s: got %q, want %q", path, got, want)
		}
	}
}

func TestAPhraseFoundNowhereNearItsPartsStillGetsItsPassage(t *testing.T) {
	db := searchOf(t, bodies(map[string]string{"/a": strings.Repeat("payout gap sync gap ", 100) + filler(1000) + " payout_sync"}))
	if got, want := Passages(db, "payout_sync")["/a"], oldPassage(t, db, "/a", "payout_sync"); got != want || !strings.Contains(got, "payout_sync") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAskCardsGetAPassageForAnyWordEvenAfterLongWords(t *testing.T) {
	texts := map[string]string{
		"/a": "start " + strings.Repeat("longidentifierabcdefghij ", 60) + "the refund was issued twice. " + filler(4000),
		"/b": filler(30) + " a deploy of the webhook. " + filler(30),
		"/c": filler(4000),
	}
	db := searchOf(t, bodies(texts))
	got, err := PassagesOfAny(db, []string{"refund", "webhook"}, 40)
	if err != nil {
		t.Fatal(err)
	}
	for path := range texts {
		want := oldSnippet(t, db, path, oldMatch("refund webhook", " OR ", true), 40, "", "", true)
		if got[path] != want {
			t.Errorf("%s: got %q, want %q", path, got[path], want)
		}
	}
	if !strings.Contains(got["/a"], "refund") || got["/c"] != "" {
		t.Errorf("got %q", got)
	}
}

func TestTokensAreCountedWhereFts5CountsThem(t *testing.T) {
	pieces := []struct {
		text  string
		token bool
	}{
		{"Ünïcode", true}, {" ", false}, {"café", true}, {"_", false}, {"naïve", true}, {" ́ ", false},
		{"́abc", true}, {" ", false}, {"東京", true}, {" ", false}, {"ﬁx", true}, {" ", false}, {"x²", true},
		{" ✅ ", false}, {"🙂", true}, {" ᦰ ", false}, {"q̀q", true}, {"​", false}, {"ab١٢", true}, {" ", false},
	}
	var text strings.Builder
	var want []int
	for _, p := range pieces {
		if p.token {
			want = append(want, text.Len())
		}
		text.WriteString(p.text)
	}
	db := searchOf(t, bodies(map[string]string{"/a": text.String()}))
	db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS temp.instances USING fts5vocab(main, docs, 'instance')")
	var count int
	if err := db.QueryRow("SELECT count(*) FROM temp.instances WHERE col = 'body'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(want) {
		t.Fatalf("fts5 counts %d tokens, the test expects %d", count, len(want))
	}
	words, done, err := scratch(t.Context(), "CREATE VIRTUAL TABLE words USING fts5(word, tokenize='porter unicode61')",
		"CREATE VIRTUAL TABLE word_terms USING fts5vocab(words, 'instance')")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	numbers := make([]int, len(want)+1)
	for i := range numbers {
		numbers[i] = i
	}
	got, err := byteOffsets(t.Context(), words, map[int64]string{1: text.String()}, map[int64][]int{1: numbers})
	if err != nil {
		t.Fatal(err)
	}
	if want := append(want, text.Len()); !slices.Equal(got[1], want) {
		t.Fatalf("token starts %v, want %v", got[1], want)
	}
	later, err := byteOffsets(t.Context(), words, map[int64]string{1: "ᦰ abc 🙂 target"}, map[int64][]int{1: {1}})
	if err != nil {
		t.Fatal(err)
	}
	if later[1][0] != 8 {
		t.Fatalf("a character first met after the first walk stopped moved token 1 to byte %d, want 8", later[1][0])
	}
}
