package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const usage = `hop: find a past AI coding session and hop back into it. Today it reads Claude Code sessions.

  hop                     open the picker (after shell setup; see hop init)
  hop init zsh            print the shell setup; add  eval "$(hop init zsh)"  to ~/.zshrc
  hop pick                open the picker; prints the chosen session's title, then the command
                          that resumes it
  hop search WORDS        print sessions whose title or conversation contains every word;
                          is:waiting and is:unfinished filter by how a session ended
  hop ask QUESTION        print sessions ranked by meaning (Jev with a TypeSafe key, else Claude)
  hop preview TRANSCRIPT [--width N] [--recent] [--query Q]
                          print where a session left off, or its recent conversation
  hop command TRANSCRIPT  print the shell command that resumes a session
  hop index               build or refresh the index
  hop label               mark sessions waiting on you (needs a TypeSafe key)
  hop --version
`

const zshSetup = `hop() {
  if (( $# )); then
    command hop "$@"
    return
  fi
  local out
  out=$(command hop pick) || return
  print -r -- $'\e[38;2;215;119;87m✻\e[0m Resuming \e[1m'"${out%%$'\n'*}"$'\e[0m'
  print -s -- "${out#*$'\n'}"
  eval "${out#*$'\n'}"
}

_hop_widget() {
  local out
  if out=$(command hop pick); then
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

func rowContext(r Row, status string, showAccount bool, secondary string) string {
	flag := ""
	switch {
	case status == waitingLabel:
		flag = ansiAccent + status + ansiReset + secondary + " · "
	case status != "":
		flag = status + " · "
	}
	context := flag + when(r.Session) + " · " + r.Session.Project()
	if !folderExists(r.Session.Cwd) {
		context += " · folder removed"
	}
	if showAccount {
		context += " · " + filepath.Base(r.ConfigDir)
	}
	return context
}

func showsAccounts(rows []Row) bool {
	dirs := map[string]bool{}
	for _, r := range rows {
		dirs[r.ConfigDir] = true
	}
	return len(dirs) > 1
}

func printRows(rows []Row) {
	known := statuses()
	secondary := secondaryFromEnv()
	accounts := showsAccounts(rows)
	for _, r := range rows {
		title := strings.Join(strings.Fields(r.Session.Title), " ")
		display := " " + title + "  " + secondary + rowContext(r, statusLabel(r.Path, known), accounts, secondary) + ansiReset
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", r.Path, r.Session.Cwd, r.Session.ID, r.ConfigDir, title, display)
	}
}

func searchRows(db *sql.DB, query string) []Row {
	rows := cachedRows()
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
		known := statuses()
		var kept []Row
		for _, r := range rows {
			label := statusLabel(r.Path, known)
			for _, w := range wanted {
				if label == w {
					kept = append(kept, r)
					break
				}
			}
		}
		rows = kept
	}
	if db == nil {
		return recentFirst(rows)
	}
	found, searched := matches(db, strings.Join(words, " "))
	if !searched {
		return recentFirst(rows)
	}
	return inOrder(rows, found)
}

func resumeCommand(cwd, sessionID, configDir string) string {
	defaultConfig := os.Getenv("CLAUDE_CONFIG_DIR")
	if defaultConfig == "" {
		defaultConfig = filepath.Join(home(), ".claude")
	}
	env := ""
	if resolve(configDir) != resolve(defaultConfig) {
		env = "CLAUDE_CONFIG_DIR=" + shellQuote(configDir) + " "
	}
	return "cd " + shellQuote(survivingDir(cwd)) + " && " + env + "claude --resume " + sessionID
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
		if len(args) < 2 || args[1] != "zsh" {
			fail("hop init: only zsh is supported so far")
		}
		fmt.Print(zshSetup)
	case "index":
		fmt.Printf("%d sessions indexed\n", len(index()))
	case "search":
		db, _ := openSearch()
		printRows(searchRows(db, strings.Join(args[1:], " ")))
	case "ask":
		query := strings.Join(args[1:], " ")
		db, err := openSearch()
		if err != nil {
			fail(err.Error())
		}
		paths, err := ask(db, query, cachedRows())
		var failure askFailure
		if errors.As(err, &failure) {
			fail(failure.Error())
		}
		printRows(inOrder(cachedRows(), paths))
	case "label":
		if key := typesafeKey(); key != "" {
			labelSessions(key, index())
		}
	case "preview":
		if len(args) < 2 {
			fail("hop preview: which transcript?")
		}
		req := previewRequest{path: args[1], width: 80, secondary: secondaryFromEnv()}
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
		db, _ := openSearch()
		p := buildPreview(req, loadCache(), statuses(), db)
		for _, line := range append(append(p.header, ""), p.body...) {
			fmt.Println(line)
		}
	case "command":
		if len(args) < 2 {
			fail("hop command: which transcript?")
		}
		path := resolve(args[1])
		e, ok := loadCache()[path]
		if !ok || e.Session == nil {
			fail("hop command: not an indexed session: " + args[1])
		}
		fmt.Println(resumeCommand(e.Session.Cwd, e.Session.ID, configDirOf(path, configDirs())))
	case "pick":
		chosen, err := pick()
		if err != nil {
			fail(err.Error())
		}
		if chosen == nil {
			os.Exit(1)
		}
		fmt.Println(handoffTitle(*chosen))
		fmt.Println(resumeCommand(chosen.Session.Cwd, chosen.Session.ID, chosen.ConfigDir))
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}
