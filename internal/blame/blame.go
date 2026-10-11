package blame

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/session"
)

const (
	shortestSubject = 12
	shortestSHA     = 7
	editWindow      = 3 * 86400
	fileCommits     = 30
)

type subject struct {
	typed, plain string
}

type commit struct {
	SHA      string
	Subject  string
	Subjects []subject
	PR       int
	At       float64
	Files    map[string]bool
}

type Target struct {
	Roots   []string
	Slugs   []string
	File    string
	Commits []commit
	Note    string
	folders *sync.Map
	threads map[string][]string
}

type Found struct {
	Row      session.Row
	Evidence string
	Guess    bool
	tier     int
	gap      float64
	count    int
	latest   float64
}

var (
	pullNumber  = regexp.MustCompile(`\(#(\d+)\)$|^Merge pull request #(\d+) `)
	githubSlug  = regexp.MustCompile(`github\.com[:/]([\w.-]+/[\w.-]+?)(?:\.git)?/?\s`)
	nonWord     = regexp.MustCompile(`[^a-z0-9]+`)
	lineSuffix  = regexp.MustCompile(`^(.+):([1-9][0-9]*)$`)
	messageFlag = regexp.MustCompile(`(?:^|\s)(?:-[a-zA-Z]*m|--message)(?:=|\s*)`)
	word        = regexp.MustCompile(`[A-Za-z0-9_]+`)
)

func plain(s string) string { return nonWord.ReplaceAllString(strings.ToLower(s), "") }

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, problem bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &problem
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(problem.String()); msg != "" {
			return "", errors.New(strings.TrimPrefix(msg, "fatal: "))
		}
		return "", err
	}
	return out.String(), nil
}

func Resolve(arg string) (Target, error) {
	if isFile(arg) {
		return fileTarget(arg, "")
	}
	if m := lineSuffix.FindStringSubmatch(arg); m != nil && isFile(m[1]) {
		return lineTarget(m[1], m[2])
	}
	if info, err := os.Stat(arg); err == nil && info.IsDir() {
		return Target{}, fmt.Errorf("%s is a folder; give a file, a file:line, or a commit", arg)
	}
	t, err := repoAt(".")
	if err != nil {
		return t, errors.New("not inside a git repository; run it in one to blame a commit")
	}
	sha, err := git(".", "rev-parse", "--verify", "--quiet", "--end-of-options", arg+"^{commit}")
	if err != nil || strings.TrimSpace(sha) == "" {
		return t, fmt.Errorf("%s is not a file or a commit in this repository", arg)
	}
	c, err := commitAt(".", strings.TrimSpace(sha))
	if err != nil {
		return t, err
	}
	t.Commits = []commit{c}
	return t, nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func repoAt(dir string) (Target, error) {
	out, err := git(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return Target{}, fmt.Errorf("%s is not in a git repository", dir)
	}
	t := Target{folders: &sync.Map{}}
	for _, l := range strings.Split(out, "\n") {
		if root, ok := strings.CutPrefix(l, "worktree "); ok {
			t.Roots = append(t.Roots, root)
			if real := agent.Resolve(root); real != root {
				t.Roots = append(t.Roots, real)
			}
		}
	}
	sort.Slice(t.Roots, func(i, j int) bool { return len(t.Roots[i]) > len(t.Roots[j]) })
	remotes, _ := git(dir, "remote", "-v")
	for _, m := range githubSlug.FindAllStringSubmatch(remotes, -1) {
		if slug := strings.ToLower(m[1]); !slices.Contains(t.Slugs, slug) {
			t.Slugs = append(t.Slugs, slug)
		}
	}
	return t, nil
}

func commitAt(dir, sha string) (commit, error) {
	out, err := git(dir, "show", "-s", "--format=%H%x00%s%x00%b%x00%at", sha)
	if err != nil {
		return commit{}, err
	}
	parts := strings.SplitN(strings.TrimRight(out, "\n"), "\x00", 4)
	if len(parts) < 4 {
		return commit{}, errors.New("git show printed something unexpected for " + sha)
	}
	c := parsed(parts[0], parts[1], parts[2], parts[3])
	files, err := git(dir, "diff-tree", "-z", "--root", "--no-commit-id", "--name-only", "-r", "-m", "--first-parent", c.SHA)
	if err != nil {
		return commit{}, err
	}
	c.Files = map[string]bool{}
	for _, f := range strings.Split(files, "\x00") {
		if f != "" {
			c.Files[f] = true
		}
	}
	return c, nil
}

func parsed(sha, subjectLine, body, at string) commit {
	c := commit{SHA: sha, Subject: subjectLine}
	c.At, _ = strconv.ParseFloat(strings.TrimSpace(at), 64)
	if m := pullNumber.FindStringSubmatch(subjectLine); m != nil {
		c.PR, _ = strconv.Atoi(m[1] + m[2])
	}
	lines := []string{subjectLine}
	if c.PR > 0 {
		lines = append(lines, bullets(body)...)
	}
	for _, s := range lines {
		if p := plain(s); len(p) >= shortestSubject {
			c.Subjects = append(c.Subjects, subject{s, p})
		}
	}
	return c
}

func bullets(body string) []string {
	var found []string
	for _, l := range strings.Split(body, "\n") {
		if s, ok := strings.CutPrefix(l, "* "); ok {
			found = append(found, s)
		}
	}
	return found
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	lines, last, buf := 0, byte('\n'), make([]byte, 1<<16)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			lines, last = lines+bytes.Count(buf[:n], []byte("\n")), buf[n-1]
		}
		if errors.Is(err, io.EOF) {
			if last != '\n' {
				lines++
			}
			return lines, nil
		}
		if err != nil {
			return 0, err
		}
	}
}

func lineTarget(path, line string) (Target, error) {
	lines, err := countLines(path)
	if err != nil {
		return Target{}, err
	}
	if n, _ := strconv.Atoi(line); n > lines {
		return Target{}, fmt.Errorf("%s has %s, not %s", path, plural(lines, "1 line", "%d lines"), line)
	}
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	out, err := git(dir, "blame", "--porcelain", "-L", line+","+line, "--", base)
	if err != nil {
		return Target{}, err
	}
	sha, _, _ := strings.Cut(out, " ")
	if strings.Trim(sha, "0") == "" {
		return fileTarget(path, "line "+line+" isn't committed yet, so this lists the sessions that edited the file")
	}
	t, err := repoAt(dir)
	if err != nil {
		return t, err
	}
	c, err := commitAt(dir, sha)
	if err != nil {
		return t, err
	}
	t.Commits = []commit{c}
	return t, nil
}

func fileTarget(path, note string) (Target, error) {
	dir := filepath.Dir(path)
	t, err := repoAt(dir)
	if err != nil {
		return t, fmt.Errorf("%s is not in a git repository", path)
	}
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return t, err
	}
	top = strings.TrimSpace(top)
	rel, err := filepath.Rel(agent.Resolve(top), filepath.Join(agent.Resolve(dir), filepath.Base(path)))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return t, fmt.Errorf("%s is outside its repository", path)
	}
	t.File, t.Note = filepath.ToSlash(rel), note
	out, err := git(top, "log", "-n", strconv.Itoa(fileCommits), "--format=%H%x00%s%x00%b%x00%at%x1e", "--", ":(literal)"+t.File)
	if _, unborn := git(top, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil && unborn == nil {
		return t, err
	}
	for _, entry := range strings.Split(out, "\x1e") {
		if parts := strings.SplitN(strings.TrimLeft(entry, "\n"), "\x00", 4); len(parts) == 4 {
			t.Commits = append(t.Commits, parsed(parts[0], parts[1], parts[2], parts[3]))
		}
	}
	return t, nil
}

func (t Target) relative(path string) (string, bool) {
	for _, candidate := range []string{path, filepath.Join(t.real(filepath.Dir(path)), filepath.Base(path))} {
		for _, root := range t.Roots {
			if candidate == root {
				return ".", true
			}
			if rel, ok := strings.CutPrefix(candidate, root+"/"); ok {
				return rel, true
			}
		}
	}
	return "", false
}

func (t Target) real(dir string) string {
	if known, ok := t.folders.Load(dir); ok {
		return known.(string)
	}
	real := agent.Resolve(dir)
	t.folders.Store(dir, real)
	return real
}

func (t Target) holds(r session.Row, command string) bool {
	if _, ok := t.relative(r.Session.Cwd); ok {
		return true
	}
	if _, err := os.Stat(r.Session.Cwd); err != nil {
		return true
	}
	return slices.ContainsFunc(t.Roots, func(root string) bool { return strings.Contains(command, root) })
}

func threadsOf(rows []session.Row) map[string][]string {
	indexed, rowOf := map[string]bool{}, map[string]string{}
	for _, r := range rows {
		indexed[r.Path] = true
		rowOf[r.Agent.ID+":"+r.Session.ID] = r.Path
	}
	parentOf, pathOf := map[string]string{}, map[string]string{}
	for _, p := range agent.Places() {
		paths, _ := p.Agent.Sessions(p.Home)
		for _, path := range paths {
			if parent := agent.ParentOf(p.Agent, path); parent != "" && !indexed[path] {
				id := p.Agent.ID + ":" + p.Agent.IDOf(path)
				parentOf[id], pathOf[id] = p.Agent.ID+":"+parent, path
			}
		}
	}
	threads := map[string][]string{}
	for id, path := range pathOf {
		root := parentOf[id]
		for hops := 0; rowOf[root] == "" && parentOf[root] != "" && hops < 8; hops++ {
			root = parentOf[root]
		}
		if row := rowOf[root]; row != "" {
			threads[row] = append(threads[row], path)
		}
	}
	return threads
}

func Blame(t Target, rows []session.Row) ([]Found, []error) {
	t.threads = threadsOf(rows)
	found, problems := t.scan(rows, t.needles(false))
	direct := slices.ContainsFunc(found, func(f *Found) bool { return f != nil && !f.Guess })
	if t.File == "" && !direct {
		var recent []session.Row
		for _, r := range rows {
			if t.lastChanged(r) >= t.Commits[0].At-editWindow {
				recent = append(recent, r)
			}
		}
		found, problems = t.scan(recent, t.needles(true))
	}
	var out []Found
	for _, f := range found {
		if f != nil && (t.File != "" || !direct || !f.Guess) {
			out = append(out, *f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].before(out[j], t.File != "") })
	return out, problems
}

func (t Target) lastChanged(r session.Row) float64 {
	latest := r.Session.Mtime
	for _, path := range t.threads[r.Path] {
		if info, err := os.Stat(path); err == nil {
			latest = max(latest, float64(info.ModTime().UnixNano())/1e9)
		}
	}
	return latest
}

func (t Target) scan(candidates []session.Row, needles [][]byte) ([]*Found, []error) {
	found, problems := make([]*Found, len(candidates)), make([]error, len(candidates))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(runtime.NumCPU(), max(len(candidates), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				trail, err := agent.ReadTrail(candidates[i].Agent, candidates[i].Path, needles, t.threads[candidates[i].Path]...)
				found[i], problems[i] = t.judge(candidates[i], trail), err
			}
		}()
	}
	for i := range candidates {
		next <- i
	}
	close(next)
	wg.Wait()
	return found, slices.DeleteFunc(problems, func(err error) bool { return err == nil })
}

func (t Target) needles(files bool) [][]byte {
	var found [][]byte
	add := func(s string) { found = append(found, []byte(s)) }
	if t.File != "" {
		add(filepath.Base(t.File))
	}
	for _, c := range t.Commits {
		add(c.SHA[:shortestSHA])
		for _, s := range c.Subjects {
			longest := ""
			for _, w := range word.FindAllString(s.typed, -1) {
				if len(w) > len(longest) {
					longest = w
				}
			}
			add(longest)
		}
		if c.PR > 0 {
			add(fmt.Sprintf("/pull/%d", c.PR))
			add(fmt.Sprintf(`"prNumber":%d`, c.PR))
		}
		if files && t.File == "" {
			for f := range c.Files {
				add(filepath.Base(f))
			}
		}
	}
	return found
}

const (
	madeExactly = iota
	made
	opened
	edited
)

func (a Found) before(b Found, byTime bool) bool {
	switch {
	case byTime && a.latest != b.latest:
		return a.latest > b.latest
	case a.tier != b.tier:
		return a.tier < b.tier
	case a.tier <= made && a.gap != b.gap:
		return a.gap < b.gap
	case a.count != b.count:
		return a.count > b.count
	}
	return a.latest > b.latest
}

func (t Target) judge(r session.Row, trail agent.Trail) *Found {
	messages := make([][]string, len(trail.Commits))
	for i, m := range trail.Commits {
		if t.holds(r, m.Command) {
			messages[i] = messageLines(m.Command, 0)
		}
	}
	var best *Found
	consider := func(f Found) {
		if best == nil || f.before(*best, t.File != "") {
			best = &f
		}
	}
	for _, c := range t.Commits {
		evidence := "made this commit"
		if t.File != "" {
			evidence = "made " + c.SHA[:shortestSHA] + ": " + c.Subject
		}
		if tier, gap, squashed, ok := c.madeBy(trail, messages); ok {
			if squashed && t.File == "" {
				evidence = "made a commit it squashed"
			}
			consider(Found{Row: r, Evidence: evidence, tier: tier, gap: gap, latest: c.At})
		}
		if c.PR > 0 && slices.ContainsFunc(trail.PRs, func(p agent.PR) bool {
			return p.Number == c.PR && slices.Contains(t.Slugs, strings.ToLower(p.Repo))
		}) {
			consider(Found{Row: r, Evidence: fmt.Sprintf("opened #%d", c.PR), tier: opened, latest: c.At})
		}
	}
	if best != nil && t.File == "" {
		return best
	}
	touched, times, latest := map[string]bool{}, 0, 0.0
	for _, e := range trail.Edits {
		rel, ok := t.relative(e.Path)
		if !ok || (t.File != "" && rel != t.File) || (t.File == "" && !t.Commits[0].Files[rel]) {
			continue
		}
		if t.File == "" && e.At > 0 && (e.At > t.Commits[0].At || e.At < t.Commits[0].At-editWindow) {
			continue
		}
		at := e.At
		if at == 0 {
			at = r.Session.Mtime
		}
		touched[rel], times, latest = true, times+1, max(latest, at)
	}
	switch {
	case len(touched) == 0:
		return best
	case t.File != "":
		consider(Found{Row: r, Evidence: plural(times, "edited it once", "edited it %d times"), tier: edited, count: times, latest: latest})
	default:
		consider(Found{Row: r, Evidence: fmt.Sprintf("edited %d of the commit's %d files", len(touched), len(t.Commits[0].Files)),
			Guess: true, tier: edited, count: len(touched), latest: latest})
	}
	return best
}

func messageLines(command string, depth int) []string {
	var lines []string
	add := func(text string) {
		split := strings.Split(strings.ReplaceAll(text, `\n`, "\n"), "\n")
		for i := range split {
			paragraph := ""
			for _, l := range split[i:] {
				if strings.TrimSpace(l) == "" {
					break
				}
				paragraph += l
			}
			lines = append(lines, plain(paragraph))
		}
		for _, l := range split {
			lines = append(lines, plain(l))
			if at := messageFlag.FindStringIndex(l); at != nil {
				rest, _, _ := strings.Cut(l[at[1]:], " && ")
				lines = append(lines, plain(rest))
			}
		}
	}
	add(command)
	for i := 0; i < len(command); i++ {
		q := command[i]
		if q != '"' && q != '\'' {
			continue
		}
		end := i + 1
		for end < len(command) && (command[end] != q || (q == '"' && command[end-1] == '\\')) {
			end++
		}
		inner := command[i+1 : min(end, len(command))]
		if depth < 2 && strings.Contains(inner, "git") {
			lines = append(lines, messageLines(inner, depth+1)...)
		} else {
			add(inner)
		}
		i = end
	}
	return lines
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf(many, n)
}

func (c commit) madeBy(trail agent.Trail, messages [][]string) (int, float64, bool, bool) {
	tier, gap, squashed, found := made, 0.0, false, false
	for i, m := range trail.Commits {
		exact := len(m.SHA) >= shortestSHA && strings.HasPrefix(c.SHA, m.SHA)
		match := slices.IndexFunc(c.Subjects, func(s subject) bool { return slices.Contains(messages[i], s.plain) })
		if !exact && match < 0 {
			continue
		}
		bullet := !exact && c.Subjects[match].typed != c.Subject
		g := 1e12
		if m.At > 0 {
			g = math.Abs(m.At - c.At)
		}
		t := made
		if exact {
			t = madeExactly
		}
		if !found || t < tier || (t == tier && g < gap) {
			tier, gap, squashed, found = t, g, bullet, true
		}
	}
	return tier, gap, squashed, found
}
