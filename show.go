package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/session"
	"github.com/adammiribyan/superfind/internal/show"
)

func looksLikeID(arg string) bool {
	return arg != "" && !strings.ContainsAny(arg, "/.~")
}

func findSession(command, arg string) (session.Row, bool, []session.Row) {
	if !looksLikeID(arg) {
		return sessionAt(command, arg)
	}
	rows := session.Annotate(mustIndex(command))
	matches, exact := session.ByID(rows, arg)
	switch {
	case len(matches) == 1:
		return matches[0], true, rows
	case len(matches) == 0 && len(arg) < session.ShortestIDPrefix:
		usageError(fmt.Sprintf("superfind %s: no session with id %s. To match the start of an id, give at least %d characters.",
			command, arg, session.ShortestIDPrefix))
	case len(matches) == 0:
		fail("superfind " + command + ": no session with id " + arg)
	}
	advice := "give more of the id, or one of these paths"
	if exact {
		advice = "copies of it live in more than one place; give one of these paths"
	}
	lines := []string{fmt.Sprintf("superfind %s: %s matches %d sessions; %s:", command, arg, len(matches), advice)}
	for _, r := range matches {
		lines = append(lines, "  "+field(r.Session.ID)+"  "+field(r.Path))
	}
	usageError(strings.Join(lines, "\n"))
	return session.Row{}, false, nil
}

func runShow(args []string) {
	opt := show.Options{MaxBytes: show.DefaultMaxBytes}
	asJSON, target := false, ""
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--json":
			asJSON = true
		case arg == "--tail" || arg == "--max-bytes":
			if i+1 == len(args) {
				usageError("superfind show: " + arg + " needs a number. See superfind help show.")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 || (arg == "--tail" && n == 0) {
				usageError("superfind show: " + arg + " needs a whole number, like " + arg + " 8. See superfind help show.")
			}
			if arg == "--tail" {
				opt.Tail = n
			} else {
				opt.MaxBytes = n
			}
		case strings.HasPrefix(arg, "-"):
			usageError("superfind show: there is no " + arg + " option. See superfind help show.")
		case target != "":
			usageError("superfind show: one session at a time, not " + arg + " too.")
		default:
			target = arg
		}
	}
	if target == "" {
		usageError("superfind show: which session? Pass its id, or a path that superfind search prints first.")
	}
	r, indexed, _ := findSession("show", target)
	if !indexed {
		fail("superfind show: not an indexed session: " + target)
	}
	t, err := agent.ReadTranscript(r.Agent, r.Path)
	if err != nil {
		fail("superfind show: couldn't read " + field(r.Path) + ": " + err.Error())
	}
	record := session.RecordOf(r, show.Clean)
	conversation := show.Select(t.Turns, opt)
	if asJSON {
		err = show.WriteJSON(stdout, record, conversation)
	} else {
		err = show.WriteText(stdout, record, conversation)
	}
	if err != nil {
		fail("superfind show: " + err.Error())
	}
}
