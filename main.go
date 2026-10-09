package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/adammiribyan/hop/internal/session"

	"github.com/adammiribyan/hop/internal/agent"
	"github.com/adammiribyan/hop/internal/typesafe"

	"github.com/charmbracelet/x/term"
)

const version = "0.9.0"

const usage = `hop: find a past AI coding session and hop back into it. It reads Claude Code, Codex, and Grok sessions.

  hop                     open the picker (after shell setup; see hop init)
  hop init zsh|bash       print the shell setup; add  eval "$(hop init zsh)"  to ~/.zshrc
                          or  eval "$(hop init bash)"  to ~/.bashrc
  hop pick                open the picker; prints the chosen session's title, then the command
                          that resumes it
  hop search WORDS        print sessions whose title or conversation contains every word;
                          is:waiting and is:unfinished filter by how a session ended,
                          agent:claude, agent:codex, and agent:grok by the agent
  hop search --deep WORDS
                          print the sessions that best fit what you describe, even in other words
  hop preview SESSION [--width N] [--recent] [--query Q]
                          print where a session left off, or its recent conversation
  hop command SESSION     print the shell command that resumes a session
  hop index               build or refresh the index
  hop label               mark sessions waiting on you (needs a TypeSafe key)
  hop --version
`

const zshSetup = `typeset -g _hop_bin=%s

hop() {
  if (( $# )); then
    "$_hop_bin" "$@"
    return
  fi
  local out
  out=$("$_hop_bin" pick) || return
  print -r -- $'\e[38;2;215;119;87m✻\e[0m Resuming \e[1m'"${out%%$'\n'*}"$'\e[0m'
  print -s -- "${out#*$'\n'}"
  eval "${out#*$'\n'}"
}

_hop_widget() {
  local out
  if out=$("$_hop_bin" pick); then
    zle -I
    print -r -- $'\e[38;2;215;119;87m✻\e[0m Resuming \e[1m'"${out%%$'\n'*}"$'\e[0m'
    BUFFER=${out#*$'\n'}
    zle accept-line
  else
    zle reset-prompt
  fi
}
zle -N _hop_widget
bindkey '^Xc' _hop_widget
`

const bashSetup = `_hop_bin=%s

_hop_resume() {
  printf '\e[38;2;215;119;87m✻\e[0m Resuming \e[1m%%s\e[0m\n' "${1%%%%$'\n'*}"
  history -s "${1#*$'\n'}"
  eval "${1#*$'\n'}"
}

hop() {
  if (( $# )); then
    "$_hop_bin" "$@"
    return
  fi
  local out
  out=$("$_hop_bin" pick) || return
  _hop_resume "$out"
}

_hop_widget() {
  local out
  out=$("$_hop_bin" pick) && _hop_resume "$out"
}
[[ $- == *i* ]] && bind -x '"\C-xc": _hop_widget'
`

var shellSetups = map[string]string{"zsh": zshSetup, "bash": bashSetup}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func secondaryFromEnv() string {
	hx := os.Getenv("HOP_SECONDARY")
	if !hexColor.MatchString(hx) {
		return ansiDim
	}
	r, _ := strconv.ParseUint(hx[1:3], 16, 8)
	g, _ := strconv.ParseUint(hx[3:5], 16, 8)
	b, _ := strconv.ParseUint(hx[5:7], 16, 8)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

func rowContext(t rowText, secondary string) string {
	flag := ""
	if t.waiting != "" {
		flag = ansiAccent + t.waiting + ansiReset + secondary + " · "
	}
	return flag + strings.Join(t.context, " · ")
}

func printRows(rows []session.Row, shown tags) {
	secondary := secondaryFromEnv()
	for _, r := range rows {
		t := rowTextOf(r, shown)
		display := " " + t.title + "  " + secondary + rowContext(t, secondary) + ansiReset
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", r.Path, r.Session.Cwd, r.Session.ID, r.Home, t.title, display)
	}
}

func handoffTitle(r session.Row) string {
	title := strings.Join(strings.Fields(r.Session.Title), " ")
	if r.Session.Active() {
		title += ansiReset + ansiDim + " · may still be open in another window"
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
		fail("hop: can't open the search index in " + session.CacheDir() + ": " + err.Error())
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
		fail("hop " + command + ": " + err.Error())
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
		fail("hop " + command + ": " + arg + " is not inside a " + strings.Join(labels[:len(labels)-1], ", ") +
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
	typesafe.UserAgent = "hop/" + version
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		return
	}
	switch args[0] {
	case "--version", "version":
		fmt.Println("hop " + version)
	case "init":
		setup, ok := "", len(args) > 1
		if ok {
			setup, ok = shellSetups[args[1]]
		}
		if !ok {
			fail("hop init: which shell? hop init zsh or hop init bash")
		}
		fmt.Printf(setup, agent.ShellQuote(selfPath()))
	case "index":
		fmt.Printf("%d sessions indexed\n", len(mustIndex("index")))
	case "search":
		rows := session.Annotate(mustIndex("search"))
		shown := tagsFor(rows)
		db := mustOpenSearch()
		deep, words := false, []string{}
		for _, arg := range args[1:] {
			if arg == "--deep" {
				deep = true
			} else {
				words = append(words, arg)
			}
		}
		query := strings.Join(words, " ")
		if !deep {
			found, err := session.Search(db, query, rows)
			if err != nil {
				fail("hop search: " + err.Error())
			}
			printRows(found, shown)
			return
		}
		query, eligible := session.Narrow(query, rows)
		if query == "" {
			printRows(session.RecentFirst(eligible), shown)
			return
		}
		paths, err := ask(db, query, eligible)
		var failure askFailure
		if errors.As(err, &failure) {
			fail(failure.Error())
		}
		printRows(session.InOrder(rows, paths), shown)
	case "label":
		key := typesafe.Key()
		if key == "" {
			fail("hop label: needs a TypeSafe key in TYPESAFE_API_KEY or ~/.config/hop/env")
		}
		if err := session.Label(key, mustIndex("label")); err != nil {
			fail("hop label: " + err.Error())
		}
	case "preview":
		if len(args) < 2 {
			fail("hop preview: which session?")
		}
		req := previewRequest{width: 80, secondary: secondaryFromEnv()}
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--recent":
				req.recent = true
			case "--width":
				if i+1 < len(args) {
					req.width, _ = strconv.Atoi(args[i+1])
					i++
				}
			case "--query":
				if i+1 < len(args) {
					req.query = args[i+1]
					i++
				}
			}
		}
		r, indexed, rows := sessionAt("preview", args[1])
		req.agent, req.path = r.Agent, r.Path
		var header []string
		if indexed {
			header = append(previewHeader(req, r, tagsFor(rows), mustOpenSearch()), "")
		}
		body, err := previewBody(req)
		for _, line := range append(header, body...) {
			fmt.Println(line)
		}
		if err != nil {
			os.Exit(1)
		}
	case "command":
		if len(args) < 2 {
			fail("hop command: which session?")
		}
		r, indexed, _ := sessionAt("command", args[1])
		if !indexed {
			fail("hop command: not an indexed session: " + args[1])
		}
		fmt.Println(agent.ResumeCommand(r.Agent, r.Home, r.Session.Cwd, r.Session.ID))
	case "pick":
		chosen, err := pick()
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
