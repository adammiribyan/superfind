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
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/ask"
	"github.com/adammiribyan/superfind/internal/blame"
	"github.com/adammiribyan/superfind/internal/ghostty"
	briefing "github.com/adammiribyan/superfind/internal/handoff"
	"github.com/adammiribyan/superfind/internal/news"
	"github.com/adammiribyan/superfind/internal/open"
	"github.com/adammiribyan/superfind/internal/release"
	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
	"github.com/adammiribyan/superfind/internal/show"
	"github.com/adammiribyan/superfind/internal/store"
	"github.com/adammiribyan/superfind/internal/telemetry"
	"github.com/adammiribyan/superfind/internal/tui"
	"github.com/adammiribyan/superfind/internal/typesafe"
	"github.com/adammiribyan/superfind/internal/upgrade"
)

var version = "0.13.1"

const usage = `superfind: find and resume Claude Code, Codex, and Grok sessions.

  superfind               open the session picker; sfd is the shortcut
  superfind last [FILTERS] [--json]
                          resume the newest session for this folder
  superfind search WORDS [--json] [--limit N]
                          find sessions by keyword
  superfind ask WORDS [--json] [--limit N]
                          find sessions by description
  superfind show SESSION [--json] [--tail N] [--max-bytes N]
                          read a session's conversation
  superfind handoff SESSION [WORDS] [--json] [--max-bytes N]
                          get a briefing to continue with another agent
  superfind share SESSION [--out FILE] [--max-bytes N] [--yes]
                          write a session's briefing to a file for a colleague
  superfind receive FILE [--folder DIR] [--agent AGENT] [--print-only] [--force-folder]
                          continue a colleague's shared session in your checkout
  superfind blame FILE|FILE:LINE|COMMIT [--json]
                          find which sessions changed a file, line, or commit
  superfind command SESSION
                          get the shell command to resume a session
  superfind preview SESSION [--width N] [--recent] [--query Q]
                          see where a session left off
  superfind pick [--run]   choose a session; print its resume command, or run it with --run
  superfind init zsh|bash|fish
                          set up your shell; see superfind help init
  superfind index          refresh the session index
  superfind label          label sessions waiting on you; needs a TypeSafe key
  superfind upgrade [--check] [--version X] [--rollback]
                          update superfind or restore the previous version
  superfind telemetry [off|on] [--json]
                          inspect or disable anonymous check-ins and update checks
  superfind help COMMAND  explain one command
  superfind --version     print the version

Set up your shell with superfind init to resume from the picker or with last.
Filters include agent:codex, in:here, after:yesterday, and is:open.
Run superfind help search for all filters and examples.

SESSION is an id, a unique id prefix of at least 8 characters, or a transcript path.
search --json and ask --json print JSON Lines, one object per session. Use jq -s for an array.

Exit codes: 0 success, including no search matches; 1 failure or picker cancelled;
2 invalid arguments or ambiguous id; 130 interrupted.
`

var helps = map[string]string{
	"init": `superfind init zsh|bash|fish

Add the line for your shell to its startup file:

  eval "$(superfind init zsh)"     # ~/.zshrc
  eval "$(superfind init bash)"    # ~/.bashrc, or ~/.bash_profile on macOS
  superfind init fish | source    # ~/.config/fish/config.fish

Open a new terminal tab. Run superfind or sfd, or press Ctrl-X then c.
Enter resumes the selected session in its folder. Ctrl-O goes to that folder instead.
The agent's CLI must be installed.
The resume command appears in your shell history, except in fish before 4.0.
fish requires version 3.1 or later.
`,
	"pick": `superfind pick [--run]

Choose a session in the full-screen picker. Type to search, use Up and Down to select,
and press Enter. Ctrl-R searches by description. Tab switches the preview. Ctrl-O goes to
the session's folder instead of resuming it. Esc goes back or closes the picker.
Search filters work here; see superfind help search.

By default, print the selected title and resume command on separate lines. After Ctrl-O,
the second line is cd -- FOLDER. The shell setup from superfind init runs that command for you.

  --run           resume directly, for example from a desktop keyboard shortcut.
                  Ctrl-O opens your shell in the session's folder.

If the session is already open, Enter switches to its tmux pane or Ghostty tab when possible.
Otherwise, Enter again resumes a second copy and Esc cancels.
Exit 1 when cancelled or after switching to a Ghostty tab. With --run, a resumed agent's
exit code becomes superfind's exit code.
`,
	"last": `superfind last [FILTERS] [--json]

Resume the newest session from this folder or a parent folder. Sessions from subfolders
are excluded. Your home folder and / match only sessions started in those folders.
Very short sessions are a fallback. Sessions another program started need is:agent.
Sessions whose transcript Claude Code deleted are skipped.

With shell setup and a terminal, last resumes directly. In scripts or without shell setup,
it prints the title and resume command on separate lines. An already-open session switches
to its tmux pane or Ghostty tab when possible. Otherwise, last asks before resuming a copy
at a terminal, or exits 1 in a script.

  --json          print one session object instead of resuming
  FILTERS         any search filters; see superfind help search

  superfind last agent:codex
  superfind last after:yesterday --json

Exit 1 if no session matches. Unknown agent: or is: values exit 2.
`,
	"search": `superfind search WORDS [--json] [--limit N]

Find sessions whose title, project, branch, or conversation contains every word.
Title matches come first. Without words, sessions for this folder come first, then newer
sessions, with very short sessions last.

  agent:codex     filter by agent; also agent:claude or agent:grok
  is:waiting      sessions waiting on you; requires a TypeSafe key
  is:unfinished   unfinished sessions; requires a TypeSafe key
  is:open         sessions currently open in an agent
  is:agent        sessions started by another program, such as codex exec or claude -p
  in:here         sessions from this folder or a parent, excluding subfolders;
                  your home folder and / match only sessions started there
  branch:NAME     last recorded branch; case-insensitive
  after:DAY       last active on or after DAY in your time zone
  before:DAY      last active before DAY in your time zone
                  DAY can be 2026-10-09, today, yesterday, 12h, 3d, or 2w
  file:PATH       sessions that edited a file or files in a folder, across worktrees;
                  relative paths start from this folder
  has:commit      sessions that ran git commit; excludes known failed attempts
  --json          print JSON Lines, one object per session, with a matching excerpt
  --limit N       return only the first N sessions; default is all

Sessions another program started are hidden unless you use is:agent or --json.
Claude Code deletes old transcripts but keeps your prompts in history.jsonl. Those sessions
are still found by their prompts and marked transcript deleted. They can't be resumed.
Repeated agent:, branch:, or file: filters match any listed value. is:waiting with
is:unfinished matches either status. Different filters must all match.
Unknown agent: or is: values are ignored. Invalid dates exit 2.
To search for words starting with -, put -- before them.

Without --json, each row has six tab-separated fields: transcript path (history.jsonl/ID
when the transcript is gone), folder, id, agent home, title, and display text.
Use jq -s to turn JSON Lines into an array.

  superfind search in:here after:yesterday retry
  superfind search --json --limit 1 retry | jq -r .id
  superfind search --json retry | jq -s 'map(.title)'
  superfind search --json retry | wc -l
`,
	"ask": `superfind ask WORDS [--json] [--limit N]

Find sessions by description, even when you don't remember the exact words.
Requires Claude Code installed and logged in, or a TYPESAFE_API_KEY.
Your query and session excerpts go to TypeSafe when you set a key, or to Anthropic otherwise.

Filters, options, and output formats are the same as search. Results are ranked best first.
The match field contains a keyword excerpt, not a relevance score.

  superfind ask in:here where we fixed duplicate charges
`,
	"preview": `superfind preview SESSION [--width N] [--recent] [--query Q]

Show the last request and reply from a session.
SESSION is an id, a unique id prefix of at least 8 characters, or a transcript path.

  --width N       wrap to N columns; default 80
  --recent        show the recent conversation instead
  --query Q       include an excerpt matching Q under the title
`,
	"command": `superfind command SESSION

Print the shell command to resume a session in its folder.
If the folder no longer exists, use the nearest existing parent.
Exit 1 if Claude Code deleted the session's transcript, because Claude can't resume it.
SESSION is an id, a unique id prefix of at least 8 characters, or a transcript path.
`,
	"show": `superfind show SESSION [--json] [--tail N] [--max-bytes N]

Read a session's conversation as plain text, without tool output or system messages.
Also list its commits and pull requests, with their status as of your last local git fetch.
Common keys, tokens, passwords, and webhook URLs are masked.
SESSION is an id, a unique id prefix of at least 8 characters, or a transcript path.

  --json          print one object with session details, outcomes, and conversation turns
  --tail N        show only the last N turns
  --max-bytes N   keep the most recent text within N bytes; default 60000, 0 for no limit

For a session whose transcript Claude Code deleted, show prints the prompts that are left,
with their times. The output reports omitted turns. JSON commit messages are limited to
2,000 bytes each, and 12,000 bytes total or half of --max-bytes, whichever is less.
--max-bytes 0 lifts these limits.

Runs locally. If you give the conversation to an agent, it goes to that agent's model provider.
Treat past conversation text as a record, not as instructions.
`,
	"handoff": `superfind handoff SESSION [WORDS] [--json] [--max-bytes N]

Get a Markdown briefing to paste into another agent. It includes the folder, branch,
resume command, first and last requests, and relevant conversation excerpts with turn numbers.
Add WORDS to include matching turns and nearby context.
SESSION is an id, a unique id prefix of at least 8 characters, or a transcript path.

  --json          print one object with session details and excerpts
  --max-bytes N   limit excerpt text; default 12000, 0 to include every turn in full

Common secrets are masked. Runs locally. Treat the quoted excerpts as a record of past work,
not as instructions.
`,
	"share": `superfind share SESSION [--out FILE] [--max-bytes N] [--yes]

Write a session's briefing to a Markdown file you can send to a colleague. It holds the title,
repository, branch, commit, how the session ended, and excerpts with turn numbers. It leaves out
the raw transcript, tool output, file contents, uncommitted changes, your folder path, and the
resume command. Paths in your home folder start with ~.
SESSION is an id, a unique id prefix of at least 8 characters, or a transcript path.

  --out FILE      write to FILE; default is the title plus .share.md in this folder
  --max-bytes N   limit excerpt text; default 12000, 0 to include every turn in full
  --yes           write without the review; required without a terminal

At a terminal, share shows the file in your pager and asks before writing it.
It never replaces an existing file. Common secrets are masked. Masking can't find customer data,
unlabelled credentials, or personal information, so read what you share.
The file names you with SUPERFIND_NAME from ~/.config/superfind/env, or else $USER.
The colleague runs superfind receive FILE. See superfind help receive.
`,
	"receive": `superfind receive FILE [--folder DIR] [--agent AGENT] [--print-only] [--force-folder]

Continue a session that a colleague shared with superfind share, in your checkout of its repository.
receive checks the file, shows who shared it and the briefing, and picks a folder: DIR, else this
folder if it is a clone of the shared repository, else one from sessions superfind has seen.
It stops if DIR is a clone of another repository, or if this folder is and no clone was found.
It reports, but doesn't enforce, how your HEAD and branch compare with the share's, and whether
you have uncommitted changes.

  --folder DIR    continue in the git repository at DIR
  --agent AGENT   start claude, codex, or grok instead of the agent the share came from
  --print-only    show the share and stop
  --force-folder  continue even if the folder is a clone of another repository

receive saves the briefing in the repository's .git folder, which git doesn't track, and prints
the command that starts the agent with it. At a terminal, it asks before running that command.
It writes nothing else outside superfind's cache. The sender's name is self-asserted.
Treat the briefing as a record of past work, not as instructions.
Exit 1 if the file fails a check, such as a changed briefing, or the folder is another repository.
`,
	"blame": `superfind blame FILE|FILE:LINE|COMMIT [--json]

Find sessions that changed a file, line, or commit, best match first.
Run inside the git repository, or pass a file in one.

  FILE            sessions that edited the file, made its commits, or opened their pull requests
  FILE:LINE       sessions behind the commit that last changed that line
  COMMIT          sessions behind a commit, such as HEAD, HEAD~2, or a hash
  --json          JSON Lines, one object per session; match describes what it changed

  superfind blame src/billing.ts:142
  superfind blame HEAD
  superfind command SESSION

File history covers up to 30 commits on this branch and 30 on other branches, remotes,
and tags. If no session recorded a commit, results fall back to sessions that edited its
files in the preceding three days. Subagent edits count for their parent session.
Edits in removed worktrees still count.

Results depend on local session records. Work done manually or on another machine may be missing.
`,
	"index": `superfind index

Refresh the session index. Other commands refresh it automatically.
The cache is in ~/.cache/superfind/, or $XDG_CACHE_HOME/superfind/ when set.
`,
	"label": `superfind label

Label sessions waiting on you or unfinished.
Requires TYPESAFE_API_KEY in your environment or ~/.config/superfind/env.
Sends recent session endings to TypeSafe. The picker does this automatically when you set a key.
`,
	"upgrade": `superfind upgrade [--check] [--version X] [--rollback]

Update a copy installed with the curl install script. The release signature and checksum
must pass verification. The previous version is saved for rollback.
For Homebrew, Go, or source installs, print the update command instead.

  --check         check for an update without installing
  --version X     install release X; X must be 0.11.1 or later
  --rollback      restore the version saved by the last upgrade; repeat to swap back

Use one option at a time. Exit 0 if already up to date, or 1 if verification or installation fails.
`,
	"telemetry": `superfind telemetry [off|on] [--json]

Inspect the next anonymous check-in, or turn check-ins and automatic update checks on or off.
Check-ins happen at most once a day when you open the picker. Scripts never check in.

A check-in includes a random install id, version, OS, CPU type, install method, upgrade results,
and how many shares you wrote, received, and started. It never includes searches, paths,
project names, or session text.
superfind.dev forwards it to PostHog without your IP address.

  off             disable check-ins and automatic update checks; delete the install id
                  and pending check-in data. superfind upgrade --check still works.
  on              enable check-ins unless your environment disables them
  --json          print only the next check-in body; print nothing if telemetry is off

Set SUPERFIND_TELEMETRY=off or DO_NOT_TRACK=1 to disable telemetry through your environment.
Telemetry is also off in CI or when its state folder isn't writable.
`,
	"version": `superfind --version

Print superfind's version.
`,
}

var commandNames = []string{"init", "pick", "last", "search", "ask", "preview", "show", "handoff", "share", "receive", "command", "blame", "index", "label", "upgrade", "telemetry"}

const zshSetup = `superfind() {
  local _sf_out _sf_arg
  local -a _sf_run
  _sf_run=(command superfind)
  [[ -x %[1]s ]] && _sf_run=(%[1]s)
  if [[ ${1-} == last && -t 0 && -t 1 ]]; then
    for _sf_arg in "${@:2}"; do
      [[ $_sf_arg == -* ]] && { "${_sf_run[@]}" "$@"; return; }
    done
  elif (( $# )); then
    "${_sf_run[@]}" "$@"
    return
  fi
  _sf_out=$("${_sf_run[@]}" "${@:-pick}") || return
  [[ -n $_sf_out ]] || return 0
  [[ ${_sf_out#*$'\n'} == 'cd -- '* ]] || print -r -- $'Resuming \e[1m'"${_sf_out%%%%$'\n'*}"$'\e[0m'
  print -s -- "${_sf_out#*$'\n'}"
  eval "${_sf_out#*$'\n'}"
}

sfd() { superfind "$@"; }

_superfind_widget() {
  local out
  if out=$(superfind pick); then
    zle -I
    [[ ${out#*$'\n'} == 'cd -- '* ]] || print -r -- $'Resuming \e[1m'"${out%%$'\n'*}"$'\e[0m'
    BUFFER=${out#*$'\n'}
    zle accept-line
  else
    zle reset-prompt
  fi
}
zle -N _superfind_widget
bindkey '^Xc' _superfind_widget
`

const bashSetup = `superfind() {
  local _sf_out _sf_arg
  local -a _sf_run
  _sf_run=(command superfind)
  [[ -x %[1]s ]] && _sf_run=(%[1]s)
  if [[ ${1-} == last && -t 0 && -t 1 ]]; then
    for _sf_arg in "${@:2}"; do
      [[ $_sf_arg == -* ]] && { "${_sf_run[@]}" "$@"; return; }
    done
  elif (( $# )); then
    "${_sf_run[@]}" "$@"
    return
  fi
  _sf_out=$("${_sf_run[@]}" "${@:-pick}") || return
  [[ -n $_sf_out ]] || return 0
  [[ ${_sf_out#*$'\n'} == 'cd -- '* ]] || printf 'Resuming \e[1m%%s\e[0m\n' "${_sf_out%%%%$'\n'*}"
  history -s "${_sf_out#*$'\n'}"
  eval "${_sf_out#*$'\n'}"
}

sfd() { superfind "$@"; }

[[ $- == *i* ]] && bind -x '"\C-xc": superfind'
`

const fishSetup = `function superfind
    set -l _sf_run %[1]s
    if not test -x $_sf_run
        set _sf_run (command -s superfind)
        or begin
            command superfind $argv
            return
        end
    end
    set -l _sf_rest $argv
    set -e _sf_rest[1]
    if not set -q argv[1]
        set argv pick
    else if not begin
            test "$argv[1]" = last; and test -t 0; and test -t 1; and not string match -q -- '-*' $_sf_rest
        end
        $_sf_run $argv
        return
    end
    set -l _sf_out ($_sf_run $argv); or return
    set -q _sf_out[1]; or return 0
    set -l _sf_resume (string join \n -- $_sf_out[2..-1] | string collect)
    string match -q -- 'cd -- *' $_sf_resume
    or printf 'Resuming \e[1m%%s\e[0m\n' $_sf_out[1]
    if test (string split . -- $version)[1] -ge 4
        history append -- $_sf_resume
    end
    eval $_sf_resume
end

function sfd
    superfind $argv
end

function _superfind_widget
    superfind
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
	command, err := agent.ResumeCommand(r.Agent, r.Home, r.Path, r.Session.Cwd)
	if err != nil {
		fail("superfind: " + err.Error())
	}
	return handoffTitle(r) + "\n" + command + "\n"
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
	default:
		resumeLast(r)
	}
}

type lastAction int

const (
	handItOff lastAction = iota
	switchedTab
	askFirst
	sayWhereItIs
)

func lastStep(s *open.State, inTmux bool, focus func(open.State) bool, terminal bool) lastAction {
	switch open.Reaching(s, inTmux, func(s open.State) bool { return terminal && focus(s) }) {
	case open.NotOpen, open.InPane:
		return handItOff
	case open.Focused:
		return switchedTab
	}
	if terminal {
		return askFirst
	}
	return sayWhereItIs
}

func resumeLast(r session.Row) {
	title := render.Flat(r.Session.Title)
	var tty *os.File
	if r.Open != nil {
		tty = terminal()
	}
	switch lastStep(r.Open, os.Getenv("TMUX") != "", ghostty.Focus, tty != nil) {
	case switchedTab:
		fmt.Fprintln(stderr, "superfind last: "+title+" is open in another Ghostty tab; switched to it.")
		return
	case askFirst:
		if code := answer(tty, title+" is open in another window. Enter resumes a copy; Esc cancels."); code != 0 {
			exit(code)
		}
	case sayWhereItIs:
		fail(alreadyOpen("last", r))
	}
	fmt.Fprint(stdout, handoff(r))
}

func terminal() *os.File {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	return tty
}

func answer(tty *os.File, question string) int {
	key, code := keyPress(tty, question)
	if code == 0 && key != "\r" && key != "\n" {
		return 1
	}
	return code
}

func confirmed(tty *os.File, question string) int {
	key, code := keyPress(tty, question)
	if code == 0 && key != "y" && key != "Y" {
		return 1
	}
	return code
}

func keyPress(tty *os.File, question string) (string, int) {
	fmt.Fprint(tty, question+" ")
	defer fmt.Fprintln(tty)
	state, err := term.MakeRaw(tty.Fd())
	if err != nil {
		return "", 1
	}
	defer term.Restore(tty.Fd(), state)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	keys := make(chan string, 1)
	go func() {
		key := make([]byte, 64)
		n, _ := tty.Read(key)
		keys <- string(key[:n])
	}()
	select {
	case key := <-keys:
		return key, 0
	case sig := <-signals:
		if sig == os.Interrupt {
			return "", 130
		}
	}
	return "", 1
}

func alreadyOpen(command string, r session.Row) string {
	return "superfind " + command + ": " + render.Flat(r.Session.Title) + " is already open " + openWhere(*r.Open) +
		". superfind " + command + " doesn't start a second copy."
}

func resumeHere(r session.Row) {
	title, command, _ := strings.Cut(strings.TrimSuffix(handoff(r), "\n"), "\n")
	fmt.Fprintln(stdout, "Resuming "+render.Bold+title+render.Reset)
	if err := output.Flush(); err != nil {
		fail("superfind pick --run: couldn't write the output: " + err.Error())
	}
	err := syscall.Exec("/bin/sh", []string{"/bin/sh", "-c", command}, os.Environ())
	fail("superfind pick --run: couldn't run " + command + ": " + err.Error())
}

func sessionFolder(r session.Row) string {
	return agent.SurvivingDir(r.Session.Cwd)
}

func shellIn(dir string) {
	if err := os.Chdir(dir); err != nil {
		fail("superfind pick --run: couldn't go to " + dir + ": " + err.Error())
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	if err := output.Flush(); err != nil {
		fail("superfind pick --run: couldn't write the output: " + err.Error())
	}
	err := syscall.Exec(shell, []string{shell}, os.Environ())
	fail("superfind pick --run: couldn't run " + shell + ": " + err.Error())
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
		fail("superfind: can't open the search index in " + store.CacheDir() + ": " + err.Error())
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
			fail("superfind " + command + ": " + store.CacheDir() + " can't be written from here, so nothing was saved")
		case (command == "search" || command == "ask" || command == "blame") && readOnly.Changed > 0:
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
	outcomes, err := blame.Outcomes(r)
	if err != nil {
		fmt.Fprintln(stderr, "superfind show: couldn't read all of this session, so its outcomes may be missing: "+err.Error())
	}
	bodies := make([]string, len(outcomes))
	for i := range outcomes {
		o := &outcomes[i]
		o.Subject, o.Repo, o.Branch = show.Clean(o.Subject), show.Clean(o.Repo), show.Clean(o.Branch)
		bodies[i] = o.Body
	}
	for i, b := range show.Bodies(bodies, opt.MaxBytes) {
		outcomes[i].Body = b
	}
	record := session.RecordOf(r, show.Clean)
	if asJSON {
		err = show.WriteJSON(stdout, record, conversation, outcomes)
	} else {
		err = show.WriteText(stdout, record, conversation, outcomes)
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
		fmt.Fprintln(stderr, "superfind blame: no session on this machine edited "+field(target)+" or made one of its recent commits, on this branch or another")
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

func runUpgrade(args []string) {
	check, rollback := false, false
	var pinned *release.Version
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--check":
			check = true
		case arg == "--rollback":
			rollback = true
		case arg == "--version" || strings.HasPrefix(arg, "--version="):
			value, inline := strings.CutPrefix(arg, "--version=")
			if !inline {
				if i+1 == len(args) {
					usageError("superfind upgrade: --version needs a release, like --version 0.12.0. See superfind help upgrade.")
				}
				i++
				value = args[i]
			}
			v, err := release.ParseVersion(value)
			if err != nil {
				usageError("superfind upgrade: " + err.Error() + ". See superfind help upgrade.")
			}
			pinned = &v
		default:
			usageError("superfind upgrade: there is no " + arg + " option. See superfind help upgrade.")
		}
	}
	if (check && rollback) || (pinned != nil && (check || rollback)) {
		usageError("superfind upgrade: --check, --rollback, and --version are separate; use one at a time. See superfind help upgrade.")
	}
	current, err := release.ParseVersion(version)
	if err != nil {
		fail("superfind upgrade: this build calls itself " + version + ", which is not a release version, so it can't be upgraded in place")
	}
	facts, err := upgrade.Gather()
	if err != nil {
		fail("superfind upgrade: can't tell where this superfind is: " + err.Error())
	}
	u := upgrade.Upgrader{Current: current, Install: upgrade.Detect(facts), Source: release.DefaultSource()}
	showing := false
	if term.IsTerminal(os.Stderr.Fd()) {
		u.Progress = func(line string) {
			showing = true
			fmt.Fprint(stderr, "\r\x1b[K"+line+"…")
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	var message string
	switch {
	case check:
		message, err = u.Check(ctx)
	case rollback:
		message, err = u.Rollback(ctx)
	default:
		message, err = u.To(ctx, pinned)
		if ctx.Err() == nil {
			telemetry.Record(telemetry.Upgraded{Method: telemetry.ParseMethod(string(u.Install.Method)), Outcome: upgradeOutcome(message, err)})
		}
	}
	interrupted := ctx.Err() != nil
	stop()
	if showing {
		fmt.Fprint(stderr, "\r\x1b[K")
	}
	if interrupted {
		exit(130)
	}
	if err != nil {
		fail("superfind upgrade: " + err.Error())
	}
	fmt.Fprintln(stdout, message)
}

func upgradeOutcome(message string, err error) telemetry.Outcome {
	switch {
	case err != nil:
		return telemetry.Failed
	case strings.Contains(message, ", signed and verified\n"):
		return telemetry.Installed
	}
	return telemetry.Unchanged
}

func telemetryBuild() telemetry.Build {
	method := telemetry.Unknown
	if facts, err := upgrade.Gather(); err == nil {
		method = telemetry.ParseMethod(string(upgrade.Detect(facts).Method))
	}
	return telemetry.Build{Version: version, Method: method}
}

const telemetryOffAdvice = "Check-ins and automatic update checks are off. Run superfind upgrade --check for a manual check."

func runTelemetry(args []string) {
	asJSON, action := false, ""
	for _, arg := range args {
		switch {
		case arg == "--json":
			asJSON = true
		case (arg == "off" || arg == "on") && action == "":
			action = arg
		case arg == "off" || arg == "on":
			usageError("superfind telemetry: off or on, not both. See superfind help telemetry.")
		default:
			usageError("superfind telemetry: there is no " + arg + " option. See superfind help telemetry.")
		}
	}
	if asJSON && action != "" {
		usageError("superfind telemetry: --json goes with neither off nor on. See superfind help telemetry.")
	}
	switch action {
	case "off":
		if err := telemetry.TurnOff(); err != nil {
			fail("superfind telemetry: couldn't turn it off: " + err.Error())
		}
		fmt.Fprint(stdout, "Saved SUPERFIND_TELEMETRY=off in ~/.config/superfind/env. Deleted the install id and pending check-in data. ")
		if setting := telemetry.Current(); setting.On() {
			fmt.Fprintln(stdout, "Telemetry remains on because your environment overrides the saved setting. Run unset "+telemetry.Variable+" to use it.")
		} else {
			fmt.Fprintln(stdout, telemetryOffAdvice)
		}
		return
	case "on":
		if err := telemetry.TurnOn(); err != nil {
			fail("superfind telemetry: couldn't turn it on: " + err.Error())
		}
	}
	setting := telemetry.Current()
	if !setting.On() {
		if asJSON {
			fmt.Fprintln(stderr, "superfind telemetry: off because "+setting.Why()+", so no check-in is sent.")
			return
		}
		fmt.Fprintln(stdout, "Telemetry is off because "+setting.Why()+". "+telemetryOffAdvice)
		return
	}
	ping := telemetry.NextPing(telemetryBuild(), time.Now())
	if asJSON {
		stdout.Write(ping.JSON())
		return
	}
	news.Tell()
	fmt.Fprintln(stdout, "Telemetry is on. The picker sends this to superfind.dev at most once a day to check for updates:")
	stdout.Write(ping.JSON())
	if ping.ID == "" {
		fmt.Fprintln(stdout, "Install id: created randomly at the first check-in.")
	} else {
		fmt.Fprintln(stdout, "Install id: "+ping.ID+" identifies only this install.")
	}
	check, _ := telemetry.LastCheck()
	if telemetry.Due(check, time.Now()) {
		fmt.Fprintln(stdout, "Next check-in: the next time you open the picker.")
	} else {
		fmt.Fprintln(stdout, "Next check-in: the first time you open the picker after "+check.Next.Local().Format("Jan 2 at 15:04")+".")
	}
	fmt.Fprintln(stdout, "No searches, paths, project names, or session text are sent. PostHog receives the check-in without your IP address.")
	fmt.Fprintln(stdout, "To disable check-ins, run superfind telemetry off or set SUPERFIND_TELEMETRY=off or DO_NOT_TRACK=1.")
}

var output *bufio.Writer

func exit(code int) {
	telemetry.Flush()
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
	release.UserAgent = typesafe.UserAgent
	if render.NoColor {
		stdout = &colorprofile.Writer{Forward: os.Stdout, Profile: colorprofile.Ascii}
		stderr = &colorprofile.Writer{Forward: os.Stderr, Profile: colorprofile.Ascii}
	}
	output = bufio.NewWriter(stdout)
	stdout = output
	if v, err := release.ParseVersion(version); err == nil && (len(os.Args) < 2 || os.Args[1] != "upgrade") {
		news.Arrive(v)
	}
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
		command, err := agent.ResumeCommand(r.Agent, r.Home, r.Path, r.Session.Cwd)
		if err != nil {
			fail("superfind command: " + err.Error())
		}
		fmt.Fprintln(stdout, command)
	case "show":
		runShow(args[1:])
	case "handoff":
		runHandoff(args[1:])
	case "share":
		runShare(args[1:])
	case "receive":
		runReceive(args[1:])
	case "blame":
		runBlame(args[1:])
	case "upgrade":
		runUpgrade(args[1:])
	case "telemetry":
		runTelemetry(args[1:])
	case "checkin":
		if news.Told() {
			telemetry.Checkin(context.Background(), telemetryBuild())
		}
	case "pick":
		resume := false
		for _, arg := range args[1:] {
			if arg != "--run" {
				usageError("superfind pick: there is no " + arg + " option. See superfind help pick.")
			}
			resume = true
		}
		chosen, folder, err := tui.Pick(build())
		if err != nil {
			fail(err.Error())
		}
		if chosen == nil {
			exit(1)
			return
		}
		switch {
		case folder && resume:
			shellIn(sessionFolder(*chosen))
		case folder:
			fmt.Fprint(stdout, render.Flat(chosen.Session.Title)+"\ncd -- "+agent.ShellQuote(sessionFolder(*chosen))+"\n")
		case resume:
			resumeHere(*chosen)
		default:
			fmt.Fprint(stdout, handoff(*chosen))
		}
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
		case "--query", "--width", "--limit", "--version":
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
