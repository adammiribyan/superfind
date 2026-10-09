package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/colorprofile"
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
  superfind help COMMAND  explain one command
  superfind --version
`

var helps = map[string]string{
	"init": `superfind init zsh|bash

Print the shell setup that lets superfind change your shell's folder when you resume a session.
Add one line to your shell's startup file:

  eval "$(superfind init zsh)"     # in ~/.zshrc
  eval "$(superfind init bash)"    # in ~/.bashrc, or ~/.bash_profile on macOS

Then superfind with no arguments, sfd, or Ctrl-X c opens the picker. Choosing a session cds
into its folder and runs its agent's resume command, which lands in your shell history.
`,
	"pick": `superfind pick

Open the picker full screen. When you choose a session, print its title on the first line and
the command that resumes it on the second, then exit 0. Exit 1 if you cancel.
The shell setup from superfind init runs that command for you.
`,
	"search": `superfind search WORDS

Print the sessions whose title, project, branch, or conversation contains every word, titles
that match first. With no words, print every session, newest first.

  is:waiting      only sessions waiting on you; is:unfinished works the same way
  agent:codex     only Codex sessions; agent:claude and agent:grok work the same way

Each line has six tab-separated fields: the session, its folder, its id, its agent's home,
its title, and the line the picker shows.
`,
	"ask": `superfind ask WORDS

Print the sessions that best fit what you describe, even when they used other words, best
first. Ranked by Jev with a TypeSafe key, or by Claude Haiku through your Claude Code login
without one. Takes the same is: and agent: filters as search, and prints the same fields.
`,
	"preview": `superfind preview SESSION [--width N] [--recent] [--query Q]

Print where a session left off: your last real request and the end of the reply.
SESSION is a path that superfind search prints first.

  --width N       wrap to N columns (default 80)
  --recent        print the recent conversation instead
  --query Q       show the passage that matches Q under the title
`,
	"command": `superfind command SESSION

Print the shell command that resumes a session: cd into its folder, or the nearest parent
that still exists, then run its agent's resume command.
SESSION is a path that superfind search prints first.
`,
	"index": `superfind index

Build or refresh the index in ~/.cache/superfind/, or $XDG_CACHE_HOME/superfind/ when that is
set. Only sessions that changed are read again. Every other command refreshes the index first,
so you rarely need this.
`,
	"label": `superfind label

Mark sessions waiting on you or unfinished, using Jev. Needs a TypeSafe key in TYPESAFE_API_KEY
or ~/.config/superfind/env. It sends the ending of each recent session that changed to TypeSafe.
The picker runs this in the background when it opens.
`,
	"version": `superfind --version

Print superfind's version.
`,
}

var commandNames = []string{"init", "pick", "search", "ask", "preview", "command", "index", "label"}

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

var stdout, stderr io.Writer = os.Stdout, os.Stderr

func printRows(rows []session.Row, shown render.Tags) {
	secondary := render.SecondaryFromEnv()
	for _, r := range rows {
		t := render.RowTextOf(r, shown)
		display := " " + t.Title + "  " + secondary + render.RowContext(t, secondary) + render.Reset
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", field(r.Path), field(r.Session.Cwd), field(r.Session.ID), field(r.Home), t.Title, display)
	}
}

func field(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) || agent.Bidi(r) {
			return '?'
		}
		return r
	}, s)
}

func handoffTitle(r session.Row) string {
	title := render.Flat(r.Session.Title)
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

func mustIndex(command string) []session.Row { return mustRefresh(command, session.Index) }

func mustRefresh(command string, refresh func(func(int)) ([]session.Row, []error, error)) []session.Row {
	noticed := false
	rows, problems, err := refresh(func(stale int) {
		if term.IsTerminal(os.Stderr.Fd()) {
			fmt.Fprintf(stderr, "  Indexing %d sessions…", stale)
			noticed = true
		}
	})
	if noticed {
		fmt.Fprint(stderr, "\r\033[K")
	}
	if err != nil {
		fail("superfind " + command + ": " + err.Error())
	}
	if unreadable := session.Unreadable(problems); unreadable != "" {
		fmt.Fprintln(stderr, "superfind "+command+": "+unreadable)
	}
	return rows
}

func sessionAt(command, arg string) (r session.Row, indexed bool, rows []session.Row) {
	given, _ := filepath.Abs(arg)
	candidates := []string{given, filepath.Join(agent.Resolve(filepath.Dir(arg)), filepath.Base(arg))}
	var located []session.Row
	places := agent.Places()
	for _, path := range candidates {
		if p, ok := agent.Locate(path, places); ok {
			located = append(located, session.Row{Agent: p.Agent, Home: p.Home, Path: path})
		}
	}
	if len(located) == 0 {
		labels := make([]string, len(agent.Agents))
		for i, a := range agent.Agents {
			labels[i] = a.Label
		}
		fail("superfind " + command + ": " + arg + " is not inside a " + strings.Join(labels[:len(labels)-1], ", ") +
			", or " + labels[len(labels)-1] + " home")
	}
	rows = session.Annotate(mustIndex(command))
	for _, want := range located {
		for _, r := range rows {
			if r.Path == want.Path {
				return r, true, rows
			}
		}
	}
	return located[len(located)-1], false, rows
}

var output *bufio.Writer

func exit(code int) {
	if err := output.Flush(); err != nil && code == 0 {
		fmt.Fprintln(stderr, "superfind: couldn't write the output: "+err.Error())
		code = 1
	}
	os.Exit(code)
}

func fail(message string) {
	fmt.Fprintln(stderr, message)
	exit(1)
}

func usageError(message string) {
	fmt.Fprintln(stderr, message)
	exit(2)
}

func searchWords(command string, args []string) string {
	var words []string
	for i, arg := range args {
		switch {
		case arg == "--":
			return strings.Join(append(words, args[i+1:]...), " ")
		case arg == "--deep" && command == "search":
			usageError("superfind search: --deep is now its own command: superfind ask WORDS")
		case strings.HasPrefix(arg, "--"):
			usageError("superfind " + command + ": there is no " + arg + " option. To search for it, put -- first: superfind " +
				command + " -- " + arg)
		}
		words = append(words, arg)
	}
	return strings.Join(words, " ")
}

func main() {
	typesafe.UserAgent = "superfind/" + version
	if render.NoColor {
		stdout = &colorprofile.Writer{Forward: os.Stdout, Profile: colorprofile.Ascii}
		stderr = &colorprofile.Writer{Forward: os.Stderr, Profile: colorprofile.Ascii}
	}
	output = bufio.NewWriter(stdout)
	stdout = output
	run(os.Args[1:])
	exit(0)
}

func run(args []string) {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return
	}
	switch args[0] {
	case "--help", "-h", "help":
		if len(args) > 1 {
			explain(args[1])
			return
		}
		fmt.Fprint(stdout, usage)
		return
	}
	if _, known := helps[args[0]]; known && asksForHelp(args[1:]) {
		explain(args[0])
		return
	}
	switch args[0] {
	case "--version", "version":
		fmt.Fprintln(stdout, "superfind "+version)
	case "init":
		setup, ok := "", len(args) > 1
		if ok {
			setup, ok = shellSetups[args[1]]
		}
		if !ok {
			usageError("superfind init: which shell? superfind init zsh or superfind init bash")
		}
		fmt.Fprintf(stdout, setup, agent.ShellQuote(selfPath()))
	case "index":
		n, noun := len(mustIndex("index")), "sessions"
		if n == 1 {
			noun = "session"
		}
		fmt.Fprintf(stdout, "%d %s indexed\n", n, noun)
	case "search":
		query := searchWords("search", args[1:])
		rows := session.Annotate(mustIndex("search"))
		found, err := session.Search(mustOpenSearch(), query, rows)
		if errors.Is(err, session.ErrDamaged) {
			rows = session.Annotate(mustRefresh("search", session.Rebuild))
			found, err = session.Search(mustOpenSearch(), query, rows)
		}
		if err != nil {
			fail("superfind search: " + err.Error())
		}
		printRows(found, render.TagsFor(rows))
	case "ask":
		rows := session.Annotate(mustIndex("ask"))
		shown := render.TagsFor(rows)
		db := mustOpenSearch()
		query, eligible := session.Narrow(searchWords("ask", args[1:]), rows)
		if query == "" {
			printRows(session.RecentFirst(eligible), shown)
			return
		}
		paths, err := ask.Ask(context.Background(), db, query, eligible)
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
		req := render.PreviewRequest{Width: 80, Secondary: render.SecondaryFromEnv()}
		target := ""
		for i := 1; i < len(args); i++ {
			switch arg := args[i]; {
			case arg == "--recent":
				req.Recent = true
			case arg == "--width" || arg == "--query":
				if i+1 == len(args) {
					usageError("superfind preview: " + arg + " needs a value. See superfind help preview.")
				}
				i++
				if arg == "--query" {
					req.Query = args[i]
					continue
				}
				width, err := strconv.Atoi(args[i])
				if err != nil || width < 1 {
					usageError("superfind preview: --width needs a number of columns, like --width 80.")
				}
				req.Width = width
			case strings.HasPrefix(arg, "-"):
				usageError("superfind preview: there is no " + arg + " option. See superfind help preview.")
			case target != "":
				usageError("superfind preview: one session at a time, not " + arg + " too.")
			default:
				target = arg
			}
		}
		if target == "" {
			usageError("superfind preview: which session? Pass a path that superfind search prints first.")
		}
		r, indexed, rows := sessionAt("preview", target)
		req.Agent, req.Path = r.Agent, r.Path
		var header []string
		if indexed {
			header = append(render.PreviewHeader(req, r, render.TagsFor(rows), mustOpenSearch()), "")
		}
		body, err := render.PreviewBody(req)
		for _, line := range append(header, body...) {
			fmt.Fprintln(stdout, line)
		}
		if err != nil {
			exit(1)
		}
	case "command":
		if len(args) < 2 {
			usageError("superfind command: which session?")
		}
		r, indexed, _ := sessionAt("command", args[1])
		if !indexed {
			fail("superfind command: not an indexed session: " + args[1])
		}
		fmt.Fprintln(stdout, agent.ResumeCommand(r.Agent, r.Home, r.Session.Cwd, r.Session.ID))
	case "pick":
		chosen, err := tui.Pick()
		if err != nil {
			fail(err.Error())
		}
		if chosen == nil {
			exit(1)
			return
		}
		fmt.Fprintln(stdout, handoffTitle(*chosen))
		fmt.Fprintln(stdout, agent.ResumeCommand(chosen.Agent, chosen.Home, chosen.Session.Cwd, chosen.Session.ID))
	default:
		unknownCommand(args[0])
	}
}

func asksForHelp(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			return false
		case "--query", "--width":
			i++
		case "--help", "-h":
			return true
		}
	}
	return false
}

func explain(name string) {
	if name == "--version" {
		name = "version"
	}
	text, ok := helps[name]
	if !ok {
		unknownCommand(name)
	}
	fmt.Fprint(stdout, text)
}

func unknownCommand(name string) {
	fmt.Fprintf(stderr, "superfind: there is no %q command. The commands are %s, and %s.\nRun superfind --help to see what each one does.\n",
		name, strings.Join(commandNames[:len(commandNames)-1], ", "), commandNames[len(commandNames)-1])
	exit(2)
}
