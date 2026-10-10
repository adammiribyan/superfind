//go:build linux

package proc

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

func Lookup(pid int) (Process, bool) {
	if pid <= 0 {
		return Process{}, false
	}
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return Process{}, false
	}
	s, ok := parseStat(string(raw))
	boot, booted := bootTime()
	if !ok || !booted {
		return Process{}, false
	}
	return Process{PID: pid, Name: s.name, Start: startedAt(boot, s.ticks), TTY: s.tty}, true
}

var bootTime = sync.OnceValues(func() (time.Time, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		if rest, found := strings.CutPrefix(lines.Text(), "btime "); found {
			secs, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			return time.Unix(secs, 0), err == nil
		}
	}
	return time.Time{}, false
})

func Named(prefixes ...string) []Process {
	comms, _ := filepath.Glob("/proc/[0-9]*/comm")
	var out []Process
	for _, c := range comms {
		name, err := os.ReadFile(c)
		named := strings.TrimSuffix(string(name), "\n")
		if err != nil || !slices.ContainsFunc(prefixes, func(prefix string) bool { return strings.HasPrefix(named, prefix) }) {
			continue
		}
		pid, _ := strconv.Atoi(filepath.Base(filepath.Dir(c)))
		if p, ok := Lookup(pid); ok {
			out = append(out, p)
		}
	}
	return out
}

func Args(pid int) []string {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || len(raw) == 0 {
		return nil
	}
	return strings.Split(string(bytes.TrimSuffix(raw, []byte{0})), "\x00")
}

func TerminalOf(path string) (TTY, bool) {
	var st unix.Stat_t
	if unix.Stat(path, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFCHR {
		return TTY{}, false
	}
	return TTY{unix.Major(st.Rdev), unix.Minor(st.Rdev)}, true
}
