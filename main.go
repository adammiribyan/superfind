package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/ask"
	"github.com/adammiribyan/superfind/internal/blame"
	briefing "github.com/adammiribyan/superfind/internal/handoff"
	"github.com/adammiribyan/superfind/internal/open"
	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
	"github.com/adammiribyan/superfind/internal/show"
	"github.com/adammiribyan/superfind/internal/tui"
	"github.com/adammiribyan/superfind/internal/typesafe"
)

const version = "0.10.0"

const usage = `superfind: find a past AI coding session and resume it. It reads Claude Code, Codex, and Grok sessions.

  superfind               open the picker (after shell setup; see superfind init); sfd is short for it
  superfind init zsh|bash|fish
                          print the shell setup; add  eval "$(superfind init zsh)"  to ~/.zshrc,
                          eval "$(superfind init bash)"  to ~/.bashrc, or
                          superfind init fish | source  to ~/.config/fish/config.fish
  superfind pick          open the picker; prints the chosen session's title, then the command
                          that resumes it
  superfind last [FILTERS] [--json]
                          resume the newest session from this folder (after shell setup);
                          without the shell setup, or in a script, print its title and the
                          command that resumes it
  superfind search WORDS [--json] [--limit N]
                          print sessions whose title or conversation contains every word;
                          is:waiting and is:unfinished filter by how a session ended,
                          agent:claude, agent:codex, and agent:grok by the agent,
                          is:agent shows sessions another program started, is:open shows
                          sessions an agent has open right now, and in:here,
                          branch:NAME, after:DAY, and before:DAY narrow by folder, branch,
                          and last activity
  superfind ask WORDS [--json] [--limit N]
                          print the sessions that best fit what you describe, even in other words;
                          takes the same filters
  superfind preview SESSION [--width N] [--recent] [--query Q]
                          print where a session left off, or its recent conversation
  superfind show SESSION [--json] [--tail N] [--max-bytes N]
                          print a session's conversation as plain text, for scripts and agents
  superfind handoff SESSION [WORDS] [--json] [--max-bytes N]
                          print excerpts of a session for another agent to continue from:
                          where it left off, and the turns that mention WORDS
  superfind command SESSION
                          print the shell command that resumes a session
  superfind blame FILE|FILE:LINE|COMMIT [--json]
                          print the sessions that made a commit, or that edited a file
  superfind index         build or refresh the index
  superfind label         mark sessions waiting on you (needs a TypeSafe key)
  superfind help COMMAND  explain one command
  superfind --version

SESSION is a session id, a unique start of one (8 or more characters), or a path that
superfind search prints first.

search --json and ask --json print JSON Lines: one object per session, one per line, best
first. Not an array: jq -r .id prints every id, --limit 1 keeps the first, and jq -s makes
an array.

Exit codes: 0 done, even when nothing matches; 1 failed, or the picker was cancelled;
2 a usage mistake, or an id that matches more than one session; 130 interrupted.
`

var helps = map[string]string{
	"init": `superfind init zsh|bash|fish

Print the shell setup that lets superfind change your shell's folder when you resume a session.
Add one line to your shell's startup file:

  eval "$(superfind init zsh)"     # in ~/.zshrc
  eval "$(superfind init bash)"    # in ~/.bashrc, or ~/.bash_profile on macOS
  superfind init fish | source     # in ~/.config/fish/config.fish

Then superfind with no arguments, sfd, or Ctrl-X c opens the picker. Choosing a session cds
into its folder and runs its agent's resume command, which lands in your shell history
(in fish, from version 4.0). fish needs version 3.1 or later.
`,
	"pick": `superfind pick

Open the picker full screen. When you choose a session, print its title on the first line and
the command that resumes it on the second, then exit 0. Exit 1 if you cancel.
When an agent already has the session open in a tmux pane and you're in tmux, the second line
is tmux switch-client to that pane instead.
The shell setup from superfind init runs that command for you.

Type filters in the search box to narrow the list, such as in:here, branch:main,
after:yesterday, or agent:codex; superfind help search lists them all.
`,
	"last": `superfind last [FILTERS] [--json]

Resume the newest session started in this folder or in a folder that holds it, with no keys to
press. Sessions started in a folder inside this one don't count. Your home folder and / count
only for sessions started right there.
Very short sessions count only when nothing else fits, and sessions another program started
are left out unless you add is:agent.

With the shell setup from superfind init, when its input and output are a terminal and no
option starting with - is given, superfind last cds into the session's folder and resumes it,
as choosing it in the picker does. Otherwise, such as in a pipe, a script, or an agent's shell,
it prints the session's title on the first line and the command that resumes it on the second,
as superfind pick does. Exit 1 when no session fits, or when an agent already has the newest
session open: superfind last says where it is open instead of starting a second copy. An agent: or is: value superfind doesn't
know, such as agent:codx, is a usage mistake (exit 2) instead of resuming any session.

  agent:codex     only Codex sessions; every search filter works, such as branch:main or
                  after:yesterday
  --json          print the session as one JSON object, as search --json does
`,
	"search": `superfind search WORDS [--json] [--limit N]

Print the sessions whose title, project, branch, or conversation contains every word, titles
that match first. With no words, print every session: the ones from this folder or a folder
that holds it first, then newest first, and very short ones last. Sessions that another
program started, such as codex exec, claude -p, or another Claude Code session, are left out
unless you ask for them with is:agent.

  is:waiting      only sessions waiting on you; is:unfinished works the same way
  is:open         only sessions an agent has open right now
  is:agent        only sessions another program started
  agent:codex     only Codex sessions; agent:claude and agent:grok work the same way
  in:here         only sessions from this folder or a folder that holds it, not from folders
                  inside it; your home folder and / count only for sessions started right there
  branch:main     only sessions that ended on that branch, in any letter case; several
                  branch: filters mean any of them
  after:DAY       only sessions last active on or after that day, in your time zone; DAY is a
                  date like 2026-10-09, or today, yesterday, or a time ago like 12h, 3d, or 2w
  before:DAY      only sessions last active before that day; after:2026-10-09
                  before:2026-10-10 is October 9
  --json          print one JSON object per session, each on its own line (JSON Lines, not
                  an array), including the ones another program started, each with its kind
                  and the passage that matched
  --limit N       print only the first N sessions (default: all)

Two branch: or agent: filters, or is:waiting with is:unfinished, keep sessions that fit either;
different filters must all fit. An is: or agent: value superfind doesn't know is ignored.

Each line has six tab-separated fields: the session, its folder, its id, its agent's home,
its title, and the line the picker shows. To search for a word that starts with -, put --
before it. A filter superfind can't read, such as after:3days, is a usage mistake (exit 2).

  superfind search --json --limit 1 retry | jq -r .id   the best match's id
  superfind search --json retry | jq -s 'map(.title)'   every match, as one array
  superfind search --json retry | wc -l                 how many match (jq length counts
                                                        each object's fields instead)
`,
	"ask": `superfind ask WORDS [--json] [--limit N]

Print the sessions that best fit what you describe, even when they used other words, best
first. Ranked by Jev with a TypeSafe key, or by Claude Haiku through your Claude Code login
without one. Takes the same filters as search, such as agent:codex or in:here, and prints
the same fields.
With --json, it prints one JSON object per line, as search --json does. --limit N prints
only the first N sessions. Its match is a passage with some of your words, not counting
common ones like the, when the conversation doesn't hold them all. It shows where a session
touches your words, not how well it fits.
`,
	"preview": `superfind preview SESSION [--width N] [--recent] [--query Q]

Print where a session left off: your last real request and the end of the reply.
SESSION is a session id, a unique start of one (8 or more characters), or a path that
superfind search prints first.

  --width N       wrap to N columns (default 80)
  --recent        print the recent conversation instead
  --query Q       show the passage that matches Q under the title
`,
	"command": `superfind command SESSION

Print the shell command that resumes a session: cd into its folder, or the nearest parent
that still exists, then run its agent's resume command.
SESSION is a session id, a unique start of one (8 or more characters), or a path that
superfind search prints first.
`,
	"show": `superfind show SESSION [--json] [--tail N] [--max-bytes N]

Print a session's conversation for scripts and agents: what you asked and what the agent
answered, without color or wrapping. Tool calls, tool output, and system text are left out,
messages in a row from one speaker are joined into one turn, and lines that can't be read are
skipped. Common keys, tokens, and passwords are replaced with [masked …]. Nothing is sent
anywhere, but an agent that reads this passes it to its own model provider.

SESSION is a session id, a unique start of one (8 or more characters), or a path that
superfind search prints first.

  --json          print one JSON object: the session's fields and its turns
  --tail N        print only the last N turns
  --max-bytes N   keep the most recent turns that fit in N bytes of text (default 60000;
                  0 for no limit). The output says how many earlier turns it left out.
`,
	"handoff": `superfind handoff SESSION [WORDS] [--json] [--max-bytes N]

Print a handoff that lets another agent continue a session's work without resuming it. It is
Markdown to paste into any agent, or JSON with --json. It gives the session's folder, branch,
and resume command, how the session ended, and excerpts of the conversation. The excerpts are
the first request, the last request and what followed, and the turns that contain any of WORDS
with the turns on each side. Each excerpt keeps its turn number and speaker, and the handoff
says which turns it left out. The excerpts are quoted, under a note that they are a record and
not instructions. Keys, tokens, and passwords are replaced with [masked …], as in show.
Nothing is sent anywhere.

SESSION is a session id, a unique start of one (8 or more characters), or a path that
superfind search prints first.

  --json          print one JSON object: the session's fields and the excerpts
  --max-bytes N   the most text the excerpts may hold, plus a few bytes for the … marks
                  (default 12000; 0 for no limit, which prints every turn whole). Long turns
                  are shortened around what matters.
`,
	"blame": `superfind blame FILE|FILE:LINE|COMMIT [--json]

Print the sessions on this machine that made a commit, or that changed a file, from any agent,
best first, in the same fields as search. Each line's summary starts with what the session did,
such as made this commit, made a commit it squashed, opened #19, or edited it 3 times.
To resume one, run the command that superfind command prints for its id. Run blame inside the
git repository, or pass a file in one.

  COMMIT       a commit, such as a hash, HEAD~2, or a branch. Lists the session that ran its
               git commit, then any that opened its pull request, named by a (#N) suffix or a
               "Merge pull request #N" subject. A squash merge lists the sessions that made
               the commits it squashed. When no session left a record of either, it says so,
               then lists the sessions that edited its files in the three days before it.
  FILE         lists the sessions that edited the file, or made or opened the pull request of
               one of its last 30 commits, most recent first
  FILE:LINE    blames the commit that last changed that line. A file whose name ends in
               :LINE is read as that file.
  --json       print one JSON object per session, as search --json does, with what the
               session did in match

A commit or edit that a Claude Code or Codex subagent made counts for the session that
started it. It reads the commands and edits each agent recorded, so it misses a commit made by
hand in a terminal or on another machine, and one whose message and hash the session never
recorded. A message counts when it matches, ignoring case and punctuation, has at least 12
letters and digits, and the session ran it from a folder in the repository or one that no
longer exists. Grok records no times, so its edits count as recent whenever its session is.
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

var commandNames = []string{"init", "pick", "last", "search", "ask", "preview", "show", "handoff", "command", "blame", "index", "label"}

const zshSetup = `_superfind_run() {
  if [[ -x %[1]s ]]; then
    %[1]s "$@"
  else
    command superfind "$@"
  fi
}

_superfind_resume() {
  print -r -- $'Resuming \e[1m'"${1%%%%$'\n'*}"$'\e[0m'
  print -s -- "${1#*$'\n'}"
  eval "${1#*$'\n'}"
}

superfind() {
  local out arg
  if [[ ${1-} == last && -t 0 && -t 1 ]]; then
    for arg in "${@:2}"; do
      [[ $arg == -* ]] && { _superfind_run "$@"; return; }
    done
  elif (( $# )); then
    _superfind_run "$@"
    return
  fi
  out=$(_superfind_run "${@:-pick}") || return
  _superfind_resume "$out"
}

sfd() { superfind "$@"; }

_superfind_widget() {
  local out
  if out=$(_superfind_run pick); then
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

const bashSetup = `_superfind_run() {
  if [[ -x %[1]s ]]; then
    %[1]s "$@"
  else
    command superfind "$@"
  fi
}

_superfind_resume() {
  printf 'Resuming \e[1m%%s\e[0m\n' "${1%%%%$'\n'*}"
  history -s "${1#*$'\n'}"
  eval "${1#*$'\n'}"
}

superfind() {
  local out arg
  if [[ ${1-} == last && -t 0 && -t 1 ]]; then
    for arg in "${@:2}"; do
      [[ $arg == -* ]] && { _superfind_run "$@"; return; }
    done
  elif (( $# )); then
    _superfind_run "$@"
    return
  fi
  out=$(_superfind_run "${@:-pick}") || return
  _superfind_resume "$out"
}

sfd() { superfind "$@"; }

_superfind_widget() {
  local out
  out=$(_superfind_run pick) && _superfind_resume "$out"
}
[[ $- == *i* ]] && bind -x '"\C-xc": _superfind_widget'
`

const fishSetup = `function _superfind_run
    if test -x %[1]s
        %[1]s $argv
    else
        command superfind $argv
    end
end

function _superfind_resume
    set -l resume (string join \n -- $argv[2..-1] | string collect)
    printf 'Resuming \e[1m%%s\e[0m\n' $argv[1]
    if test (string split . -- $version)[1] -ge 4
        history append -- $resume
    end
    eval $resume
end

function superfind
    set -l rest $argv
    set -e rest[1]
    if test "$argv[1]" = last; and test -t 0; and test -t 1; and not string match -q -- '-*' $rest
        set -l out (_superfind_run $argv); or return
        _superfind_resume $out
        return
    end
    if set -q argv[1]
        _superfind_run $argv
        return
    end
    set -l out (_superfind_run pick); or return
    _superfind_resume $out
end

function sfd
    superfind $argv
end

function _superfind_widget
    set -l out (_superfind_run pick)
    and _superfind_resume $out
    commandline -f repaint
end

if status is-interactive
    bind \cxc _superfind_widget
    bind -M insert \cxc _superfind_widget
end
`

var shellSetups = map[string]string{"zsh": zshSetup, "bash": bashSetup, "fish": fishSetup}

var stdout, stderr io.Writer = os.Stdout, os.Stderr

func printRows(rows []session.Row, shown render.Tags, evidence ...string) {
	secondary := render.SecondaryFromEnv()
	for i, r := range rows {
		t := render.RowTextOf(r, shown)
		if i < len(evidence) {
			t.Context = append([]string{field(evidence[i])}, t.Context...)
		}
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

func handoff(r session.Row) string {
	if r.Open != nil && r.Open.Tmux != nil && os.Getenv("TMUX") != "" {
		pane := r.Open.Tmux
		return render.Flat(r.Session.Title) + render.Reset + render.Dim + fmt.Sprintf(" · in tmux session %s, window %d", render.Flat(pane.Session), pane.Window) +
			"\ntmux switch-client -t " + agent.ShellQuote(pane.ID) + "\n"
	}
	return handoffTitle(r) + "\n" + agent.ResumeCommand(r.Agent, r.Home, r.Session.Cwd, r.Session.ID) + "\n"
}

func runLast(args []string) {
	text, asJSON, _ := searchWords("last", args)
	q := mustParse("last", text)
	if q.Unknown != nil {
		usageError("superfind last: " + q.Unknown.Error())
	}
	if q.Words != "" {
		usageError("superfind last: takes filters, such as agent:codex or branch:main, not words. To search this folder, use superfind search in:here WORDS.")
	}
	r, ok := session.Last(q, annotated("last", session.Index))
	switch {
	case !ok && text == "":
		fail("superfind last: no session started in this folder or a folder that holds it.")
	case !ok:
		fail("superfind last: no session that fits these filters started in this folder or a folder that holds it.")
	case asJSON:
		writeRecords([]session.Row{r}, nil, "", false)
	case r.Open != nil:
		fail("superfind last: " + render.Flat(r.Session.Title) + " is already open " + openWhere(*r.Open) +
			". superfind last doesn't start a second copy.")
	default:
		fmt.Fprint(stdout, handoff(r))
	}
}

func openWhere(o open.State) string {
	if o.Tmux != nil {
		return fmt.Sprintf("in tmux session %s, window %d", render.Flat(o.Tmux.Session), o.Tmux.Window)
	}
	return "in another window"
}

func handoffTitle(r session.Row) string {
	title := render.Flat(r.Session.Title)
	switch {
	case r.Open != nil:
		title += render.Reset + render.Dim + " · already open in another window"
	case r.Session.Active():
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

func annotated(command string, refresh func(func(int)) ([]session.Row, []error, error)) []session.Row {
	held := make(chan map[open.Key]open.State, 1)
	go func() { held <- open.Find(agent.Places()) }()
	rows := mustRefresh(command, refresh)
	return session.Annotate(rows, <-held)
}

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
	var readOnly session.ReadOnly
	if errors.As(err, &readOnly) {
		switch {
		case command == "index" || command == "label":
			fail("superfind " + command + ": " + session.CacheDir() + " can't be written from here, so nothing was saved")
		case (command == "search" || command == "ask") && readOnly.Changed > 0:
			fmt.Fprintln(stderr, "superfind "+command+": "+readOnly.Error())
		}
		err = nil
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
	rows = annotated(command, session.Index)
	for _, want := range located {
		for _, r := range rows {
			if r.Path == want.Path {
				return r, true, rows
			}
		}
	}
	return located[len(located)-1], false, rows
}

func findSession(command, arg string) (session.Row, bool, []session.Row) {
	if strings.Contains(arg, "/") || strings.HasPrefix(arg, ".") || strings.HasPrefix(arg, "~") {
		return sessionAt(command, arg)
	}
	rows := annotated(command, session.Index)
	matches, exact := session.ByID(rows, arg)
	if len(matches) == 0 {
		if strings.HasSuffix(arg, ".jsonl") {
			return sessionAt(command, arg)
		}
	}
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
	if exact || !slices.ContainsFunc(matches, func(r session.Row) bool { return r.Session.ID != matches[0].Session.ID }) {
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
	asJSON, target, rest := false, "", false
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case rest && target == "":
			target = arg
		case arg == "--" && !rest:
			rest = true
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
	conversation, err := show.Read(r.Agent, r.Path, opt)
	if err != nil {
		fail("superfind show: couldn't read " + field(r.Path) + ": " + err.Error())
	}
	record := session.RecordOf(r, show.Clean)
	if asJSON {
		err = show.WriteJSON(stdout, record, conversation)
	} else {
		err = show.WriteText(stdout, record, conversation)
	}
	if err != nil {
		fail("superfind show: " + err.Error())
	}
}

func runHandoff(args []string) {
	opt := briefing.Options{MaxBytes: briefing.DefaultMaxBytes}
	asJSON, target, rest := false, "", false
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case rest && target == "":
			target = arg
		case rest:
			opt.Words = append(opt.Words, arg)
		case arg == "--":
			rest = true
		case arg == "--json":
			asJSON = true
		case arg == "--max-bytes":
			if i+1 == len(args) {
				usageError("superfind handoff: --max-bytes needs a number. See superfind help handoff.")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				usageError("superfind handoff: --max-bytes needs a whole number, like --max-bytes 12000. See superfind help handoff.")
			}
			opt.MaxBytes = n
		case strings.HasPrefix(arg, "--"):
			usageError("superfind handoff: there is no " + arg + " option. To look for it, put -- first: superfind handoff SESSION -- " + arg)
		case target == "":
			target = arg
		default:
			opt.Words = append(opt.Words, arg)
		}
	}
	if target == "" {
		usageError("superfind handoff: which session? Pass its id, or a path that superfind search prints first.")
	}
	r, indexed, _ := findSession("handoff", target)
	if !indexed {
		fail("superfind handoff: not an indexed session: " + target)
	}
	t, err := agent.ReadTranscript(r.Agent, r.Path)
	if err != nil {
		fail("superfind handoff: couldn't read " + field(r.Path) + ": " + err.Error())
	}
	record := session.RecordOf(r, show.Clean)
	packet := briefing.Build(t, opt)
	if asJSON {
		err = briefing.WriteJSON(stdout, r.Agent, record, packet)
	} else {
		err = briefing.WriteMarkdown(stdout, r.Agent, record, packet)
	}
	if err != nil {
		fail("superfind handoff: " + err.Error())
	}
}

func runBlame(args []string) {
	asJSON, target, rest := false, "", false
	for _, arg := range args {
		switch {
		case rest && target == "":
			target = arg
		case arg == "--" && !rest:
			rest = true
		case arg == "--json":
			asJSON = true
		case strings.HasPrefix(arg, "-"):
			usageError("superfind blame: there is no " + arg + " option. See superfind help blame.")
		case target != "":
			usageError("superfind blame: one file or commit at a time, not " + arg + " too.")
		default:
			target = arg
		}
	}
	if target == "" {
		usageError("superfind blame: which file or commit? See superfind help blame.")
	}
	t, err := blame.Resolve(target)
	if err != nil {
		fail("superfind blame: " + err.Error())
	}
	if t.Note != "" {
		fmt.Fprintln(stderr, "superfind blame: "+t.Note)
	}
	rows := annotated("blame", session.Index)
	found, problems := blame.Blame(t, rows)
	if unreadable := session.Unreadable(problems); unreadable != "" {
		fmt.Fprintln(stderr, "superfind blame: "+unreadable)
	}
	switch {
	case len(found) == 0 && t.File != "":
		fmt.Fprintln(stderr, "superfind blame: no session on this machine edited "+field(target)+" or made one of its last 30 commits")
	case len(found) == 0:
		fmt.Fprintln(stderr, "superfind blame: no session on this machine made "+field(target)+" or edited its files in the three days before it")
	case t.File == "" && found[0].Guess:
		fmt.Fprintln(stderr, "superfind blame: no session left a record of making "+field(target)+"; these edited its files in the three days before it")
	}
	if !asJSON {
		shown, evidence := make([]session.Row, len(found)), make([]string, len(found))
		for i, f := range found {
			shown[i], evidence[i] = f.Row, f.Evidence
		}
		printRows(shown, render.TagsFor(rows), evidence...)
		return
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	for _, f := range found {
		record := session.RecordOf(f.Row, show.Clean)
		record.Match = show.Clean(f.Evidence)
		if err := enc.Encode(record); err != nil {
			fail("superfind: couldn't write the output: " + err.Error())
		}
	}
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

func writeRecords(rows []session.Row, db *sql.DB, query string, byMeaning bool) {
	if err := show.WriteRecords(stdout, rows, db, query, byMeaning); err != nil {
		fail("superfind: couldn't write the output: " + err.Error())
	}
}

func mustParse(command, text string) session.Query {
	q := session.Parse(text)
	if q.Problem != nil {
		usageError("superfind " + command + ": " + q.Problem.Error())
	}
	return q
}

func searchWords(command string, args []string) (string, bool, int) {
	var words []string
	asJSON, limit := false, 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return strings.Join(append(words, args[i+1:]...), " "), asJSON, limit
		case arg == "--json":
			asJSON = true
			continue
		case (arg == "--limit" || strings.HasPrefix(arg, "--limit=")) && command != "last":
			value, inline := strings.CutPrefix(arg, "--limit=")
			if !inline {
				i++
				value = ""
				if i < len(args) {
					value = args[i]
				}
			}
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				usageError("superfind " + command + ": --limit needs a number of sessions, like --limit 5.")
			}
			limit = n
			continue
		case arg == "--deep" && command == "search":
			usageError("superfind search: --deep is now its own command: superfind ask WORDS")
		case strings.HasPrefix(arg, "-") && arg != "-" && command == "last":
			usageError("superfind last: there is no " + arg + " option. See superfind help last.")
		case strings.HasPrefix(arg, "-") && arg != "-":
			usageError("superfind " + command + ": there is no " + arg + " option. To search for it, put -- first: superfind " +
				command + " -- " + arg)
		}
		words = append(words, arg)
	}
	return strings.Join(words, " "), asJSON, limit
}

func atMost(rows []session.Row, limit int) []session.Row {
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
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
			usageError("superfind init: which shell? superfind init zsh, superfind init bash, or superfind init fish")
		}
		fmt.Fprintf(stdout, setup, agent.ShellQuote(selfPath()))
	case "index":
		n, noun := len(mustIndex("index")), "sessions"
		if n == 1 {
			noun = "session"
		}
		fmt.Fprintf(stdout, "%d %s indexed\n", n, noun)
	case "search":
		query, asJSON, limit := searchWords("search", args[1:])
		q := mustParse("search", query)
		find := session.Search
		if asJSON {
			find = session.SearchEvery
		}
		rows := annotated("search", session.Index)
		db := mustOpenSearch()
		found, err := find(db, query, rows)
		if errors.Is(err, session.ErrDamaged) {
			rows = annotated("search", session.Rebuild)
			db = mustOpenSearch()
			found, err = find(db, query, rows)
		}
		if err != nil {
			fail("superfind search: " + err.Error())
		}
		found = atMost(found, limit)
		if asJSON {
			writeRecords(found, db, query, false)
			return
		}
		printRows(found, render.TagsFor(session.SameKind(rows, q.Agents)))
	case "ask":
		words, asJSON, limit := searchWords("ask", args[1:])
		q := mustParse("ask", words)
		rows := annotated("ask", session.Index)
		shown := render.TagsFor(session.SameKind(rows, q.Agents))
		db := mustOpenSearch()
		query, eligible := q.Words, q.Rows(rows, asJSON)
		if query == "" && asJSON {
			writeRecords(atMost(session.RecentFirst(eligible), limit), db, "", false)
			return
		}
		if query == "" {
			printRows(atMost(session.RecentFirst(eligible), limit), shown)
			return
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		paths, err := ask.Ask(ctx, db, query, eligible)
		interrupted := ctx.Err() != nil
		stop()
		if interrupted {
			exit(130)
		}
		var failure ask.Failure
		if errors.As(err, &failure) {
			fail(failure.Error())
		}
		ranked := atMost(session.InOrder(rows, paths), limit)
		if asJSON {
			writeRecords(ranked, db, query, true)
			return
		}
		printRows(ranked, shown)
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
			usageError("superfind preview: which session? Pass its id, or a path that superfind search prints first.")
		}
		r, indexed, rows := findSession("preview", target)
		req.Agent, req.Path = r.Agent, r.Path
		var header []string
		if indexed {
			header = append(render.PreviewHeader(req, r, render.TagsFor(session.SameKind(rows, r.Session.ByAgent)), mustOpenSearch()), "")
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
		r, indexed, _ := findSession("command", args[1])
		if !indexed {
			fail("superfind command: not an indexed session: " + args[1])
		}
		fmt.Fprintln(stdout, agent.ResumeCommand(r.Agent, r.Home, r.Session.Cwd, r.Session.ID))
	case "show":
		runShow(args[1:])
	case "handoff":
		runHandoff(args[1:])
	case "blame":
		runBlame(args[1:])
	case "pick":
		chosen, err := tui.Pick()
		if err != nil {
			fail(err.Error())
		}
		if chosen == nil {
			exit(1)
			return
		}
		fmt.Fprint(stdout, handoff(*chosen))
	case "last":
		runLast(args[1:])
	default:
		unknownCommand(args[0])
	}
}

func asksForHelp(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			return false
		case "--query", "--width", "--limit":
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
