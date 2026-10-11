package ghostty

import (
	"os"
	"testing"

	"github.com/adammiribyan/superfind/internal/proc"
)

func TestMain(m *testing.M) {
	empty, err := os.MkdirTemp("", "no-osascript")
	if err != nil {
		panic(err)
	}
	os.Setenv("PATH", empty)
	code := m.Run()
	os.Remove(empty)
	os.Exit(code)
}

func dev(major, minor uint32) proc.TTY { return proc.TTY{Major: major, Minor: minor} }

func TestTheTerminalToFocusIsTheOnlyOneOnTheSessionsTTY(t *testing.T) {
	devices := map[string]proc.TTY{"/dev/ttys003": dev(16, 3), "/dev/ttys005": dev(16, 5), "/dev/pts/7": dev(136, 7), "/dev/tty.alias": dev(16, 5), `/dev/ttys9" & quit & "`: dev(16, 9)}
	terminalOf := func(path string) (proc.TTY, bool) {
		tty, ok := devices[path]
		return tty, ok
	}
	cases := []struct {
		out  string
		tty  proc.TTY
		want string
	}{
		{"/dev/ttys003, /dev/ttys004\n", dev(16, 3), "/dev/ttys003"},
		{"/dev/ttys004, /dev/pts/7\n", dev(136, 7), "/dev/pts/7"},
		{"/dev/ttys003\n", dev(16, 4), ""},
		{"/dev/ttys005, /dev/tty.alias\n", dev(16, 5), ""},
		{"missing value, /dev/ttys003\n", dev(16, 3), "/dev/ttys003"},
		{"/dev/ttys003\", \"x\n", dev(16, 3), ""},
		{`/dev/ttys9" & quit & "` + "\n", dev(16, 9), ""},
		{"\n", dev(16, 3), ""},
		{"/dev/ttys003\n", proc.TTY{}, ""},
	}
	for _, c := range cases {
		got, ok := terminalFor([]byte(c.out), c.tty, terminalOf)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("terminalFor(%q, %v) = %q, %v, want %q", c.out, c.tty, got, ok, c.want)
		}
	}
}

func TestOnlyAProcessWithAGhosttyAncestorRunsUnderGhostty(t *testing.T) {
	tree := map[int]proc.Process{
		1:   {PID: 1, Name: "launchd"},
		500: {PID: 500, PPID: 1, Name: "ghostty"},
		501: {PID: 501, PPID: 500, Name: "login"},
		502: {PID: 502, PPID: 501, Name: "-zsh"},
		503: {PID: 503, PPID: 502, Name: "claude"},
		600: {PID: 600, PPID: 1, Name: "tmux"},
		601: {PID: 601, PPID: 600, Name: "claude"},
		700: {PID: 700, PPID: 699, Name: "claude"},
		800: {PID: 800, PPID: 800, Name: "claude"},
		900: {PID: 900, PPID: 1, Name: "ghostty-helper"},
		901: {PID: 901, PPID: 900, Name: "claude"},
	}
	lookup := func(pid int) (proc.Process, bool) {
		p, ok := tree[pid]
		return p, ok
	}
	for pid, want := range map[int]bool{503: true, 502: true, 601: false, 700: false, 800: false, 901: false, 1: false} {
		if got := underGhostty(pid, lookup); got != want {
			t.Errorf("underGhostty(%d) = %v, want %v", pid, got, want)
		}
	}
}

func TestADeepTreeStopsLookingAfterThirtyTwoParents(t *testing.T) {
	tree := map[int]proc.Process{1: {PID: 1, Name: "ghostty"}}
	for pid := 2; pid <= 40; pid++ {
		tree[pid] = proc.Process{PID: pid, PPID: pid - 1, Name: "sh"}
	}
	lookup := func(pid int) (proc.Process, bool) {
		p, ok := tree[pid]
		return p, ok
	}
	if !underGhostty(20, lookup) {
		t.Error("a process 19 parents under Ghostty is not under Ghostty")
	}
	if underGhostty(40, lookup) {
		t.Error("a process 39 parents under Ghostty was followed all the way up")
	}
}
