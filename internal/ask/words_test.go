package ask

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/adammiribyan/superfind/internal/session"
)

func wordsCorpus(t *testing.T) (*sql.DB, []session.Row) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	db, err := session.OpenSearch()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	docs := [][3]string{
		{"/s/vault", "Card vault", "tokenize card numbers before they reach the logs"},
		{"/s/invoice", "Invoice layout", "the billing webhook now sends the invoice twice as a retry"},
		{"/s/webhook", "Webhook signatures", "verify each delivery and rotate the signing secret"},
	}
	var rows []session.Row
	for _, d := range docs {
		if _, err := db.Exec("INSERT INTO docs (path, title, body) VALUES (?, ?, ?)", d[0], d[1], d[2]); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, session.Row{Path: d[0], Session: session.Session{Title: d[1], Prompts: 5, Mtime: 1}})
	}
	return db, rows
}

func TestWhenNoRankerAnswersAskRanksTheSessionsByYourWordsAndSaysWhy(t *testing.T) {
	cases := []struct {
		name, key, claude, fallback string
	}{
		{"no key and no Claude Code", "", "", "Ask needs a TypeSafe key or Claude Code."},
		{"TypeSafe fails", "a-key", "", "Ask failed: TypeSafe answered 500."},
		{"Claude Code fails", "", "exit 1", "Ask failed: Claude Code didn't answer."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, rows := wordsCorpus(t)
			t.Setenv("PATH", "/usr/bin:/bin")
			if c.claude != "" {
				stubClaude(t, c.claude)
			}
			t.Setenv("TYPESAFE_API_KEY", c.key)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
			defer server.Close()
			t.Setenv("TYPESAFE_URL", server.URL)
			answer, err := Ask(context.Background(), db, "the webhook signing", rows)
			want := []string{"/s/webhook", "/s/invoice"}
			if err != nil || !slices.Equal(answer.Paths, want) || answer.Fallback != c.fallback {
				t.Fatalf("Ask gave %v and %q (%v), want %v and %q", answer.Paths, answer.Fallback, err, want, c.fallback)
			}
		})
	}
}

func TestWhenClaudeCodeAnswersAskKeepsItsOrder(t *testing.T) {
	db, rows := wordsCorpus(t)
	t.Setenv("TYPESAFE_API_KEY", "")
	stubClaude(t, `cat > /dev/null; echo '{"result": "[0]"}'`)
	answer, err := Ask(context.Background(), db, "the webhook signing", rows)
	if err != nil || len(answer.Paths) != 1 || answer.Fallback != "" {
		t.Fatalf("Ask gave %v and %q (%v), want one session ranked by Claude Code and no fallback", answer.Paths, answer.Fallback, err)
	}
}

func TestCancellingAskStopsAKeywordSearchThatIsWaitingForTheIndex(t *testing.T) {
	db, rows := wordsCorpus(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("TYPESAFE_API_KEY", "")
	db.SetMaxOpenConns(1)
	busy, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Ask(ctx, db, "webhook", rows)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a cancelled ask returned %v, want the deadline", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a cancelled ask was still waiting for the index after 3 s")
	}
}
