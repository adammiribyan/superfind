package session

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"modernc.org/sqlite"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/mask"
)

const cacheVersion = 16

const (
	titleRunes  = 200
	promptRunes = 400
)

const (
	maskWindow = 4 << 10
	maskMargin = 1 << 10
)

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if cut, over := cutAt(s, n); over {
		return s[:cut] + "…"
	}
	return s
}

func cutAt(s string, n int) (int, bool) {
	cut, clusters := 0, 0
	for i := 0; i < len(s); clusters++ {
		if clusters == n-1 {
			cut = i
		}
		if clusters == n {
			return cut, true
		}
		cluster, _ := ansi.FirstGraphemeCluster(s[i:], ansi.GraphemeWidth)
		i += len(cluster)
	}
	return len(s), false
}

func clipMasked(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	for window := maskWindow; window < len(s); window *= 4 {
		end := window
		for end < len(s) && !utf8.RuneStart(s[end]) {
			end++
		}
		head := mask.Secrets(s[:end])
		if cut, over := cutAt(head, n); over && cut <= len(head)-maskMargin {
			return head[:cut] + "…"
		}
	}
	return clip(mask.Secrets(s), n)
}

func parse(f found) (parsed, bool, error) {
	t, trail, err := agent.ReadSession(f.Agent, f.path)
	if err != nil {
		return parsed{}, false, err
	}
	seen := map[agent.PR]bool{}
	trail.PRs = slices.DeleteFunc(trail.PRs, func(pr agent.PR) bool {
		repeated := seen[pr]
		seen[pr] = true
		return repeated
	})
	p := parsed{entry: cacheEntry{FileID: f.id, Name: f.name, Parent: t.Parent, Commits: len(trail.Commits)}, trail: trail}
	firstPrompt, latestPrompt := "", ""
	if len(t.Prompts) > 0 {
		firstPrompt, latestPrompt = t.Prompts[0], t.Prompts[len(t.Prompts)-1]
	}
	if t.Hidden || t.Cwd == "" || (firstPrompt == "" && t.LastPrompt == "") {
		return p, t.Incomplete, nil
	}
	p.body = agent.Searchable(strings.Join(t.Messages, "\n"))
	p.entry.Session = &Session{
		ID: f.Agent.IDOf(f.path), Cwd: t.Cwd,
		Title:      clipMasked(cmp.Or(f.name, t.CustomTitle, t.AITitle, firstPrompt, t.LastPrompt), titleRunes),
		LastPrompt: clipMasked(cmp.Or(t.LastPrompt, latestPrompt), promptRunes), Branch: t.Branch, Prompts: max(len(t.Prompts), 1),
		Words: len(strings.Fields(p.body)), ToolCalls: t.ToolCalls, Mtime: f.mtime, ByAgent: t.ByAgent, Ending: t.Last,
	}
	return p, t.Incomplete, nil
}

type cacheEntry struct {
	agent.FileID
	Name    string   `json:"name,omitempty"`
	Session *Session `json:"session"`
	Parent  string   `json:"parent,omitempty"`
	Commits int      `json:"commits,omitempty"`
}

func (e cacheEntry) row(p agent.Place, path string) Row {
	return Row{Session: *e.Session, Agent: p.Agent, Home: p.Home, Path: path, Commits: e.Commits}
}

func withThreads(rows []Row, entries map[string]cacheEntry, places []agent.Place) []Row {
	at := map[string]int{}
	for i, r := range rows {
		at[r.Agent.ID+":"+r.Session.ID] = i
	}
	parentOf, pathOf := map[string]string{}, map[string]string{}
	for path, e := range entries {
		if e.Parent == "" || e.Session != nil {
			continue
		}
		if p, ok := agent.Locate(path, places); ok {
			id := p.Agent.ID + ":" + p.Agent.IDOf(path)
			parentOf[id], pathOf[id] = p.Agent.ID+":"+e.Parent, path
		}
	}
	for _, id := range slices.Sorted(maps.Keys(pathOf)) {
		root := parentOf[id]
		for hops := 0; hops < 8 && parentOf[root] != ""; hops++ {
			if _, ok := at[root]; ok {
				break
			}
			root = parentOf[root]
		}
		i, ok := at[root]
		if !ok {
			continue
		}
		thread := entries[pathOf[id]]
		r := &rows[i]
		r.Threads = append(r.Threads, pathOf[id])
		r.Commits += thread.Commits
	}
	return rows
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
	os.Chmod(CacheDir(), 0o700)
	return nil
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

func unwritable(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS)
}

type ReadOnly struct {
	Changed int
	Since   time.Time
}

func (r ReadOnly) Error() string {
	noun := "sessions"
	if r.Changed == 1 {
		noun = "session"
	}
	return fmt.Sprintf("results may miss what changed in %d %s since %s, because the index in %s can't be updated from here",
		r.Changed, noun, r.Since.Local().Format("Jan 2 15:04"), CacheDir())
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
	entries := loadCache()
	for path, e := range entries {
		if p, ok := agent.Locate(path, in); ok && e.Session != nil {
			rows = append(rows, e.row(p, path))
		}
	}
	return withThreads(rows, entries, in)
}

var (
	tables = []string{"docs", "trails", "files", "built"}
	schema = []string{
		"CREATE VIRTUAL TABLE IF NOT EXISTS docs USING fts5(path UNINDEXED, title, body, tokenize='porter unicode61')",
		"CREATE TABLE IF NOT EXISTS trails (path TEXT PRIMARY KEY, trail TEXT NOT NULL)",
		"CREATE TABLE IF NOT EXISTS files (session TEXT NOT NULL, path TEXT NOT NULL, name TEXT NOT NULL, how TEXT NOT NULL, " +
			"kind TEXT NOT NULL, failed INTEGER NOT NULL, at REAL NOT NULL)",
		"CREATE INDEX IF NOT EXISTS files_by_name ON files (name)",
		"CREATE INDEX IF NOT EXISTS files_by_path ON files (path)",
		"CREATE INDEX IF NOT EXISTS files_by_session ON files (session)",
		"CREATE TABLE IF NOT EXISTS built (version INTEGER NOT NULL)",
	}
)

const (
	Edited  = "edited"
	Read    = "read"
	ByATool = "tool"
)

func OpenSearch() (*sql.DB, error) {
	if err := makeCacheDir(); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(CachePath("search.db"))
	if err != nil {
		return nil, err
	}
	settings := "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	readOnly := unwritable(err)
	switch {
	case readOnly:
		if _, err = os.Stat(path); err != nil {
			return nil, err
		}
		settings = "mode=ro&immutable=1"
		if _, err := os.Stat(path + "-wal"); err == nil {
			settings = "mode=ro&_pragma=busy_timeout(5000)"
		}
	case err != nil:
		return nil, err
	default:
		f.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Chmod(path+suffix, 0o600)
		}
	}
	location := url.URL{Scheme: "file", Path: path, RawQuery: settings}
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if readOnly {
		return db, nil
	}
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func Trails(names []string) (map[string]agent.Trail, error) {
	db, err := OpenSearch()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	trails := map[string]agent.Trail{}
	pairs, err := QueryPairs(db, "SELECT path, trail FROM trails")
	if err != nil {
		return nil, err
	}
	for _, pair := range pairs {
		var t agent.Trail
		if err := json.Unmarshal([]byte(pair[1]), &t); err != nil {
			return nil, err
		}
		trails[pair[0]] = t
	}
	named, err := json.Marshal(names)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query("SELECT session, path, at FROM files WHERE how = ? AND NOT failed AND name IN (SELECT value FROM json_each(?)) ORDER BY rowid",
		Edited, string(named))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var session string
		var e agent.Edit
		if err := rows.Scan(&session, &e.Path, &e.At); err != nil {
			return nil, err
		}
		t := trails[session]
		t.Edits = append(t.Edits, e)
		trails[session] = t
	}
	return trails, rows.Err()
}

func searchHolds(cached map[string]cacheEntry, readOnly bool) bool {
	want := 0
	for _, e := range cached {
		if e.Session != nil {
			want++
		}
	}
	held, built, err := searchCount()
	if err == nil {
		return held == want && built
	}
	if !readOnly {
		discardSearch()
	}
	return false
}

func discardSearch() {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(CachePath("search.db" + suffix))
	}
}

var ErrDamaged = errors.New("the search index is damaged and needs a rebuild")

func damaged(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && (e.Code()&0xff == 11 || e.Code()&0xff == 26)
}

var checked sync.Map

func intact(db *sql.DB) bool {
	if _, done := checked.LoadOrStore(db, true); done {
		return true
	}
	var docs int
	if err := db.QueryRow("SELECT count(*) FROM docs").Scan(&docs); err != nil || docs == 0 {
		return err == nil
	}
	db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS temp.terms USING fts5vocab(main, docs, 'row')")
	var term string
	return db.QueryRow("SELECT term FROM temp.terms LIMIT 1").Scan(&term) == nil
}

func searchCount() (int, bool, error) {
	db, err := OpenSearch()
	if err != nil {
		return 0, false, err
	}
	defer db.Close()
	var held, built int
	if err := db.QueryRow("SELECT count(*) FROM docs").Scan(&held); err != nil {
		return 0, false, err
	}
	err = db.QueryRow("SELECT count(*) FROM built").Scan(&built)
	return held, built > 0, err
}

type found struct {
	agent.Place
	path  string
	id    agent.FileID
	mtime float64
	name  string
}

type parsed struct {
	entry cacheEntry
	body  string
	trail agent.Trail
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

func Index(announce func(stale int)) ([]Row, []error, error) { return index(announce, false) }

func Rebuild(announce func(stale int)) ([]Row, []error, error) { return index(announce, true) }

func index(announce func(stale int), rebuild bool) ([]Row, []error, error) {
	unlock, err := lockFile("index.lock")
	readOnly := unwritable(err)
	if err != nil && !readOnly {
		return nil, nil, fmt.Errorf("locking the index: %w", err)
	}
	if !readOnly {
		defer unlock()
	}
	if rebuild && readOnly {
		return nil, nil, fmt.Errorf("%w, and it can't be rebuilt from here. Run superfind index where %s can be written",
			ErrDamaged, CacheDir())
	}
	if rebuild {
		discardSearch()
	}
	old := loadCache()
	if !searchHolds(old, readOnly) {
		if readOnly {
			return nil, nil, fmt.Errorf("there's no usable index in %s, and it can't be written from here. "+
				"Run superfind index once where it can, or set XDG_CACHE_HOME to a folder that can be written", CacheDir())
		}
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
	partial := map[string]parsed{}
	var mu sync.Mutex
	work := make(chan found)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range work {
				p, incomplete, err := parse(f)
				mu.Lock()
				if err != nil {
					problems = append(problems, err)
					mu.Unlock()
					continue
				}
				if incomplete {
					partial[f.path] = p
				} else {
					results[f.path] = p
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
			entry, known = p.entry, true
		}
		if known {
			fresh[f.path] = entry
		}
		unfinished, listed := partial[f.path]
		shown := unfinished.entry
		if !listed || (known && entry.Session == nil) {
			shown = entry
		}
		if shown.Session != nil {
			rows = append(rows, shown.row(f.Place, f.path))
		}
	}
	for path, entry := range old {
		_, seen := fresh[path]
		under := slices.ContainsFunc(blocked, func(dir string) bool { return strings.HasPrefix(path, dir) })
		if p, ok := agent.Locate(path, places); !seen && under && ok {
			fresh[path] = entry
			if entry.Session != nil {
				rows = append(rows, entry.row(p, path))
			}
		}
	}
	rows = withThreads(rows, fresh, places)
	var gone []string
	for p := range old {
		if _, ok := fresh[p]; !ok {
			gone = append(gone, p)
		}
	}
	if readOnly {
		since := time.Time{}
		if info, err := os.Stat(CachePath("index.json")); err == nil {
			since = info.ModTime()
		}
		return rows, problems, ReadOnly{len(stale) + len(gone), since}
	}
	discarded := len(old) == 0
	if len(results) > 0 || len(partial) > 0 || len(gone) > 0 || discarded {
		db, err := OpenSearch()
		if err == nil {
			err = updateSearch(db, results, partial, gone, discarded)
			db.Close()
		}
		if err != nil {
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

func updateSearch(db *sql.DB, results, partial map[string]parsed, gone []string, discarded bool) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if discarded {
		var statements []string
		for _, table := range tables {
			statements = append(statements, "DROP TABLE IF EXISTS "+table)
		}
		for _, statement := range append(append(statements, schema...), fmt.Sprintf("INSERT INTO built VALUES (%d)", cacheVersion)) {
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
		}
	} else {
		if _, err := tx.Exec("CREATE TEMP TABLE replaced (path TEXT PRIMARY KEY, searched INTEGER NOT NULL)"); err != nil {
			return err
		}
		for _, p := range slices.AppendSeq(slices.Clone(gone), maps.Keys(results)) {
			if _, err := tx.Exec("INSERT OR IGNORE INTO replaced VALUES (?, 1)", p); err != nil {
				return err
			}
		}
		for p := range partial {
			if _, err := tx.Exec("INSERT OR IGNORE INTO replaced VALUES (?, 0)", p); err != nil {
				return err
			}
		}
		for _, statement := range []string{"DELETE FROM docs WHERE path IN (SELECT path FROM replaced WHERE searched)",
			"DELETE FROM trails WHERE path IN (SELECT path FROM replaced)",
			"DELETE FROM files WHERE session IN (SELECT path FROM replaced)", "DROP TABLE replaced"} {
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
		}
	}
	file, err := tx.Prepare("INSERT INTO files (session, path, name, how, kind, failed, at) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer file.Close()
	for p, r := range results {
		if s := r.entry.Session; s != nil {
			if _, err := tx.Exec("INSERT INTO docs (path, title, body) VALUES (?, ?, ?)", p,
				agent.Searchable(s.Title+" "+s.Project()+" "+s.Branch), r.body); err != nil {
				return err
			}
		}
	}
	for _, batch := range []map[string]parsed{results, partial} {
		for p, r := range batch {
			for how, files := range map[string][]agent.Edit{Edited: r.trail.Edits, Read: r.trail.Reads} {
				for _, f := range files {
					if _, err := file.Exec(p, f.Path, filepath.Base(f.Path), how, ByATool, f.Failed, f.At); err != nil {
						return err
					}
				}
			}
			if len(r.trail.Commits)+len(r.trail.PRs) > 0 {
				trail, err := json.Marshal(agent.Trail{Commits: r.trail.Commits, PRs: r.trail.PRs})
				if err != nil {
					return err
				}
				if _, err := tx.Exec("INSERT INTO trails (path, trail) VALUES (?, ?)", p, trail); err != nil {
					return err
				}
			}
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

type scored struct {
	path  string
	tier  int
	score float64
}

func matches(db *sql.DB, query string) ([]scored, bool, error) {
	match := joined(phrasesOf(query), " ")
	if match == "" {
		return nil, false, nil
	}
	seen := map[string]bool{}
	var out []scored
	for tier, scoped := range []string{"title : (" + match + ")", match} {
		rows, err := db.Query("SELECT path, bm25(docs) FROM docs WHERE docs MATCH ? ORDER BY rank, path", scoped)
		if err != nil {
			return nil, true, err
		}
		for rows.Next() {
			var hit scored
			if err := rows.Scan(&hit.path, &hit.score); err != nil {
				rows.Close()
				return nil, true, err
			}
			if !seen[hit.path] {
				seen[hit.path] = true
				hit.tier, hit.score = tier, -hit.score
				out = append(out, hit)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, true, err
		}
	}
	return out, true, nil
}

const bandRatio = 1.25

func banded(hits []scored, mtime map[string]float64) []string {
	hits = slices.DeleteFunc(slices.Clone(hits), func(h scored) bool {
		_, listed := mtime[h.path]
		return !listed
	})
	best := map[int]float64{}
	for _, h := range hits {
		best[h.tier] = max(best[h.tier], h.score)
	}
	type ranked struct {
		scored
		band int
	}
	order := make([]ranked, len(hits))
	for i, h := range hits {
		order[i] = ranked{h, math.MaxInt}
		if h.score > 0 {
			order[i].band = int(math.Floor(math.Log(best[h.tier]/h.score) / math.Log(bandRatio)))
		}
	}
	slices.SortFunc(order, func(a, b ranked) int {
		return cmp.Or(cmp.Compare(a.tier, b.tier), cmp.Compare(a.band, b.band), cmp.Compare(mtime[b.path], mtime[a.path]),
			strings.Compare(a.path, b.path))
	})
	paths := make([]string, len(order))
	for i, r := range order {
		paths[i] = r.path
	}
	return paths
}

func Narrow(query string, rows []Row) (string, []Row) {
	q := Parse(query)
	return q.Words, q.Rows(rows, false)
}

func NarrowEvery(query string, rows []Row) (string, []Row) {
	q := Parse(query)
	return q.Words, q.Rows(rows, true)
}

func Search(db *sql.DB, query string, rows []Row) ([]Row, error) {
	return search(db, query, rows, false)
}

func SearchEvery(db *sql.DB, query string, rows []Row) ([]Row, error) {
	return search(db, query, rows, true)
}

func search(db *sql.DB, query string, rows []Row, everyKind bool) ([]Row, error) {
	q := Parse(query)
	words, rows := q.Words, q.Rows(rows, everyKind)
	if db == nil {
		return RecentFirst(rows), nil
	}
	found, searched, err := matches(db, words)
	if damaged(err) || (err == nil && searched && len(found) == 0 && !intact(db)) {
		return nil, ErrDamaged
	}
	if err != nil {
		return nil, fmt.Errorf("the search index couldn't be read: %w", err)
	}
	if !searched {
		return RecentFirst(rows), nil
	}
	mtime := make(map[string]float64, len(rows))
	for _, r := range rows {
		mtime[r.Path] = r.Session.Mtime
	}
	return InOrder(rows, banded(found, mtime)), nil
}
