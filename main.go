package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/ask"
	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
	"github.com/adammiribyan/superfind/internal/tui"
	"github.com/adammiribyan/superfind/internal/typesafe"
)

const version = "0.9.0"

const usage = `superfind: find a past AI coding session and resume it. It reads Claude Code, Codex, and Grok sessions.

  superfind               open the picker (after shell setup; see superfind init); sfd is short for it
  superfind init zsh|bash print the shell setup; add  eval "$(superfind init zsh)"  to ~/.zshrc
                          or  eval "$(superfind init bash)"  to ~/.bashrc
  superfind pick          open the picker; prints the chosen session's title, then the command
                          that resumes it
  superfind search WORDS  print sessions whose title or conversation contains every word;
                          is:waiting and is:unfinished filter by how a session ended,
                          agent:claude, agent:codex, and agent:grok by the agent
  superfind ask WORDS     print the sessions that best fit what you describe, even in other words;
                          takes the same is: and agent: filters
  superfind preview SESSION [--width N] [--recent] [--query Q]
                          print where a session left off, or its recent conversation
  superfind command SESSION
                          print the shell command that resumes a session
  superfind index         build or refresh the index
  superfind label         mark sessions waiting on you (needs a TypeSafe key)
  superfind --version
`

const zshSetup = `typeset -g _superfind_bin=%s

superfind() {
  if (( $# )); then
    "$_superfind_bin" "$@"
    return
  fi
  local out
  out=$("$_superfind_bin" pick) || return
  print -r -- $'Resuming \e[1m'"${out%%$'\n'*}"$'\e[0m'
  print -s -- "${out#*$'\n'}"
  eval "${out#*$'\n'}"
}

sfd() { superfind "$@"; }

_superfind_widget() {
  local out
  if out=$("$_superfind_bin" pick); then
    zle -I
    print -r -- $'Resuming \e[1m'"${out%%$'\n'*}"$'\e[0m'
    BUFFER=${out#*$'\n'}
    zle accept-line
  else
    zle reset-prompt
  fi
}
zle -N _superfind_widget
bindkey '^Xc' _superfind_widget
`

const bashSetup = `_superfind_bin=%s

_superfind_resume() {
  printf 'Resuming \e[1m%%s\e[0m\n' "${1%%%%$'\n'*}"
  history -s "${1#*$'\n'}"
  eval "${1#*$'\n'}"
}

superfind() {
  if (( $# )); then
    "$_superfind_bin" "$@"
    return
  fi
  local out
  out=$("$_superfind_bin" pick) || return
  _superfind_resume "$out"
}

sfd() { superfind "$@"; }

_superfind_widget() {
  local out
  out=$("$_superfind_bin" pick) && _superfind_resume "$out"
}
[[ $- == *i* ]] && bind -x '"\C-xc": _superfind_widget'
`

var shellSetups = map[string]string{"zsh": zshSetup, "bash": bashSetup}

func printRows(rows []session.Row, shown render.Tags) {
	secondary := render.SecondaryFromEnv()
	for _, r := range rows {
		t := render.RowTextOf(r, shown)
		display := " " + t.Title + "  " + secondary + render.RowContext(t, secondary) + render.Reset
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", r.Path, r.Session.Cwd, r.Session.ID, r.Home, t.Title, display)
	}
}

func handoffTitle(r session.Row) string {
	title := strings.Join(strings.Fields(r.Session.Title), " ")
	if r.Session.Active() {
		title += render.Reset + render.Dim + " · may still be open in another window"
	}
	return title
}

func selfPath() string {
	if strings.Contains(os.Args[0], "/") {
		if abs, err := filepath.Abs(os.Args[0]); err == nil {
			return abs
		}
	}
	if found, err := exec.LookPath(os.Args[0]); err == nil {
		if abs, err := filepath.Abs(found); err == nil {
			return abs
		}
	}
	exe, _ := os.Executable()
	return exe
}

func mustOpenSearch() *sql.DB {
	db, err := session.OpenSearch()
	if err != nil {
		fail("superfind: can't open the search index in " + session.CacheDir() + ": " + err.Error())
	}
	return db
}

func mustIndex(command string) []session.Row {
	noticed := false
	rows, err := session.Index(func(stale int) {
		if term.IsTerminal(os.Stderr.Fd()) {
			fmt.Fprintf(os.Stderr, "  Indexing %d sessions…", stale)
			noticed = true
		}
	})
	if noticed {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	if err != nil {
		fail("superfind " + command + ": " + err.Error())
	}
	return rows
}

func sessionAt(command, arg string) (r session.Row, indexed bool, rows []session.Row) {
	path := filepath.Join(agent.Resolve(filepath.Dir(arg)), filepath.Base(arg))
	p, ok := agent.Locate(path, agent.Places())
	if !ok {
		labels := make([]string, len(agent.Agents))
		for i, a := range agent.Agents {
			labels[i] = a.Label
		}
		fail("superfind " + command + ": " + arg + " is not inside a " + strings.Join(labels[:len(labels)-1], ", ") +
			", or " + labels[len(labels)-1] + " home")
	}
	rows = session.Annotate(mustIndex(command))
	for _, r := range rows {
		if r.Path == path {
			return r, true, rows
		}
	}
	return session.Row{Agent: p.Agent, Home: p.Home, Path: path}, false, rows
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

func main() {
	typesafe.UserAgent = "superfind/" + version
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		return
	}
	switch args[0] {
	case "--version", "version":
		fmt.Println("superfind " + version)
	case "init":
		setup, ok := "", len(args) > 1
		if ok {
			setup, ok = shellSetups[args[1]]
		}
		if !ok {
			fail("superfind init: which shell? superfind init zsh or superfind init bash")
		}
		fmt.Printf(setup, agent.ShellQuote(selfPath()))
	case "index":
		fmt.Printf("%d sessions indexed\n", len(mustIndex("index")))
	case "search":
		rows := session.Annotate(mustIndex("search"))
		found, err := session.Search(mustOpenSearch(), strings.Join(args[1:], " "), rows)
		if err != nil {
			fail("superfind search: " + err.Error())
		}
		printRows(found, render.TagsFor(rows))
	case "ask":
		rows := session.Annotate(mustIndex("ask"))
		shown := render.TagsFor(rows)
		db := mustOpenSearch()
		query, eligible := session.Narrow(strings.Join(args[1:], " "), rows)
		if query == "" {
			printRows(session.RecentFirst(eligible), shown)
			return
		}
		paths, err := ask.Ask(db, query, eligible)
		var failure ask.Failure
		if errors.As(err, &failure) {
			fail(failure.Error())
		}
		printRows(session.InOrder(rows, paths), shown)
	case "label":
		key := typesafe.Key()
		if key == "" {
			fail("superfind label: needs a TypeSafe key in TYPESAFE_API_KEY or ~/.config/superfind/env")
		}
		if err := session.Label(key, mustIndex("label")); err != nil {
			fail("superfind label: " + err.Error())
		}
	case "preview":
		if len(args) < 2 {
			fail("superfind preview: which session?")
		}
		req := render.PreviewRequest{Width: 80, Secondary: render.SecondaryFromEnv()}
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--recent":
				req.Recent = true
			case "--width":
				if i+1 < len(args) {
					req.Width, _ = strconv.Atoi(args[i+1])
					i++
				}
			case "--query":
				if i+1 < len(args) {
					req.Query = args[i+1]
					i++
				}
			}
		}
		r, indexed, rows := sessionAt("preview", args[1])
		req.Agent, req.Path = r.Agent, r.Path
		var header []string
		if indexed {
			header = append(render.PreviewHeader(req, r, render.TagsFor(rows), mustOpenSearch()), "")
		}
		body, err := render.PreviewBody(req)
		for _, line := range append(header, body...) {
			fmt.Println(line)
		}
		if err != nil {
			os.Exit(1)
		}
	case "command":
		if len(args) < 2 {
			fail("superfind command: which session?")
		}
		r, indexed, _ := sessionAt("command", args[1])
		if !indexed {
			fail("superfind command: not an indexed session: " + args[1])
		}
		fmt.Println(agent.ResumeCommand(r.Agent, r.Home, r.Session.Cwd, r.Session.ID))
	case "pick":
		chosen, err := tui.Pick()
		if err != nil {
			fail(err.Error())
		}
		if chosen == nil {
			os.Exit(1)
		}
		fmt.Println(handoffTitle(*chosen))
		fmt.Println(agent.ResumeCommand(chosen.Agent, chosen.Home, chosen.Session.Cwd, chosen.Session.ID))
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}
