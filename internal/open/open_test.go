package open

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/proc"
)

const sessionID = "0f6cab12-3a4b-4c5d-8e9f-001122334455"

func needsProcessFacts(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process facts are read on macOS and Linux only")
	}
	t.Setenv("PATH", t.TempDir())
}

func started(t *testing.T) time.Time {
	t.Helper()
	me, ok := proc.Lookup(os.Getpid())
	if !ok {
		t.Fatal("this process was not found")
	}
	return me.Start
}

func reapedPID(t *testing.T) int {
	t.Helper()
	child := exec.Command("/bin/sh", "-c", "exit 0")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	return child.Process.Pid
}

func pidJSON(pid int, id string, startedAt time.Time, status string) []byte {
	raw, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": id, "startedAt": startedAt.UnixMilli(), "status": status})
	return raw
}

func TestOnlyPidFileNamesAreRead(t *testing.T) {
	cases := map[string]int{"123.json": 123, "7.json": 7, "123.abc.key": 0, "123.json.key": 0, "abc.json": 0, "123.JSON": 0, ".json": 0, "123.json.tmp": 0}
	for name, want := range cases {
		if got, ok := pidFileName(name); got != want || ok != (want != 0) {
			t.Errorf("pidFileName(%q) = %d, %v", name, got, ok)
		}
	}
}

func TestAClaudePidFileClaimsItsSessionOnlyWhileItsProcessRuns(t *testing.T) {
	needsProcessFacts(t)
	me, start, gone := os.Getpid(), started(t), reapedPID(t)
	cases := []struct {
		name string
		raw  []byte
		pid  int
		want State
		ok   bool
	}{
		{"busy", pidJSON(me, sessionID, start.Add(2*time.Second), "busy"), me, State{PID: me, Status: "busy"}, true},
		{"idle", pidJSON(me, sessionID, start.Add(2*time.Second), "idle"), me, State{PID: me, Status: "idle"}, true},
		{"unknown status", pidJSON(me, sessionID, start, "thinking"), me, State{PID: me}, true},
		{"written just before the start", pidJSON(me, sessionID, start.Add(-900*time.Millisecond), "busy"), me, State{PID: me, Status: "busy"}, true},
		{"written before a reused pid started", pidJSON(me, sessionID, start.Add(-time.Hour), "busy"), me, State{}, false},
		{"another pid than its name", pidJSON(me, sessionID, start, "busy"), me + 1, State{}, false},
		{"process gone", pidJSON(gone, sessionID, start, "busy"), gone, State{}, false},
		{"no session", pidJSON(me, "", start, "busy"), me, State{}, false},
		{"no start time", []byte(fmt.Sprintf(`{"pid":%d,"sessionId":%q,"status":"busy"}`, me, sessionID)), me, State{}, false},
		{"pid zero", pidJSON(0, sessionID, start, "busy"), 0, State{}, false},
		{"not json", []byte(`{"pid":`), me, State{}, false},
	}
	for _, c := range cases {
		got, ok := claudeClaim(c.raw, c.pid)
		if ok != c.ok || got.state != c.want || ok && got.key != (Key{"claude", sessionID}) {
			t.Errorf("%s: got %+v ok=%v, want %+v ok=%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestGrokEntriesAcceptEveryOpenedAtShape(t *testing.T) {
	raw := []byte(`[
		{"session_id":"a","pid":10,"cwd":"/w","opened_at":"2026-10-10T19:36:21Z"},
		{"session_id":"b","pid":11,"cwd":"/w","opened_at":1791660981},
		{"session_id":"c","pid":12,"cwd":"/w","opened_at":1791660981500},
		{"session_id":"d","pid":13,"cwd":"/w","opened_at":"yesterday"},
		{"session_id":"e","pid":14,"cwd":"/w"},
		{"session_id":"","pid":15,"opened_at":1791660981},
		{"session_id":"g","pid":0,"opened_at":1791660981}
	]`)
	got := grokEntries(raw)
	want := []grokEntry{
		{"a", 10, time.Date(2026, 10, 10, 19, 36, 21, 0, time.UTC)},
		{"b", 11, time.Unix(1791660981, 0)},
		{"c", 12, time.UnixMilli(1791660981500)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].id != want[i].id || got[i].pid != want[i].pid || !got[i].opened.Equal(want[i].opened) {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got := grokEntries([]byte(`{"session_id":"a"}`)); got != nil {
		t.Fatalf("an object instead of a list gave %+v", got)
	}
}

func TestTheResumedSessionIsReadFromEachAgentsArguments(t *testing.T) {
	codex, claude, grok := evidenceFor["codex"].resume, evidenceFor["claude"].resume, evidenceFor["grok"].resume
	cases := []struct {
		args   []string
		resume []string
		want   string
	}{
		{[]string{"codex", "resume", sessionID}, codex, sessionID},
		{[]string{"/opt/bin/codex", "resume", "-m", "o3", sessionID}, codex, sessionID},
		{[]string{"codex", "resume", "--last"}, codex, ""},
		{[]string{"codex", sessionID}, codex, ""},
		{[]string{"codex", "fork", sessionID}, codex, ""},
		{[]string{"codex", "--resume", sessionID}, codex, ""},
		{[]string{"claude", "--resume", sessionID}, claude, sessionID},
		{[]string{"claude", "-r", sessionID, "--model", "opus"}, claude, sessionID},
		{[]string{"claude", "--resume=" + sessionID}, claude, sessionID},
		{[]string{"claude", "--resume", sessionID, "--fork-session"}, claude, ""},
		{[]string{"claude", "resume", sessionID}, claude, ""},
		{[]string{"claude"}, claude, ""},
		{[]string{"claude", "--resume", "my-title"}, claude, ""},
		{[]string{"grok", "--resume", sessionID}, grok, sessionID},
		{[]string{"grok", "-r=" + sessionID}, grok, sessionID},
		{[]string{"grok", "resume", sessionID}, grok, sessionID},
	}
	for _, c := range cases {
		if got := resumedID(c.args, c.resume); got != c.want {
			t.Errorf("resumedID(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestTheStrongestClaimWinsBusyThenIdleThenTheLowestPid(t *testing.T) {
	k := Key{"claude", sessionID}
	got := strongest([]claim{
		{k, State{PID: 30}, proc.TTY{}},
		{k, State{PID: 40, Status: "idle"}, proc.TTY{}},
		{k, State{PID: 50, Status: "busy"}, proc.TTY{}},
		{k, State{PID: 20, Status: "idle"}, proc.TTY{}},
		{Key{"codex", sessionID}, State{PID: 9}, proc.TTY{}},
		{Key{"codex", sessionID}, State{PID: 8}, proc.TTY{}},
	})
	if got[k].state != (State{PID: 50, Status: "busy"}) || got[Key{"codex", sessionID}].state != (State{PID: 8}) {
		t.Fatalf("got %+v", got)
	}
}

func TestTmuxPanesAreKeyedByTheirTerminal(t *testing.T) {
	devices := map[string]proc.TTY{"/dev/ttys014": {16, 14}, "/dev/pts/3": {136, 3}}
	terminalOf := func(path string) (proc.TTY, bool) { tty, ok := devices[path]; return tty, ok }
	out := []byte("/dev/ttys014\t%3\t2\twork\n/dev/pts/3\t%12\t0\tmy\tsession\n/dev/gone\t%4\t1\tx\nbroken line\n")
	got := parsePanes(out, terminalOf)
	want := map[proc.TTY]Pane{{16, 14}: {ID: "%3", Session: "work", Window: 2}, {136, 3}: {ID: "%12", Session: "my\tsession", Window: 0}}
	if len(got) != len(want) || got[proc.TTY{16, 14}] != want[proc.TTY{16, 14}] || got[proc.TTY{136, 3}] != want[proc.TTY{136, 3}] {
		t.Fatalf("got %+v", got)
	}
}

func claudeHome(t *testing.T) string {
	home := filepath.Join(t.TempDir(), ".claude")
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestFindReportsASessionWhoseClaudeProcessRuns(t *testing.T) {
	needsProcessFacts(t)
	home, me := claudeHome(t), os.Getpid()
	sessions := filepath.Join(home, "sessions")
	os.WriteFile(filepath.Join(sessions, strconv.Itoa(me)+".json"), pidJSON(me, sessionID, time.Now(), "busy"), 0o600)
	for _, fifo := range []string{strconv.Itoa(me) + ".abc.key", "999999.json"} {
		if err := syscall.Mkfifo(filepath.Join(sessions, fifo), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := Find([]agent.Place{{Agent: agent.Named("claude"), Home: home}})
	if state, ok := got[Key{"claude", sessionID}]; !ok || state != (State{PID: me, Status: "busy"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestFindIgnoresAPidFileWrittenBeforeItsProcessStarted(t *testing.T) {
	needsProcessFacts(t)
	home, me := claudeHome(t), os.Getpid()
	os.WriteFile(filepath.Join(home, "sessions", strconv.Itoa(me)+".json"), pidJSON(me, sessionID, started(t).Add(-time.Hour), "busy"), 0o600)
	if state, ok := Find([]agent.Place{{Agent: agent.Named("claude"), Home: home}})[Key{"claude", sessionID}]; ok {
		t.Fatalf("a stale pid file still claims the session: %+v", state)
	}
}

func TestFindReportsAGrokSessionFromItsActiveList(t *testing.T) {
	needsProcessFacts(t)
	home, me := filepath.Join(t.TempDir(), ".grok"), os.Getpid()
	os.MkdirAll(home, 0o700)
	os.WriteFile(filepath.Join(home, "active_sessions.json"), []byte(fmt.Sprintf(
		`[{"session_id":%q,"pid":%d,"cwd":"/w","opened_at":%q}]`, sessionID, me, time.Now().UTC().Format(time.RFC3339))), 0o600)
	got := Find([]agent.Place{{Agent: agent.Named("grok"), Home: home}})
	if state, ok := got[Key{"grok", sessionID}]; !ok || state != (State{PID: me}) {
		t.Fatalf("got %+v", got)
	}
}
