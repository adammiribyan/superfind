package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"modernc.org/sqlite"

	"github.com/adammiribyan/superfind/internal/agent"
)

func sessionsSaying(from, to int, said string) map[string]parsed {
	results := map[string]parsed{}
	for i := from; i < to; i++ {
		body := fmt.Sprintf("session %d %s ", i, said) + strings.Repeat("words people said ", 100)
		results[fmt.Sprintf("/sessions/%d.jsonl", i)] = parsed{entry: cacheEntry{Session: &Session{Title: "work", Cwd: "/work"}}, body: body}
	}
	return results
}

func pagesRead(t *testing.T, db *sql.DB) int {
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	total := 0
	err = conn.Raw(func(dc any) error {
		for _, op := range []sqlite.DBStatusOp{sqlite.DBStatusCacheHit, sqlite.DBStatusCacheMiss} {
			n, _, err := dc.(sqlite.DBStatus).Status(op, true)
			total += n
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return total
}

func freshSearch(t *testing.T) *sql.DB {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	db, err := OpenSearch()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	pagesRead(t, db)
	return db
}

func pagesToIndex(t *testing.T, sessions int) int {
	db := freshSearch(t)
	if err := updateSearch(db, sessionsSaying(0, sessions, "first"), nil, true); err != nil {
		t.Fatal(err)
	}
	return pagesRead(t, db)
}

func TestIndexingReadsInLineWithTheNumberOfSessions(t *testing.T) {
	few, many := pagesToIndex(t, 100), pagesToIndex(t, 800)
	if many > 16*few {
		t.Fatalf("indexing 8 times as many sessions read %d times as many pages (%d, then %d)", many/few, few, many)
	}
}

func pagesToRefresh(t *testing.T, changed int) int {
	db := freshSearch(t)
	if err := updateSearch(db, sessionsSaying(0, 400, "first"), nil, true); err != nil {
		t.Fatal(err)
	}
	pagesRead(t, db)
	if err := updateSearch(db, sessionsSaying(0, changed, "second"), []string{"/sessions/399.jsonl"}, false); err != nil {
		t.Fatal(err)
	}
	return pagesRead(t, db)
}

func TestRefreshingManySessionsReadsTheIndexOnce(t *testing.T) {
	one, many := pagesToRefresh(t, 1), pagesToRefresh(t, 40)
	if many > 8*one {
		t.Fatalf("refreshing 40 sessions read %d times the pages of refreshing one (%d, then %d)", many/one, one, many)
	}
}

func TestARefreshReplacesChangedSessionsAndDropsRemovedOnes(t *testing.T) {
	db := freshSearch(t)
	if err := updateSearch(db, sessionsSaying(0, 3, "first"), nil, true); err != nil {
		t.Fatal(err)
	}
	if err := updateSearch(db, sessionsSaying(1, 2, "second"), []string{"/sessions/2.jsonl"}, false); err != nil {
		t.Fatal(err)
	}
	got, err := QueryPairs(db, "SELECT path, substr(body, 1, 17) FROM docs ORDER BY path")
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"/sessions/0.jsonl", "session 0 first w"}, {"/sessions/1.jsonl", "session 1 second "}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("index holds %q, want %q", got, want)
	}
}

func TestARefreshReplacesASessionWhosePathIsNotUTF8(t *testing.T) {
	db := freshSearch(t)
	odd := map[string]parsed{"/sessions/caf\xe9.jsonl": {entry: cacheEntry{Session: &Session{Title: "work", Cwd: "/work"}}, body: "first"}}
	if err := updateSearch(db, odd, nil, true); err != nil {
		t.Fatal(err)
	}
	odd["/sessions/caf\xe9.jsonl"] = parsed{entry: cacheEntry{Session: &Session{Title: "work", Cwd: "/work"}}, body: "second"}
	if err := updateSearch(db, odd, nil, false); err != nil {
		t.Fatal(err)
	}
	got, err := QueryPairs(db, "SELECT path, body FROM docs")
	if err != nil {
		t.Fatal(err)
	}
	if want := [][2]string{{"/sessions/caf\xe9.jsonl", "second"}}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("index holds %q, want %q", got, want)
	}
}

func TestTheIndexKeepsReadsAndFailedEditsApartFromEditsAndReplacesThemOnRefresh(t *testing.T) {
	db := freshSearch(t)
	trail := agent.Trail{Edits: []agent.Edit{{Path: "/w/a.go", At: 10}, {Path: "/w/b.go", At: 20, Failed: true}},
		Reads: []agent.Edit{{Path: "/w/README.md", At: 5}}}
	results := map[string]parsed{"/sessions/1.jsonl": {entry: cacheEntry{Session: &Session{Title: "work", Cwd: "/work"}}, body: "x", trail: trail}}
	if err := updateSearch(db, results, nil, true); err != nil {
		t.Fatal(err)
	}
	stored := func() string {
		rows, err := db.Query("SELECT session, path, name, how, kind, failed, at FROM files ORDER BY path")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var session, path, name, how, kind string
			var failed bool
			var at float64
			if err := rows.Scan(&session, &path, &name, &how, &kind, &failed, &at); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(session, " ", path, " ", name, " ", how, " ", kind, " ", failed, " ", at))
		}
		return strings.Join(out, "\n")
	}
	want := "/sessions/1.jsonl /w/README.md README.md read tool false 5\n" +
		"/sessions/1.jsonl /w/a.go a.go edited tool false 10\n" +
		"/sessions/1.jsonl /w/b.go b.go edited tool true 20"
	if got := stored(); got != want {
		t.Fatalf("files holds\n%s\nwant\n%s", got, want)
	}
	results["/sessions/1.jsonl"] = parsed{entry: cacheEntry{Session: &Session{Title: "work", Cwd: "/work"}}, body: "x"}
	if err := updateSearch(db, results, nil, false); err != nil {
		t.Fatal(err)
	}
	if got := stored(); got != "" {
		t.Fatalf("a refreshed session with no files still holds\n%s", got)
	}
}
