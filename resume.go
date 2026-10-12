package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/adammiribyan/superfind/internal/session"
	"github.com/adammiribyan/superfind/internal/tui"
)

type shape int

const (
	nothing shape = iota
	exact
	word
	query
)

type named struct {
	shape shape
	text  string
}

func nameOf(tokens []string) named {
	switch {
	case len(tokens) == 0:
		return named{nothing, ""}
	case len(tokens) > 1:
		return named{query, strings.Join(tokens, " ")}
	}
	t := tokens[0]
	switch {
	case session.Parse(t).Words != t:
		return named{query, t}
	case t == "-", len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"', session.LooksLikePath(t), strings.HasSuffix(t, ".jsonl"),
		len(t) >= session.ShortestIDPrefix && strings.Trim(t, "0123456789abcdefABCDEF-") == "":
		return named{exact, t}
	}
	return named{word, t}
}

type chooser int

const (
	noChoice chooser = iota
	byPicker
	byRank
)

type plan struct {
	lookup bool
	then   chooser
	exec   bool
}

func resumePlan(s shape, interactive, stdoutTTY, run bool) plan {
	exec := run || interactive && stdoutTTY
	switch {
	case s == exact:
		return plan{lookup: true, exec: exec}
	case s == nothing || interactive:
		return plan{lookup: s == word, then: byPicker, exec: exec}
	}
	return plan{lookup: s == word, then: byRank}
}

func resumeArgs(command string, args []string) (run bool, tokens []string) {
	for i, arg := range args {
		switch {
		case arg == "--run":
			run = true
		case arg == "--":
			return run, append(tokens, args[i+1:]...)
		case strings.HasPrefix(arg, "-") && arg != "-":
			usageError("superfind " + command + ": there is no " + arg + " option. See superfind help " + command + ".")
		default:
			tokens = append(tokens, arg)
		}
	}
	return run, tokens
}

func runResume(command string, args []string) {
	run, tokens := resumeArgs(command, args)
	n := nameOf(tokens)
	tty := terminal()
	if tty != nil {
		tty.Close()
	}
	p := resumePlan(n.shape, tty != nil, term.IsTerminal(os.Stdout.Fd()), run)
	if p.lookup {
		if r := lookup(command, n); r != nil {
			deliver(command, *r, p.exec)
			return
		}
	}
	switch p.then {
	case byPicker:
		chosen, err := tui.Pick(build(), n.text)
		if err != nil {
			fail("superfind " + command + ": " + err.Error())
		}
		if chosen == nil {
			exit(1)
		}
		finish(command, *chosen, p.exec)
	case byRank:
		deliver(command, top(command, n.text), p.exec)
	}
}

func lookup(command string, n named) *session.Row {
	switch n.shape {
	case exact:
		r, indexed, _ := findSession(command, n.text)
		if !indexed {
			fail("superfind " + command + ": not an indexed session: " + n.text)
		}
		return &r
	case word:
		if matches, _ := session.ByID(annotated(command, session.Index), n.text); len(matches) == 1 {
			return &matches[0]
		}
	}
	return nil
}

func top(command, text string) session.Row {
	q := mustParse(command, text)
	rows := annotated(command, session.Index)
	eligible := q.Rows(rows, false)
	ranked := session.RecentFirst(eligible)
	if q.Words != "" {
		db := mustOpenSearch()
		var err error
		if ranked, err = askOrder(db, q.Words, eligible, rows); err != nil {
			fmt.Fprintln(stderr, "superfind "+command+": "+err.Error()+" Taking the best keyword match instead.")
			if ranked, err = session.Search(db, text, rows); err != nil {
				fail("superfind " + command + ": " + err.Error())
			}
		}
	}
	if len(ranked) == 0 {
		fail("superfind " + command + ": no session fits " + text)
	}
	return ranked[0]
}
