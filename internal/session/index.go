package session

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"

	"modernc.org/sqlite"

	"github.com/adammiribyan/superfind/internal/agent"
)

const cacheVersion = 13

const (
	titleRunes  = 200
	promptRunes = 400
)

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func parse(f found) (*Session, string, bool, error) {
	t, err := agent.ReadTranscript(f.Agent, f.path)
	if err != nil {
		return nil, "", false, err
	}
	firstPrompt, latestPrompt := "", ""
	if len(t.Prompts) > 0 {
		firstPrompt, latestPrompt = t.Prompts[0], t.Prompts[len(t.Prompts)-1]
	}
	if t.Hidden || t.Cwd == "" || (firstPrompt == "" && t.LastPrompt == "") {
		return nil, "", t.Incomplete, nil
	}
	body := agent.Printable(strings.Join(t.Messages, "\n"))
	return &Session{
		ID: f.Agent.IDOf(f.path), Cwd: t.Cwd,
		Title:      clip(cmp.Or(f.name, t.CustomTitle, t.AITitle, firstPrompt, t.LastPrompt), titleRunes),
		LastPrompt: clip(cmp.Or(t.LastPrompt, latestPrompt), promptRunes), Branch: t.Branch, Prompts: max(len(t.Prompts), 1),
		Words: len(strings.Fields(body)), ToolCalls: t.ToolCalls, Mtime: f.mtime,
	}, body, t.Incomplete, nil
}

type cacheEntry struct {
	agent.FileID
	Name    string   `json:"name,omitempty"`
	Session *Session `json:"session"`
}

type cacheFile struct {
	Version int                   `json:"version"`
	Files   map[string]cacheEntry `json:"files"`
}

func loadCache() map[string]cacheEntry {
	raw, err := os.ReadFile(CachePath("index.json"))
	if err != nil {
		return map[string]cacheEntry{}
	}
	var c cacheFile
	if json.Unmarshal(raw, &c) != nil || c.Version != cacheVersion || c.Files == nil {
		return map[string]cacheEntry{}
	}
	return c.Files
}

func makeCacheDir() error {
	if err := os.MkdirAll(CacheDir(), 0o700); err != nil {
		return err
	}
	return os.Chmod(CacheDir(), 0o700)
}

func writeAtomically(path string, data []byte) error {
	if err := makeCacheDir(); err != nil {
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
	if err := makeCacheDir(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(CachePath(name), os.O_CREATE|os.O_WRONLY, 0o600)
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
	return writeAtomically(CachePath("index.json"), data)
}

func CachedRows() []Row {
	in := agent.Places()
	var rows []Row
	for path, e := range loadCache() {
		if p, ok := agent.Locate(path, in); ok && e.Session != nil {
			rows = append(rows, Row{Session: *e.Session, Agent: p.Agent, Home: p.Home, Path: path})
		}
	}
	return rows
}

func OpenSearch() (*sql.DB, error) {
	if err := makeCacheDir(); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(CachePath("search.db"))
	if err != nil {
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Chmod(path+suffix, 0o600)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err := os.Chmod(path, 0o600); err != nil {
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

func searchHolds(cached map[string]cacheEntry) bool {
	want := 0
	for _, e := range cached {
		if e.Session != nil {
			want++
		}
	}
	held, err := searchCount()
	if err == nil {
		return held == want
	}
	discardSearch()
	return false
}

func discardSearch() {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(CachePath("search.db" + suffix))
	}
}

var ErrDamaged = errors.New("the search index was damaged, so it is being rebuilt")

func damaged(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && (e.Code()&0xff == 11 || e.Code()&0xff == 26)
}

var checked sync.Map

func intact(db *sql.DB) bool {
	if _, done := checked.LoadOrStore(db, true); done {
		return true
	}
	_, err := db.Exec("INSERT INTO docs(docs) VALUES('integrity-check')")
	return !damaged(err)
}

func searchCount() (int, error) {
	db, err := OpenSearch()
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var held int
	err = db.QueryRow("SELECT count(*) FROM docs").Scan(&held)
	return held, err
}

type found struct {
	agent.Place
	path  string
	id    agent.FileID
	mtime float64
	name  string
}

type parsed struct {
	session *Session
	body    string
}

func Unreadable(problems []error) string {
	if len(problems) == 0 {
		return ""
	}
	noun := "session files or folders"
	if len(problems) == 1 {
		noun = "session file or folder"
	}
	return fmt.Sprintf("%d %s couldn't be read, so their sessions may be missing or out of date: %v",
		len(problems), noun, problems[0])
}

func Index(announce func(stale int)) ([]Row, []error, error) {
	unlock, err := lockFile("index.lock")
	if err != nil {
		return nil, nil, fmt.Errorf("locking the index: %w", err)
	}
	defer unlock()
	old := loadCache()
	if !searchHolds(old) {
		old = map[string]cacheEntry{}
	}
	var all []found
	var problems []error
	var blocked []string
	places := agent.Places()
	for _, p := range places {
		var names map[string]string
		if p.Agent.Names != nil {
			names = p.Agent.Names(p.Home)
		}
		paths, unreadable := p.Agent.Sessions(p.Home)
		for _, err := range unreadable {
			problems = append(problems, err)
			var e *fs.PathError
			if errors.As(err, &e) {
				blocked = append(blocked, e.Path+"/")
			}
		}
		for _, path := range paths {
			if strings.HasPrefix(p.Agent.IDOf(path), "-") {
				continue
			}
			if id, mtime, err := p.Agent.Stat(path); err == nil {
				all = append(all, found{p, path, id, mtime, names[p.Agent.IDOf(path)]})
			}
		}
	}
	var stale []found
	for _, f := range all {
		e, known := old[f.path]
		if !known || e.FileID != f.id || e.Name != f.name {
			stale = append(stale, f)
		}
	}
	if announce != nil && len(stale) > indexingNoticeAt {
		announce(len(stale))
	}
	results := make(map[string]parsed, len(stale))
	partial := map[string]*Session{}
	var mu sync.Mutex
	work := make(chan found)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range work {
				s, body, incomplete, err := parse(f)
				mu.Lock()
				if err != nil {
					problems = append(problems, err)
					mu.Unlock()
					continue
				}
				if incomplete {
					partial[f.path] = s
				} else {
					results[f.path] = parsed{s, body}
				}
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
		if known {
			fresh[f.path] = entry
		}
		s, listed := partial[f.path]
		if !listed {
			s = entry.Session
		}
		if s != nil {
			rows = append(rows, Row{Session: *s, Agent: f.Agent, Home: f.Home, Path: f.path})
		}
	}
	for path, entry := range old {
		_, seen := fresh[path]
		under := slices.ContainsFunc(blocked, func(dir string) bool { return strings.HasPrefix(path, dir) })
		if p, ok := agent.Locate(path, places); !seen && under && ok {
			fresh[path] = entry
			if entry.Session != nil {
				rows = append(rows, Row{Session: *entry.Session, Agent: p.Agent, Home: p.Home, Path: path})
			}
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
			return rows, problems, fmt.Errorf("updating the search index: %w", err)
		}
	}
	if len(results) > 0 || len(gone) > 0 || len(fresh) != len(old) {
		if err := saveCache(fresh); err != nil {
			return rows, problems, fmt.Errorf("saving the session cache: %w", err)
		}
	}
	return rows, problems, nil
}

func updateSearch(results map[string]parsed, gone []string, discarded bool) error {
	db, err := OpenSearch()
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
				agent.Printable(s.Title+" "+s.Project()+" "+s.Branch), r.body); err != nil {
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

func MeaningfulWords(query string) []string {
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
	for _, w := range MeaningfulWords(query) {
		parts = append(parts, `"`+w+`"*`)
	}
	return strings.Join(parts, " ")
}

func QueryPairs(db *sql.DB, query string, args ...any) ([][2]string, error) {
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
		pairs, err := QueryPairs(db, "SELECT path, '' FROM docs WHERE docs MATCH ? ORDER BY rank, path", scoped)
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

func Snippet(db *sql.DB, path, query, before, after string) string {
	match := ftsQuery(query)
	if match == "" || db == nil {
		return ""
	}
	var text string
	err := db.QueryRow("SELECT snippet(docs, 2, ?, ?, '…', 10) FROM docs WHERE docs MATCH ? AND path = ?",
		before, after, match, path).Scan(&text)
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func Narrow(query string, rows []Row) (string, []Row) {
	var words, statuses []string
	var from []*agent.Agent
	for _, w := range strings.Fields(query) {
		switch {
		case strings.HasPrefix(w, "is:"):
			if label := statusLabelFor(strings.ToLower(w[3:])); label != "" {
				statuses = append(statuses, label)
			}
		case strings.HasPrefix(w, "agent:"):
			if a := agent.Named(strings.ToLower(w[6:])); a != nil {
				from = append(from, a)
			}
		default:
			words = append(words, w)
		}
	}
	if len(statuses) > 0 || len(from) > 0 {
		var kept []Row
		for _, r := range rows {
			if (len(statuses) == 0 || slices.Contains(statuses, r.Status)) && (len(from) == 0 || slices.Contains(from, r.Agent)) {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	return strings.Join(words, " "), rows
}

func Search(db *sql.DB, query string, rows []Row) ([]Row, error) {
	words, rows := Narrow(query, rows)
	if db == nil {
		return RecentFirst(rows), nil
	}
	found, searched, err := matches(db, words)
	if damaged(err) || (err == nil && searched && len(found) == 0 && !intact(db)) {
		discardSearch()
		return nil, ErrDamaged
	}
	if err != nil {
		return nil, fmt.Errorf("the search index couldn't be read: %w", err)
	}
	if !searched {
		return RecentFirst(rows), nil
	}
	return InOrder(rows, found), nil
}
