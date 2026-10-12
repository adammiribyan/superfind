package session

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/adammiribyan/superfind/internal/store"
)

const (
	goneA = "aaaaaaaa-1111-4111-8111-111111111111"
	goneB = "bbbbbbbb-2222-4222-8222-222222222222"
)

func historyHome(t *testing.T) string {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "GROK_HOME", "SUPERFIND_CLAUDE_DIRS", "SUPERFIND_CODEX_DIRS", "SUPERFIND_GROK_DIRS"} {
		t.Setenv(name, "")
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func prompt(id, text string, ms int64) string {
	return fmt.Sprintf(`{"display":%q,"pastedContents":{},"timestamp":%d,"project":"/work/billing","sessionId":%q}`, text, ms, id)
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func indexed(t *testing.T) map[string]Session {
	t.Helper()
	rows, problems, err := Index(nil)
	if err != nil || len(problems) > 0 {
		t.Fatal(err, problems)
	}
	out := map[string]Session{}
	for _, r := range rows {
		out[r.Path] = r.Session
	}
	return out
}

func searched(t *testing.T) []string {
	t.Helper()
	db, err := OpenSearch()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	pairs, err := QueryPairs(db, "SELECT path, title FROM docs ORDER BY path")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range pairs {
		paths = append(paths, p[0])
	}
	return paths
}

func TestAPromptTypedInOneSessionReparsesOnlyThatSession(t *testing.T) {
	home := historyHome(t)
	history := filepath.Join(home, ".claude", "history.jsonl")
	lines := []string{prompt(goneA, "fix the billing retry loop", 1757685780000), prompt(goneB, "rename the webhook table", 1757685790000)}
	writeLines(t, history, lines...)
	indexed(t)
	raw, err := os.ReadFile(store.CachePath("index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c cacheFile
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	for _, e := range c.Files {
		e.Session.Title = "as cached"
	}
	if err := saveCache(c.Files); err != nil {
		t.Fatal(err)
	}
	writeLines(t, history, append(lines, prompt(goneA, "now add a test for the backoff", 1757686800000))...)
	got := indexed(t)
	a, b := got[history+"/"+goneA], got[history+"/"+goneB]
	if a.Title != "fix the billing retry loop" || a.LastPrompt != "now add a test for the backoff" || a.Prompts != 2 || a.Mtime != 1757686800 {
		t.Fatalf("A = %+v", a)
	}
	if b.Title != "as cached" || b.Ending != "transcript_deleted" {
		t.Fatalf("B = %+v", b)
	}
}

func TestPurgedPromptsAndAReturningTranscriptBothDropTheHistoryRow(t *testing.T) {
	home := historyHome(t)
	history := filepath.Join(home, ".claude", "history.jsonl")
	writeLines(t, history, prompt(goneA, "fix the billing retry loop", 1757685780000), prompt(goneB, "rename the webhook table", 1757685790000))
	indexed(t)
	if want := []string{history + "/" + goneA, history + "/" + goneB}; !reflect.DeepEqual(searched(t), want) {
		t.Fatalf("search holds %q", searched(t))
	}
	writeLines(t, history, prompt(goneB, "rename the webhook table", 1757685790000))
	transcript := filepath.Join(home, ".claude", "projects", "-work-billing", goneB+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	writeLines(t, transcript, `{"type":"user","cwd":"/work/billing","message":{"content":"rename the webhook table"}}`)
	got := indexed(t)
	if paths := slices.Sorted(maps.Keys(got)); !reflect.DeepEqual(paths, []string{transcript}) {
		t.Fatalf("rows = %q", paths)
	}
	if !reflect.DeepEqual(searched(t), []string{transcript}) {
		t.Fatalf("search holds %q", searched(t))
	}
}

func TestTheHomeWithTheLatestPromptKeepsASessionBothHomesRemember(t *testing.T) {
	home := historyHome(t)
	work := filepath.Join(home, ".claude-work")
	if err := os.MkdirAll(filepath.Join(work, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeLines(t, filepath.Join(home, ".claude", "history.jsonl"), prompt(goneA, "fix the billing retry loop", 1757685780000))
	writeLines(t, filepath.Join(work, "history.jsonl"), prompt(goneA, "fix the billing retry loop", 1757685780000),
		prompt(goneA, "and the refund path", 1757686800000))
	got := indexed(t)
	if s, ok := got[filepath.Join(work, "history.jsonl", goneA)]; len(got) != 1 || !ok || s.Prompts != 2 {
		t.Fatalf("rows = %+v", got)
	}
}
