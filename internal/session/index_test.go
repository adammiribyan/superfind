package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"modernc.org/sqlite"
)

func sessionsSaying(from, to int, said string) map[string]parsed {
	results := map[string]parsed{}
	for i := from; i < to; i++ {
		body := fmt.Sprintf("session %d %s ", i, said) + strings.Repeat("words people said ", 100)
		results[fmt.Sprintf("/sessions/%d.jsonl", i)] = parsed{&Session{Title: "work", Cwd: "/work"}, body}
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
	odd := map[string]parsed{"/sessions/caf\xe9.jsonl": {&Session{Title: "work", Cwd: "/work"}, "first"}}
	if err := updateSearch(db, odd, nil, true); err != nil {
		t.Fatal(err)
	}
	odd["/sessions/caf\xe9.jsonl"] = parsed{&Session{Title: "work", Cwd: "/work"}, "second"}
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
