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
)

const usage = `hop: find a past AI coding session and hop back into it. Today it reads Claude Code sessions.

  hop                     open the picker (after shell setup; see hop init)
  hop init zsh            print the shell setup; add  eval "$(hop init zsh)"  to ~/.zshrc
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

func rowContext(r Row, showAccount bool, secondary string) string {
	flag := ""
	switch {
	case r.Status == waitingLabel:
		flag = ansiAccent + r.Status + ansiReset + secondary + " · "
	case r.Status != "":
		flag = r.Status + " · "
	}
	context := flag + when(r.Session) + " · " + r.Session.Project()
	if r.FolderGone {
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
	secondary := secondaryFromEnv()
	accounts := showsAccounts(rows)
	for _, r := range rows {
		title := strings.Join(strings.Fields(r.Session.Title), " ")
		display := " " + title + "  " + secondary + rowContext(r, accounts, secondary) + ansiReset
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", r.Path, r.Session.Cwd, r.Session.ID, r.ConfigDir, title, display)
	}
}

func searchRows(db *sql.DB, query string, rows []Row) []Row {
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
	return "cd " + shellQuote(survivingDir(cwd)) + " && " + env + "claude --resume " + shellQuote(sessionID)
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
		fmt.Printf(zshSetup, shellQuote(selfPath()))
	case "index":
		rows, err := index()
		if err != nil {
			fail("hop index: " + err.Error())
		}
		fmt.Printf("%d sessions indexed\n", len(rows))
	case "search":
		db := mustOpenSearch()
		rows := annotate(cachedRows())
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
			printRows(searchRows(db, query, rows))
			return
		}
		if strings.TrimSpace(query) == "" {
			printRows(recentFirst(rows))
			return
		}
		paths, err := ask(db, query, rows)
		var failure askFailure
		if errors.As(err, &failure) {
			fail(failure.Error())
		}
		printRows(inOrder(rows, paths))
	case "label":
		if key := typesafeKey(); key != "" {
			rows, _ := index()
			labelSessions(key, rows)
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
		header := previewHeader(req, loadCache(), statuses(), mustOpenSearch())
		if header != nil {
			header = append(header, "")
		}
		for _, line := range append(header, previewBody(req)...) {
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
