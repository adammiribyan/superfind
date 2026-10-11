package blame

import (
	"cmp"
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/session"
)

type Stage int

const (
	Committed Stage = iota
	Pushed
	Opened
	Merged
	Reverted
)

var stageNames = [...]string{"committed", "pushed", "opened", "merged", "reverted"}

func (s Stage) String() string { return stageNames[s] }

func (s Stage) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

const (
	Made        = "commit"
	PullRequest = "pull_request"
)

type Outcome struct {
	Kind       string `json:"kind"`
	Commit     string `json:"commit"`
	PR         int    `json:"pr"`
	Subject    string `json:"subject"`
	Repo       string `json:"repo"`
	Stage      Stage  `json:"stage"`
	Branch     string `json:"branch"`
	Landed     string `json:"landed"`
	LandedAt   string `json:"landed_at"`
	RevertedBy string `json:"reverted_by"`
	at         float64
}

const (
	matchBefore  = 5 * 60
	matchAfter   = 30 * 60
	logSlack     = 86400
	undatedSlack = 30 * 86400
)

var (
	folderChange = regexp.MustCompile(`(?:^|[\s;&|(])cd\s+` + folderArg)
	commitCall   = regexp.MustCompile(`\bgit((?:\s+-[cC]\s+` + folderArg + `)*)\s+commit\b`)
	gitFolder    = regexp.MustCompile(`(?:^|\s)-C\s+` + folderArg)
	pullSuffix   = regexp.MustCompile(`\s*\(#\d+\)$`)
	revertNote   = regexp.MustCompile(`This reverts commit ([0-9a-f]{7,40})`)
	hexSHA       = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

type entry struct {
	commit
	body    string
	landed  float64
	parents []string
}

type repo struct {
	top, branch string
	slugs, refs []string
	log         []entry
	onDefault   map[string]bool
	onRemote    map[string]bool
}

func bare(s string) string { return plain(pullSuffix.ReplaceAllString(s, "")) }

func Outcomes(r session.Row, rows []session.Row) ([]Outcome, error) {
	var threads []string
	if r.Agent.ID == "codex" {
		threads = threadsOf(rows)[r.Path]
	}
	trail, err := agent.ReadTrail(r.Agent, r.Path, nil, threads...)
	found, _ := outcomesOf(r, trail)
	return found, err
}

func outcomesOf(r session.Row, trail agent.Trail) ([]Outcome, int) {
	tops, repos := map[string]string{}, map[string]*repo{}
	topOf := func(dir string) string {
		if top, ok := tops[dir]; ok {
			return top
		}
		out, err := git(dir, "rev-parse", "--show-toplevel")
		tops[dir] = strings.TrimSuffix(out, "\n")
		if err != nil {
			tops[dir] = ""
		}
		return tops[dir]
	}
	where, since := make([]string, len(trail.Commits)), map[string]float64{}
	earliest := func(top string, at float64) {
		bound := at - logSlack
		if at == 0 {
			bound = r.Session.Mtime - undatedSlack
		}
		if old, ok := since[top]; !ok || bound < old {
			since[top] = bound
		}
	}
	for i, c := range trail.Commits {
		where[i] = cmp.Or(topOf(agent.SurvivingDir(folderOf(c.Command, cmp.Or(c.Dir, r.Session.Cwd)))), topOf(agent.SurvivingDir(r.Session.Cwd)))
		if where[i] != "" {
			earliest(where[i], c.At)
		}
	}
	home := ""
	if len(trail.PRs) > 0 {
		if home = topOf(agent.SurvivingDir(r.Session.Cwd)); home != "" {
			earliest(home, 0)
		}
	}
	for top, bound := range since {
		repos[top] = load(top, bound)
	}
	var out []Outcome
	missing := 0
	for i, c := range trail.Commits {
		rp := repos[where[i]]
		if rp == nil {
			missing++
			continue
		}
		o, ok := rp.outcome(c)
		if !ok {
			missing++
			continue
		}
		if j := slices.IndexFunc(out, func(m Outcome) bool { return same(m, o) }); j < 0 {
			out = append(out, o)
		} else if !rp.onDefault[out[j].Commit] || rp.onDefault[o.Commit] {
			out[j] = o
		}
	}
	tried := map[string]bool{}
	for _, p := range trail.PRs {
		key := strings.ToLower(p.Repo) + "#" + strconv.Itoa(p.Number)
		for _, top := range append(slices.Clone(where), home) {
			rp := repos[top]
			if rp == nil || tried[key] || !slices.Contains(rp.slugs, strings.ToLower(p.Repo)) {
				continue
			}
			tried[key] = true
			if o, ok := rp.pullRequest(p.Number, out); ok {
				out = append(out, o)
			}
		}
	}
	return out, missing
}

func same(a, b Outcome) bool {
	if a.Repo != b.Repo || plain(a.Subject) != plain(b.Subject) {
		return false
	}
	return a.Commit == b.Commit && a.Landed == b.Landed || a.Landed != "" && a.Landed == b.Landed
}

const folderArg = `("[^"]*"|'[^']*'|[^\s;&|)]+)`

func folderOf(command, cwd string) string {
	call := commitCall.FindStringSubmatchIndex(command)
	if call == nil {
		call = []int{len(command), len(command), 0, 0}
	}
	dir := cwd
	for _, m := range folderChange.FindAllStringSubmatch(command[:call[0]], -1) {
		dir = moved(dir, m[1])
	}
	for _, m := range gitFolder.FindAllStringSubmatch(command[call[2]:call[3]], -1) {
		dir = moved(dir, m[1])
	}
	return dir
}

func moved(dir, to string) string {
	to = strings.Trim(to, `"'`)
	switch {
	case to == "~" || strings.HasPrefix(to, "~/"):
		return filepath.Join(agent.Home(), strings.TrimPrefix(to, "~"))
	case filepath.IsAbs(to):
		return to
	}
	return filepath.Join(dir, to)
}

func load(top string, since float64) *repo {
	rp := &repo{top: top, onDefault: map[string]bool{}, onRemote: map[string]bool{}}
	if t, err := repoAt(top); err == nil {
		rp.slugs = t.Slugs
	}
	bound := "--since=@" + strconv.FormatInt(int64(since), 10)
	ref, _ := git(top, "rev-parse", "--abbrev-ref", "origin/HEAD")
	refs := []string{strings.TrimSpace(ref)}
	if refs[0] == "" || refs[0] == "origin/HEAD" {
		refs = nil
		for _, name := range []string{"origin/main", "origin/master", "main", "master"} {
			if _, err := git(top, "rev-parse", "--verify", "--quiet", name+"^{commit}"); err == nil {
				refs = []string{name}
				break
			}
		}
	}
	if len(refs) > 0 {
		rp.branch = strings.TrimPrefix(refs[0], "origin/")
		if local := "refs/heads/" + rp.branch; strings.HasPrefix(refs[0], "origin/") {
			if _, err := git(top, "rev-parse", "--verify", "--quiet", local); err == nil {
				refs = append(refs, local)
			}
		}
		rp.refs = refs
		listed, _ := git(top, append([]string{"rev-list", bound}, refs...)...)
		for _, sha := range strings.Fields(listed) {
			rp.onDefault[sha] = true
		}
	}
	remote, _ := git(top, "rev-list", bound, "--remotes")
	for _, sha := range strings.Fields(remote) {
		rp.onRemote[sha] = true
	}
	out, _ := git(top, "log", "--no-show-signature", "--all", "--reflog", bound, "--format="+logFormat+"%x1e")
	for _, raw := range strings.Split(out, "\x1e") {
		if e, ok := parseEntry(strings.TrimLeft(raw, "\n")); ok {
			rp.log = append(rp.log, e)
		}
	}
	return rp
}

const logFormat = "%H%x00%s%x00%b%x00%at%x00%ct%x00%P"

func parseEntry(raw string) (entry, bool) {
	parts := strings.SplitN(strings.TrimRight(raw, "\n"), "\x00", 6)
	if len(parts) < 6 {
		return entry{}, false
	}
	e := entry{commit: parsed(parts[0], parts[1], parts[2], parts[3]), body: parts[2], parents: strings.Fields(parts[5])}
	e.landed, _ = strconv.ParseFloat(strings.TrimSpace(parts[4]), 64)
	return e, true
}

func (rp *repo) outcome(c agent.Commit) (Outcome, bool) {
	if e, ok := rp.bySHA(c.SHA); ok {
		return rp.judge(e), true
	}
	messages := messageLines(c.Command, 0)
	if e, ok := rp.byMessage(c.At, messages); ok {
		return rp.judge(e), true
	}
	squash, bullet, ok := rp.squash(c.At-matchBefore, func(p string) bool { return slices.Contains(messages, p) })
	if !ok {
		return Outcome{}, false
	}
	o := rp.landedAs(Outcome{Kind: Made, Subject: bullet}, squash)
	return rp.reverted(o), true
}

func (rp *repo) bySHA(sha string) (entry, bool) {
	if !hexSHA.MatchString(sha) {
		return entry{}, false
	}
	var found []entry
	for _, e := range rp.log {
		if strings.HasPrefix(e.SHA, sha) {
			found = append(found, e)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	out, err := git(rp.top, "show", "--no-show-signature", "-s", "--format="+logFormat, sha+"^{commit}")
	if err != nil {
		return entry{}, false
	}
	e, ok := parseEntry(out)
	if ok {
		rp.reachable(e.SHA)
	}
	return e, ok
}

func (rp *repo) reachable(sha string) {
	for _, ref := range rp.refs {
		if _, err := git(rp.top, "merge-base", "--is-ancestor", sha, ref); err == nil {
			rp.onDefault[sha] = true
		}
	}
	if out, _ := git(rp.top, "for-each-ref", "--contains", sha, "--count=1", "refs/remotes"); out != "" {
		rp.onRemote[sha] = true
	}
}

func (rp *repo) byMessage(at float64, messages []string) (entry, bool) {
	var near []entry
	for _, e := range rp.log {
		if p := plain(e.Subject); len(p) >= shortestSubject && slices.Contains(messages, p) && (at == 0 || (e.At >= at-matchBefore && e.At <= at+matchAfter)) {
			near = append(near, e)
		}
	}
	if len(near) == 0 {
		return entry{}, false
	}
	return slices.MinFunc(near, func(a, b entry) int {
		return cmp.Or(cmp.Compare(distance(a, at), distance(b, at)), cmp.Compare(rp.reach(b), rp.reach(a)), cmp.Compare(b.landed, a.landed))
	}), true
}

func distance(e entry, at float64) float64 {
	if at == 0 {
		return -e.At
	}
	return abs(e.At - at)
}

func (rp *repo) reach(e entry) int {
	switch {
	case rp.onDefault[e.SHA]:
		return 2
	case rp.onRemote[e.SHA]:
		return 1
	}
	return 0
}

func abs(f float64) float64 { return max(f, -f) }

func (rp *repo) squash(after float64, carries func(string) bool) (entry, string, bool) {
	var best entry
	text, found := "", false
	for _, e := range rp.log {
		if e.PR == 0 || !rp.onDefault[e.SHA] || e.landed < after || (found && e.landed >= best.landed) {
			continue
		}
		lines := append([]string{pullSuffix.ReplaceAllString(e.Subject, "")}, bullets(e.body)...)
		if i := slices.IndexFunc(lines, func(s string) bool { return len(plain(s)) >= shortestSubject && carries(plain(s)) }); i >= 0 {
			best, text, found = e, lines[i], true
		}
	}
	return best, text, found
}

func (rp *repo) judge(e entry) Outcome {
	o := Outcome{Kind: Made, Commit: e.SHA, Subject: e.Subject, Repo: rp.top, Branch: rp.branch, Stage: Committed}
	if rp.onDefault[e.SHA] {
		return rp.reverted(rp.landedAs(o, e))
	}
	if squash, _, ok := rp.squash(e.At, func(p string) bool { return p == bare(e.Subject) }); ok {
		return rp.reverted(rp.landedAs(o, squash))
	}
	if copied, ok := rp.copyOf(e, rp.onDefault); ok {
		return rp.reverted(rp.landedAs(o, copied))
	}
	if _, ok := rp.copyOf(e, rp.onRemote); ok || rp.onRemote[e.SHA] {
		o.Stage = Pushed
	}
	return o
}

func (rp *repo) copyOf(e entry, on map[string]bool) (entry, bool) {
	p := plain(e.Subject)
	var found []entry
	for _, c := range rp.log {
		if on[c.SHA] && len(p) >= shortestSubject && plain(c.Subject) == p && c.At >= e.At-matchBefore && c.At <= e.At+matchAfter && c.landed >= e.landed {
			found = append(found, c)
		}
	}
	if len(found) == 0 {
		return entry{}, false
	}
	return slices.MinFunc(found, func(a, b entry) int { return cmp.Compare(a.landed, b.landed) }), true
}

func (rp *repo) landedAs(o Outcome, e entry) Outcome {
	o.Repo, o.Branch, o.Stage = rp.top, rp.branch, Merged
	o.Landed, o.PR, o.at = e.SHA, e.PR, e.landed
	o.LandedAt = time.Unix(int64(e.landed), 0).UTC().Format(time.RFC3339)
	return o
}

func (rp *repo) reverted(o Outcome) Outcome {
	if o.Stage != Merged {
		return o
	}
	shas, subject := []string{o.Commit, o.Landed}, o.Subject
	for _, e := range slices.Backward(rp.log) {
		if !rp.onDefault[e.SHA] || e.landed < o.at || !reverts(e, shas, subject) {
			continue
		}
		if o.Stage == Merged {
			o.Stage, o.RevertedBy = Reverted, e.SHA
		} else {
			o.Stage, o.RevertedBy = Merged, ""
		}
		shas, subject = []string{e.SHA}, e.Subject
	}
	return o
}

func reverts(e entry, shas []string, subject string) bool {
	notes := revertNote.FindAllStringSubmatch(e.body, -1)
	if len(notes) == 0 {
		return subject != "" && bare(e.Subject) == plain(`Revert "`+subject+`"`)
	}
	return slices.ContainsFunc(notes, func(note []string) bool {
		return slices.ContainsFunc(shas, func(sha string) bool { return sha != "" && strings.HasPrefix(sha, note[1]) })
	})
}

func (rp *repo) pullRequest(number int, made []Outcome) (Outcome, bool) {
	o := Outcome{Kind: PullRequest, PR: number, Repo: rp.top, Branch: rp.branch, Stage: Opened}
	if i := slices.IndexFunc(rp.log, func(e entry) bool { return e.PR == number && rp.onDefault[e.SHA] }); i >= 0 {
		e := rp.log[i]
		o = rp.landedAs(o, e)
		if o.Subject = pullSuffix.ReplaceAllString(e.Subject, ""); len(e.parents) > 1 {
			o.Subject = ""
		}
		o = rp.reverted(o)
		if len(e.parents) > 1 {
			carried, _ := git(rp.top, "rev-list", e.parents[0]+".."+e.SHA)
			for j := range made {
				m := &made[j]
				if m.Repo != rp.top || m.PR != 0 || m.Commit == "" || !strings.Contains(carried, m.Commit) {
					continue
				}
				if m.PR = number; m.Stage == Merged && o.Stage == Reverted {
					m.Stage, m.RevertedBy = Reverted, o.RevertedBy
				}
			}
		}
	}
	if slices.ContainsFunc(made, func(m Outcome) bool { return m.Repo == rp.top && m.PR == number }) {
		return Outcome{}, false
	}
	return o, true
}
