package main

import (
	"path/filepath"

	"github.com/adammiribyan/hop/internal/agent"
)

type tags struct {
	agents bool
	homes  map[*agent.Agent]bool
}

func tagsFor(rows []Row) tags {
	seen := map[*agent.Agent]map[string]bool{}
	for _, r := range rows {
		if seen[r.Agent] == nil {
			seen[r.Agent] = map[string]bool{}
		}
		seen[r.Agent][r.Home] = true
	}
	t := tags{agents: len(seen) > 1, homes: map[*agent.Agent]bool{}}
	for a, homes := range seen {
		t.homes[a] = len(homes) > 1
	}
	return t
}

func (t tags) of(r Row) []string {
	var out []string
	if t.agents {
		out = append(out, r.Agent.ID)
	}
	if t.homes[r.Agent] {
		out = append(out, filepath.Base(r.Home))
	}
	return out
}

func pickerLabel(rows []Row, in []agent.Place) string {
	present := map[*agent.Agent]bool{}
	for _, r := range rows {
		present[r.Agent] = true
	}
	if len(rows) == 0 {
		for _, p := range in {
			if len(p.Agent.Sessions(p.Home)) > 0 {
				present[p.Agent] = true
			}
		}
	}
	if len(present) == 1 {
		for a := range present {
			return a.Label + " sessions"
		}
	}
	return "Sessions"
}
