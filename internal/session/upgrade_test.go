package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func searchBytes() int64 {
	var total int64
	for _, suffix := range []string{"", "-wal"} {
		if info, err := os.Stat(CachePath("search.db" + suffix)); err == nil {
			total += info.Size()
		}
	}
	return total
}

func upgradeHome(t *testing.T, sessions int) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "GROK_HOME", "SUPERFIND_CLAUDE_DIRS", "SUPERFIND_CODEX_DIRS", "SUPERFIND_GROK_DIRS"} {
		t.Setenv(name, "")
	}
	for i := range sessions {
		writeSession(t, home, i, "words people said")
	}
	if _, _, err := Index(nil); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeSession(t *testing.T, home string, i int, said string) {
	projects := filepath.Join(home, ".claude", "projects", "-work")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"type":"user","cwd":"/work","message":{"content":"session %d %s"}}`, i, strings.Repeat(said+" ", 300))
	if err := os.WriteFile(filepath.Join(projects, fmt.Sprintf("0199%04d-a751-7860-aac9-738072c82895.jsonl", i)), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func upgrade(t *testing.T) []Row {
	if err := os.WriteFile(CachePath("index.json"), []byte(`{"version":1,"files":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, _, err := Index(nil)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAnIndexRebuiltAfterAnUpgradeIsTheSizeOfAFreshOne(t *testing.T) {
	upgradeHome(t, 300)
	fresh := searchBytes()
	rows := upgrade(t)
	if upgraded := searchBytes(); upgraded > fresh+fresh/20 {
		t.Fatalf("the index rebuilt after an upgrade takes %d bytes, a fresh one %d", upgraded, fresh)
	}
	db, err := OpenSearch()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if found, err := Search(db, "people said", rows); err != nil || len(found) != 300 {
		t.Fatalf("after the upgrade, searching found %d of 300 sessions (%v)", len(found), err)
	}
}

func TestASearchOpenDuringAnUpgradeSeesTheRebuiltIndex(t *testing.T) {
	home := upgradeHome(t, 3)
	open, err := OpenSearch()
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	writeSession(t, home, 1, "a new request")
	rows := upgrade(t)
	if found, err := Search(open, "new request", rows); err != nil || len(found) != 1 {
		t.Fatalf("a search opened before the upgrade found %d sessions that say it, want 1 (%v)", len(found), err)
	}
}
