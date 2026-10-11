package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func ResumeCommand(a *Agent, home, cwd, id string) string {
	env := ""
	if def := a.DefaultHome(); home != def && Resolve(home) != Resolve(def) {
		env = a.homeVar + "=" + ShellQuote(home) + " "
	}
	return "cd " + ShellQuote(SurvivingDir(cwd)) + " && " + env + a.resume + " " + ShellQuote(id)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9@_+:,./-]+$`)

func ShellQuote(s string) string {
	if s != "" && shellSafe.MatchString(s) {
		return s
	}
	return "'" + quoteMarks.Replace(s) + "'"
}

var quoteMarks = strings.NewReplacer(`'`, `'"'"'`, `\`, `'"\\"'`)

func SurvivingDir(dir string) string {
	for {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}
