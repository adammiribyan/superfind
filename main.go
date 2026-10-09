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

	"github.com/charmbracelet/x/term"
)

const usage = `hop: find a past AI coding session and hop back into it. Today it reads Claude Code sessions.

  hop                     open the picker (after shell setup; see hop init)
  hop init zsh|bash       print the shell setup; add  eval "$(hop init zsh)"  to ~/.zshrc
                          or  eval "$(hop init bash)"  to ~/.bashrc
  hop pick                open the picker; prints the chosen session's title, then the command
                          that resumes it
  hop search WORDS        print sessions whose title or conversation contains every word;
                          is:waiting and is:unfinished filter by how a session ended
  hop search --deep WORDS
                          print the sessions that best fit what you describe, even in other words
  hop preview TRANSCRIPT [--width N] [--recent] [--query Q]
                          print where a session left off, or its recent conversation
  hop command TRANSCRIPT  print the shell command that resumes a session
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

func printRows(rows []Row, shown tags) {
	secondary := secondaryFromEnv()
	for _, r := range rows {
		t := rowTextOf(r, shown)
		display := " " + t.title + "  " + secondary + rowContext(t, secondary) + ansiReset
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", r.Path, r.Session.Cwd, r.Session.ID, r.Home, t.title, display)
	}
}

func searchRows(db *sql.DB, query string, rows []Row) ([]Row, error) {
	var words, filters []string
	for _, w := range strings.Fields(query) {
		if strings.HasPrefix(w, "is:") {
			filters = append(filters, statusLabelFor(w[3:]))
		} else {
			words = append(words, w)
		}
	}
	var wanted []string
	for _, f := range filters {
		if f != "" {
			wanted = append(wanted, f)
		}
	}
	if len(wanted) > 0 {
		var kept []Row
		for _, r := range rows {
			for _, w := range wanted {
				if r.Status == w {
					kept = append(kept, r)
					break
				}
			}
		}
		rows = kept
	}
	if db == nil {
		return recentFirst(rows), nil
	}
	found, searched, err := matches(db, strings.Join(words, " "))
	if err != nil {
		return nil, fmt.Errorf("the search index couldn't be read: %w", err)
	}
	if !searched {
		return recentFirst(rows), nil
	}
	return inOrder(rows, found), nil
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9@%_+=:,./-]+$`)

func shellQuote(s string) string {
	if s != "" && shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func handoffTitle(r Row) string {
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
	db, err := openSearch()
	if err != nil {
		fail("hop: can't open the search index in " + cacheDir() + ": " + err.Error())
	}
	return db
}

func mustIndex(command string) []Row {
	noticed := false
	rows, err := index(func(stale int) {
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

func sessionAt(command, arg string) (r Row, indexed bool, rows []Row) {
	path := resolve(arg)
	p, ok := locate(path, places())
	if !ok {
		fail("hop " + command + ": not a session hop reads: " + arg)
	}
	rows = annotate(mustIndex(command))
	for _, r := range rows {
		if r.Path == path {
			return r, true, rows
		}
	}
	return Row{Agent: p.agent, Home: p.home, Path: path}, false, rows
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

func main() {
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
		fmt.Printf(setup, shellQuote(selfPath()))
	case "index":
		fmt.Printf("%d sessions indexed\n", len(mustIndex("index")))
	case "search":
		rows := annotate(mustIndex("search"))
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
			found, err := searchRows(db, query, rows)
			if err != nil {
				fail("hop search: " + err.Error())
			}
			printRows(found, shown)
			return
		}
		if strings.TrimSpace(query) == "" {
			printRows(recentFirst(rows), shown)
			return
		}
		paths, err := ask(db, query, rows)
		var failure askFailure
		if errors.As(err, &failure) {
			fail(failure.Error())
		}
		printRows(inOrder(rows, paths), shown)
	case "label":
		key := typesafeKey()
		if key == "" {
			fail("hop label: needs a TypeSafe key in TYPESAFE_API_KEY or ~/.config/hop/env")
		}
		if err := labelSessions(key, mustIndex("label")); err != nil {
			fail("hop label: " + err.Error())
		}
	case "preview":
		if len(args) < 2 {
			fail("hop preview: which transcript?")
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
			fail("hop command: which transcript?")
		}
		r, indexed, _ := sessionAt("command", args[1])
		if !indexed {
			fail("hop command: not an indexed session: " + args[1])
		}
		fmt.Println(resumeCommand(r))
	case "pick":
		chosen, err := pick()
		if err != nil {
			fail(err.Error())
		}
		if chosen == nil {
			os.Exit(1)
		}
		fmt.Println(handoffTitle(*chosen))
		fmt.Println(resumeCommand(*chosen))
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}
