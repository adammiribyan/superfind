package session

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
)

const reportHours = 24

var spanBack = regexp.MustCompile(`^(\d{1,4})([hdw])$`)

func ParseReport(query string, now time.Time) (after, before time.Time, rest string, err error) {
	var others []string
	var afterWord, beforeWord string
	for _, w := range strings.Fields(query) {
		name, value, filter := strings.Cut(w, ":")
		name = strings.ToLower(name)
		switch {
		case !filter:
			others = append(others, w)
		case name == "after" || name == "before":
			t, ok := when(strings.ToLower(value), now)
			if !ok {
				return time.Time{}, time.Time{}, "", fmt.Errorf("%s needs a date like 2026-10-09, today, yesterday, or a span like 12h, 3d, or 2w", w)
			}
			if name == "after" {
				after, afterWord = t, w
			} else {
				before, beforeWord = t, w
			}
		case name == "is" || name == "agent":
			allowed := filterValues(name)
			if !slices.Contains(allowed, strings.ToLower(value)) {
				return time.Time{}, time.Time{}, "", fmt.Errorf("%s isn't a filter; %s: takes %s", w, name, orList(allowed))
			}
			others = append(others, name+":"+strings.ToLower(value))
		default:
			others = append(others, w)
		}
	}
	switch {
	case afterWord == "" && beforeWord == "":
		after = now.Add(-reportHours * time.Hour)
	case afterWord != "" && beforeWord != "" && !after.Before(before):
		return time.Time{}, time.Time{}, "", fmt.Errorf("%s is not earlier than %s, so no session could match", afterWord, beforeWord)
	}
	return after, before, strings.Join(others, " "), nil
}

func filterValues(name string) []string {
	var values []string
	if name == "agent" {
		for _, a := range agent.Agents {
			values = append(values, a.ID)
		}
		return values
	}
	for _, k := range statusKinds {
		if k.label != "" {
			values = append(values, k.name)
		}
	}
	return append(values, strings.TrimPrefix(AgentFilter, "is:"))
}

func orList(values []string) string {
	return strings.Join(values[:len(values)-1], ", ") + ", or " + values[len(values)-1]
}

func when(value string, now time.Time) (time.Time, bool) {
	y, m, d := now.Date()
	switch value {
	case "today":
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location()), true
	case "yesterday":
		return time.Date(y, m, d-1, 0, 0, 0, 0, now.Location()), true
	}
	if day, err := time.ParseInLocation(time.DateOnly, value, now.Location()); err == nil {
		return day, day.Year() >= 1970
	}
	span := spanBack.FindStringSubmatch(value)
	if span == nil {
		return time.Time{}, false
	}
	n, _ := strconv.Atoi(span[1])
	switch span[2] {
	case "h":
		return now.Add(-time.Duration(n) * time.Hour), true
	case "d":
		return now.AddDate(0, 0, -n), true
	}
	return now.AddDate(0, 0, -7*n), true
}

func Changed(rows []Row, after, before time.Time) []Row {
	from, until := math.Inf(-1), math.Inf(1)
	if !after.IsZero() {
		from = seconds(after)
	}
	if !before.IsZero() {
		until = seconds(before)
	}
	var out []Row
	for _, r := range rows {
		if s := r.Session; s.Mtime >= from && s.Mtime < until && !s.Throwaway() {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := out[i].Session.Mtime, out[j].Session.Mtime; a != b {
			return a > b
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func seconds(t time.Time) float64 { return float64(t.Unix()) + float64(t.Nanosecond())/1e9 }
