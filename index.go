package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"

	_ "modernc.org/sqlite"
)

var slashCommand = regexp.MustCompile(`(?s)<command-name>(/?[^<]*)</command-name>.*?(?:<command-args>(.*?)</command-args>|$)`)

var machinePrefixes = []string{"<", "Caveat:", "[Request interrupted", "Another Claude session sent a message", "[SYSTEM NOTIFICATION"}

func promptText(text string) string {
	if m := slashCommand.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1] + " " + m[2])
	}
	for _, p := range machinePrefixes {
		if strings.HasPrefix(text, p) {
			return ""
		}
	}
	return text
}

type entry map[string]any

func (e entry) str(key string) string {
	s, _ := e[key].(string)
	return s
}

func messageText(e entry) string {
	message, _ := e["message"].(map[string]any)
	switch content := message["content"].(type) {
	case string:
		return content
	case []any:
		var parts []string
		for _, block := range content {
			if b, ok := block.(map[string]any); ok && b["type"] == "text" {
				t, _ := b["text"].(string)
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func decode(raw []byte) entry {
	var e entry
	if json.Unmarshal(raw, &e) != nil {
		return entry{}
	}
	return e
}

func eachLine(path string, fn func(raw []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			fn(line)
		}
		if err != nil {
			return nil
		}
	}
}

var (
	markUser       = []byte(`"type":"user"`)
	markAssistant  = []byte(`"type":"assistant"`)
	markToolResult = []byte(`"tool_use_id"`)
	markToolUse    = []byte(`"type":"tool_use"`)
	markText       = []byte(`"type":"text"`)
	markCwd        = []byte(`"cwd":"`)
	markCustom     = []byte(`"type":"custom-title"`)
	markAITitle    = []byte(`"type":"ai-title"`)
	markLastPrompt = []byte(`"type":"last-prompt"`)
	markRelocated  = []byte(`"type":"relocated"`)
	markInterrupt  = []byte("[Request interrupted")
)

func parse(path string, mtime float64) (*Session, string) {
	var cwd, relocated, custom, ai, lastPrompt, firstPrompt, branch string
	prompts, toolCalls := 0, 0
	var body []string
	err := eachLine(path, func(raw []byte) {
		switch {
		case bytes.Contains(raw, markUser) && !bytes.Contains(raw, markToolResult):
			e := decode(raw)
			if cwd == "" {
				cwd = e.str("cwd")
			}
			if b := e.str("gitBranch"); b != "" {
				branch = b
			}
			text := promptText(strings.TrimSpace(messageText(e)))
			if meta, _ := e["isMeta"].(bool); !meta && text != "" {
				prompts++
				if firstPrompt == "" {
					firstPrompt = text
				}
				body = append(body, text)
			}
		case bytes.Contains(raw, markAssistant):
			toolCalls += bytes.Count(raw, markToolUse)
			if bytes.Contains(raw, markText) {
				body = append(body, messageText(decode(raw)))
			}
		case cwd == "" && bytes.Contains(raw, markCwd):
			cwd = decode(raw).str("cwd")
		case bytes.Contains(raw, markCustom):
			if t := decode(raw).str("customTitle"); t != "" {
				custom = t
			}
		case bytes.Contains(raw, markAITitle):
			if t := decode(raw).str("aiTitle"); t != "" {
				ai = t
			}
		case bytes.Contains(raw, markLastPrompt):
			if t := decode(raw).str("lastPrompt"); t != "" {
				lastPrompt = t
			}
		case bytes.Contains(raw, markRelocated):
			if t := decode(raw).str("relocatedCwd"); t != "" {
				relocated = t
			}
		}
	})
	if err != nil {
		return nil, ""
	}
	if relocated != "" {
		cwd = relocated
	}
	if cwd == "" || (firstPrompt == "" && lastPrompt == "") {
		return nil, ""
	}
	title := firstNonEmpty(custom, ai, firstPrompt, lastPrompt)
	text := strings.Join(body, "\n")
	return &Session{
		ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Cwd: cwd, Title: title,
		LastPrompt: firstNonEmpty(lastPrompt, firstPrompt), Branch: branch, Prompts: max(prompts, 1),
		Words: len(strings.Fields(text)), ToolCalls: toolCalls, Mtime: mtime,
	}, text
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
	Mtime   float64  `json:"mtime"`
	Size    int64    `json:"size"`
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

func openSearch() (*sql.DB, error) {
	if err := os.MkdirAll(cacheDir(), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", cachePath("search.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS docs USING fts5(path UNINDEXED, title, body, tokenize='porter unicode61')`)
	return db, err
}

type found struct {
	configDir string
	path      string
	info      os.FileInfo
}

type parsed struct {
	session *Session
	body    string
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func index() ([]Row, error) {
	unlock, err := lockFile("index.lock")
	if err != nil {
		return nil, fmt.Errorf("locking the index: %w", err)
	}
	defer unlock()
	old := loadCache()
	dirs := configDirs()
	var all []found
	for _, d := range dirs {
		matches, _ := filepath.Glob(filepath.Join(d, "projects", "*", "*.jsonl"))
		for _, p := range matches {
			if info, err := os.Stat(p); err == nil {
				all = append(all, found{d, p, info})
			}
		}
	}
	var stale []found
	for _, f := range all {
		e, ok := old[f.path]
		if !ok || !sameFile(e.Mtime, e.Size, f.info) {
			stale = append(stale, f)
		}
	}
	notice := len(stale) > indexingNoticeAt && isTerminal(os.Stderr)
	if notice {
		fmt.Fprintf(os.Stderr, "  Indexing %d sessions…", len(stale))
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
				s, body := parse(f.path, stamp(f.info))
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
	if notice {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}

	fresh := make(map[string]cacheEntry, len(all))
	var rows []Row
	for _, f := range all {
		var s *Session
		if p, ok := results[f.path]; ok {
			s = p.session
		} else {
			s = old[f.path].Session
		}
		fresh[f.path] = cacheEntry{stamp(f.info), f.info.Size(), s}
		if s != nil {
			rows = append(rows, Row{Session: *s, ConfigDir: f.configDir, Path: f.path})
		}
	}
	var gone []string
	for p := range old {
		if _, ok := fresh[p]; !ok {
			gone = append(gone, p)
		}
	}
	_, statErr := os.Stat(cachePath("search.db"))
	if len(results) > 0 || len(gone) > 0 || statErr != nil {
		if err := updateSearch(results, gone); err != nil {
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

func updateSearch(results map[string]parsed, gone []string) error {
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

func cachedRows() []Row {
	dirs := configDirs()
	var rows []Row
	for path, e := range loadCache() {
		if e.Session != nil {
			rows = append(rows, Row{Session: *e.Session, ConfigDir: configDirOf(path, dirs), Path: path})
		}
	}
	return rows
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

func matches(db *sql.DB, query string) ([]string, bool) {
	match := ftsQuery(query)
	if match == "" {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	for _, scoped := range []string{"title : (" + match + ")", match} {
		rows, err := db.Query("SELECT path FROM docs WHERE docs MATCH ? ORDER BY rank, path LIMIT 200", scoped)
		if err != nil {
			continue
		}
		for rows.Next() {
			var p string
			if rows.Scan(&p) == nil && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
		rows.Close()
	}
	return out, true
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
