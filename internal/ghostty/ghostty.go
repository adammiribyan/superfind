package ghostty

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"time"

	"github.com/adammiribyan/superfind/internal/open"
	"github.com/adammiribyan/superfind/internal/proc"
)

const (
	deadline  = time.Second
	ancestors = 32
)

func Focus(s open.State) bool {
	if s.TTY == (proc.TTY{}) || !underGhostty(s.PID, proc.Lookup) {
		return false
	}
	osascript, err := exec.LookPath("osascript")
	if err != nil {
		return false
	}
	out, err := tell(osascript, `get tty of every terminal`)
	if err != nil {
		return false
	}
	path, ok := terminalFor(out, s.TTY, proc.TerminalOf)
	if !ok {
		return false
	}
	_, err = tell(osascript, `focus (first terminal whose tty is "`+path+`")`)
	return err == nil
}

func tell(osascript, command string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, osascript, "-e", `tell application "Ghostty" to `+command)
	cmd.WaitDelay = deadline / 10
	return cmd.Output()
}

var ttyPath = regexp.MustCompile(`^/dev/[A-Za-z0-9._/-]+$`)

func terminalFor(out []byte, tty proc.TTY, terminalOf func(string) (proc.TTY, bool)) (string, bool) {
	if tty == (proc.TTY{}) {
		return "", false
	}
	var found []string
	for _, field := range bytes.Split(bytes.TrimSpace(out), []byte(",")) {
		path := string(bytes.TrimSpace(field))
		if !ttyPath.MatchString(path) {
			continue
		}
		if device, ok := terminalOf(path); ok && device == tty {
			found = append(found, path)
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

func underGhostty(pid int, lookup func(int) (proc.Process, bool)) bool {
	for range ancestors {
		p, ok := lookup(pid)
		if !ok {
			return false
		}
		if p.Name == "ghostty" {
			return true
		}
		pid = p.PPID
	}
	return false
}
