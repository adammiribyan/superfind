package session

import (
	"cmp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
)

type Query struct {
	Words    string
	Agents   bool
	Here     bool
	Statuses []string
	From     []*agent.Agent
	Branches []string
	After    time.Time
	Before   time.Time
	Problem  *Problem
}

type Problem struct{ Token, Hint string }

func (p *Problem) Error() string { return "can't read " + p.Token + ". " + p.Hint }

type filter struct {
	hint  string
	parse func(q *Query, value string, now time.Time) bool
}

const whenHint = ": takes a day like 2026-10-09, today, yesterday, 12h, 3d, or 2w."

var filters = map[string]filter{
	"is": {parse: func(q *Query, v string, _ time.Time) bool {
		v = strings.ToLower(v)
		if v == "agent" {
			q.Agents = true
		} else if label := statusLabelFor(v); label != "" {
			q.Statuses = append(q.Statuses, label)
		}
		return true
	}},
	"agent": {parse: func(q *Query, v string, _ time.Time) bool {
		if a := agent.Named(strings.ToLower(v)); a != nil {
			q.From = append(q.From, a)
		}
		return true
	}},
	"in": {hint: "in: takes here, as in in:here.", parse: func(q *Query, v string, _ time.Time) bool {
		q.Here = q.Here || strings.EqualFold(v, "here")
		return strings.EqualFold(v, "here")
	}},
	"branch": {hint: "branch: takes a branch name, like branch:main.", parse: func(q *Query, v string, _ time.Time) bool {
		if v != "" {
			q.Branches = append(q.Branches, v)
		}
		return v != ""
	}},
	"after": {hint: "after" + whenHint, parse: func(q *Query, v string, now time.Time) bool {
		t, ok := moment(v, now)
		if ok && (q.After.IsZero() || t.After(q.After)) {
			q.After = t
		}
		return ok
	}},
	"before": {hint: "before" + whenHint, parse: func(q *Query, v string, now time.Time) bool {
		t, ok := moment(v, now)
		if ok && (q.Before.IsZero() || t.Before(q.Before)) {
			q.Before = t
		}
		return ok
	}},
}

func Parse(text string) Query { return parseAt(text, time.Now()) }

func parseAt(text string, now time.Time) Query {
	var q Query
	var words []string
	for _, token := range strings.Fields(text) {
		name, value, isFilter := strings.Cut(token, ":")
		f, known := filters[strings.ToLower(name)]
		switch {
		case !isFilter || !known:
			words = append(words, token)
		case !f.parse(&q, value, now) && q.Problem == nil:
			q.Problem = &Problem{token, f.hint}
		}
	}
	q.Words = strings.Join(words, " ")
	return q
}

var ago = regexp.MustCompile(`^([1-9][0-9]{0,3})([hdw])$`)

func moment(value string, now time.Time) (time.Time, bool) {
	now = now.In(time.Local)
	y, m, d := now.Date()
	value = strings.ToLower(value)
	switch value {
	case "today":
		return startOfDay(y, m, d), true
	case "yesterday":
		return startOfDay(y, m, d-1), true
	}
	if found := ago.FindStringSubmatch(value); found != nil {
		n, _ := strconv.Atoi(found[1])
		unit := map[string]time.Duration{"h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[found[2]]
		return now.Add(-time.Duration(n) * unit), true
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, false
	}
	return startOfDay(t.Date()), true
}

func startOfDay(y int, m time.Month, d int) time.Time {
	y, m, d = time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Date()
	t := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	for t.Day() != d {
		t = t.Add(time.Hour)
	}
	return t
}

func (q Query) Rows(rows []Row, everyKind bool) []Row {
	here := func(string) bool { return true }
	if q.Here {
		here = hereMatcher()
	}
	var kept []Row
	for _, r := range rows {
		if q.keeps(r, everyKind) && here(r.Session.Cwd) {
			kept = append(kept, r)
		}
	}
	return kept
}

func (q Query) keeps(r Row, everyKind bool) bool {
	s := r.Session
	switch {
	case q.Agents && !s.ByAgent, !q.Agents && !everyKind && s.ByAgent:
		return false
	case len(q.Statuses) > 0 && !slices.Contains(q.Statuses, r.Status):
		return false
	case len(q.From) > 0 && !slices.Contains(q.From, r.Agent):
		return false
	case len(q.Branches) > 0 && !slices.ContainsFunc(q.Branches, func(b string) bool { return strings.EqualFold(b, s.Branch) }):
		return false
	case !q.After.IsZero() && s.Mtime < seconds(q.After), !q.Before.IsZero() && s.Mtime >= seconds(q.Before):
		return false
	}
	return true
}

func seconds(t time.Time) float64 { return float64(t.Unix()) + float64(t.Nanosecond())/1e9 }

func hereMatcher() func(string) bool {
	seen := map[string][]string{}
	forms := func(path string) []string {
		if f, ok := seen[path]; ok {
			return f
		}
		f := []string{filepath.Clean(path)}
		if real, err := filepath.EvalSymlinks(f[0]); err == nil && real != f[0] {
			f = append(f, real)
		}
		seen[path] = f
		return f
	}
	var here []string
	if pwd := os.Getenv("PWD"); filepath.IsAbs(pwd) && sameFolder(pwd, ".") {
		here = append(here, forms(pwd)...)
	}
	if wd, err := syscall.Getwd(); err == nil {
		here = append(here, forms(wd)...)
	}
	ends := append([]string{"/"}, forms(agent.Home())...)
	return func(cwd string) bool {
		if cwd == "" {
			return false
		}
		own := forms(cwd)
		end := slices.ContainsFunc(own, func(c string) bool { return slices.Contains(ends, c) })
		for _, c := range own {
			for _, p := range here {
				if c == p || inside(c, p) || inside(p, c) && !end {
					return true
				}
			}
		}
		return false
	}
}

func sameFolder(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

func inside(path, folder string) bool {
	return strings.HasPrefix(path, strings.TrimSuffix(folder, "/")+"/") && path != folder
}

func Last(q Query, rows []Row) (Row, bool) {
	q.Here = true
	kept := q.Rows(rows, false)
	if len(kept) == 0 {
		return Row{}, false
	}
	return slices.MinFunc(kept, func(a, b Row) int {
		return cmp.Or(cmp.Compare(throwaway(a), throwaway(b)), cmp.Compare(b.Session.Mtime, a.Session.Mtime), strings.Compare(a.Path, b.Path))
	}), true
}

func throwaway(r Row) int {
	if r.Session.Throwaway() {
		return 1
	}
	return 0
}
