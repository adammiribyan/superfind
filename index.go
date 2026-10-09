package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"

	_ "modernc.org/sqlite"
)

func parse(f found) (*Session, string, error) {
	t, err := readTranscript(f.agent, f.path)
	if err != nil {
		return nil, "", err
	}
	firstPrompt, latestPrompt := "", ""
	if len(t.prompts) > 0 {
		firstPrompt, latestPrompt = t.prompts[0], t.prompts[len(t.prompts)-1]
	}
	if t.hidden || t.cwd == "" || (firstPrompt == "" && t.lastPrompt == "") {
		return nil, "", nil
	}
	body := strings.Join(t.messages, "\n")
	return &Session{
		ID: f.agent.idOf(f.path), Cwd: t.cwd,
		Title:      firstNonEmpty(f.name, t.customTitle, t.aiTitle, firstPrompt, t.lastPrompt),
		LastPrompt: firstNonEmpty(t.lastPrompt, latestPrompt), Branch: t.branch, Prompts: max(len(t.prompts), 1),
		Words: len(strings.Fields(body)), ToolCalls: t.toolCalls, Mtime: f.mtime,
	}, body, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

type cacheEntry struct {
	fileID
	Name    string   `json:"name,omitempty"`
	Session *Session `json:"session"`
}

type cacheFile struct {
	Version int                   `json:"version"`
	Files   map[string]cacheEntry `json:"files"`
}

func loadCache() map[string]cacheEntry {
	raw, err := os.ReadFile(cachePath("index.json"))
	if err != nil {
		return map[string]cacheEntry{}
	}
	var c cacheFile
	if json.Unmarshal(raw, &c) != nil || c.Version != cacheVersion || c.Files == nil {
		return map[string]cacheEntry{}
	}
	return c.Files
}

func writeAtomically(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func lockFile(name string) (func(), error) {
	if err := os.MkdirAll(cacheDir(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(cachePath(name), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

func saveCache(files map[string]cacheEntry) error {
	data, err := json.Marshal(cacheFile{cacheVersion, files})
	if err != nil {
		return err
	}
	return writeAtomically(cachePath("index.json"), data)
}

func cachedRows() []Row {
	in := places()
	var rows []Row
	for path, e := range loadCache() {
		if p, ok := locate(path, in); ok && e.Session != nil {
			rows = append(rows, Row{Session: *e.Session, Agent: p.agent, Home: p.home, Path: path})
		}
	}
	return rows
}

func openSearch() (*sql.DB, error) {
	if err := os.MkdirAll(cacheDir(), 0o755); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(cachePath("search.db"))
	if err != nil {
		return nil, err
	}
	location := url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"}
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS docs USING fts5(path UNINDEXED, title, body, tokenize='porter unicode61')`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type found struct {
	place
	path  string
	id    fileID
	mtime float64
	name  string
}

type parsed struct {
	session *Session
	body    string
}

func index(announce func(stale int)) ([]Row, error) {
	unlock, err := lockFile("index.lock")
	if err != nil {
		return nil, fmt.Errorf("locking the index: %w", err)
	}
	defer unlock()
	old := loadCache()
	if _, err := os.Stat(cachePath("search.db")); err != nil {
		old = map[string]cacheEntry{}
	}
	var all []found
	for _, p := range places() {
		names := p.agent.threadNames(p.home)
		for _, path := range p.agent.sessions(p.home) {
			if id, mtime, err := p.agent.stat(path); err == nil {
				all = append(all, found{p, path, id, mtime, names[p.agent.idOf(path)]})
			}
		}
	}
	var stale []found
	for _, f := range all {
		e, known := old[f.path]
		if !known || e.fileID != f.id || e.Name != f.name {
			stale = append(stale, f)
		}
	}
	if announce != nil && len(stale) > indexingNoticeAt {
		announce(len(stale))
	}
	results := make(map[string]parsed, len(stale))
	var mu sync.Mutex
	work := make(chan found)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range work {
				s, body, err := parse(f)
				if err != nil {
					continue
				}
				mu.Lock()
				results[f.path] = parsed{s, body}
				mu.Unlock()
			}
		}()
	}
	for _, f := range stale {
		work <- f
	}
	close(work)
	wg.Wait()

	fresh := make(map[string]cacheEntry, len(all))
	var rows []Row
	for _, f := range all {
		entry, known := old[f.path]
		if p, ok := results[f.path]; ok {
			entry, known = cacheEntry{f.id, f.name, p.session}, true
		}
		if !known {
			continue
		}
		fresh[f.path] = entry
		if s := entry.Session; s != nil {
			rows = append(rows, Row{Session: *s, Agent: f.agent, Home: f.home, Path: f.path})
		}
	}
	var gone []string
	for p := range old {
		if _, ok := fresh[p]; !ok {
			gone = append(gone, p)
		}
	}
	discarded := len(old) == 0
	if len(results) > 0 || len(gone) > 0 || discarded {
		if err := updateSearch(results, gone, discarded); err != nil {
			return rows, fmt.Errorf("updating the search index: %w", err)
		}
	}
	if len(results) > 0 || len(gone) > 0 || len(fresh) != len(old) {
		if err := saveCache(fresh); err != nil {
			return rows, fmt.Errorf("saving the session cache: %w", err)
		}
	}
	return rows, nil
}

func updateSearch(results map[string]parsed, gone []string, discarded bool) error {
	db, err := openSearch()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if discarded {
		if _, err := tx.Exec("DELETE FROM docs"); err != nil {
			return err
		}
	}
	for p, r := range results {
		if _, err := tx.Exec("DELETE FROM docs WHERE path = ?", p); err != nil {
			return err
		}
		if r.session != nil {
			s := r.session
			if _, err := tx.Exec("INSERT INTO docs (path, title, body) VALUES (?, ?, ?)", p,
				s.Title+" "+s.Project()+" "+s.Branch, r.body); err != nil {
				return err
			}
		}
	}
	for _, p := range gone {
		if _, err := tx.Exec("DELETE FROM docs WHERE path = ?", p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a about all an and any are as at be been but by can could did do does for from had has have
how i if in into is it its me my of on or our so that the their them then there these they this those to
up us was we were what when where which who why will with would you your`) {
		m[w] = true
	}
	return m
}()

var wordPattern = regexp.MustCompile(`[\p{L}\p{N}_]+`)

func meaningfulWords(query string) []string {
	var out []string
	for _, w := range wordPattern.FindAllString(strings.ToLower(query), -1) {
		if !stopwords[w] && len([]rune(w)) > 1 {
			out = append(out, w)
		}
	}
	return out
}

func ftsQuery(query string) string {
	var parts []string
	for _, w := range meaningfulWords(query) {
		parts = append(parts, `"`+w+`"*`)
	}
	return strings.Join(parts, " ")
}

func queryPairs(db *sql.DB, query string, args ...any) ([][2]string, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var pair [2]string
		if err := rows.Scan(&pair[0], &pair[1]); err != nil {
			return nil, err
		}
		out = append(out, pair)
	}
	return out, rows.Err()
}

func matches(db *sql.DB, query string) ([]string, bool, error) {
	match := ftsQuery(query)
	if match == "" {
		return nil, false, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, scoped := range []string{"title : (" + match + ")", match} {
		pairs, err := queryPairs(db, "SELECT path, '' FROM docs WHERE docs MATCH ? ORDER BY rank, path", scoped)
		if err != nil {
			return nil, true, err
		}
		for _, pair := range pairs {
			if !seen[pair[0]] {
				seen[pair[0]] = true
				out = append(out, pair[0])
			}
		}
	}
	return out, true, nil
}

func snippet(db *sql.DB, path, query string) string {
	match := ftsQuery(query)
	if match == "" || db == nil {
		return ""
	}
	var text string
	err := db.QueryRow("SELECT snippet(docs, 2, ?, ?, '…', 10) FROM docs WHERE docs MATCH ? AND path = ?",
		ansiBold+ansiAccent, ansiReset, match, path).Scan(&text)
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}
