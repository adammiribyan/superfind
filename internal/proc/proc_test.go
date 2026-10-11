package proc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

func TestProcArgsSkipsTheExecPathAndItsPaddingAndStopsBeforeTheEnvironment(t *testing.T) {
	raw := append([]byte{3, 0, 0, 0}, "/usr/local/bin/codex\x00\x00\x00\x00codex\x00resume\x00019a-id\x00PATH=/bin\x00HOME=/h\x00"...)
	if got, want := procArgs(raw), []string{"codex", "resume", "019a-id"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProcArgsKeepsAnEmptyArgument(t *testing.T) {
	raw := append([]byte{2, 0, 0, 0}, "/bin/claude\x00claude\x00\x00HOME=/h\x00"...)
	if got, want := procArgs(raw), []string{"claude", ""}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProcArgsRejectsATruncatedBuffer(t *testing.T) {
	for _, raw := range [][]byte{nil, {1, 0}, append([]byte{1, 0, 0, 0}, "/bin/claude"...)} {
		if got := procArgs(raw); got != nil {
			t.Errorf("procArgs(%q) = %q, want nil", raw, got)
		}
	}
}

func TestParseStatReadsANameWithParenthesesAndSpaces(t *testing.T) {
	line := "4242 (a) (b c) S 1 4242 4242 34819 4242 4194304 120 0 0 0 7 3 0 0 20 0 1 0 987654 1000 12 18446744073709551615\n"
	got, ok := parseStat(line)
	if want := (stat{name: "a) (b c", tty: TTY{136, 3}, ticks: 987654}); !ok || got != want {
		t.Fatalf("got %+v ok=%v, want %+v", got, ok, want)
	}
}

func TestParseStatRejectsAZombieAndADeadProcess(t *testing.T) {
	line := "4242 (claude) %s 1 4242 4242 34819 4242 4194304 120 0 0 0 7 3 0 0 20 0 1 0 987654 0 0 18446744073709551615\n"
	for _, state := range []string{"Z", "X"} {
		if got, ok := parseStat(fmt.Sprintf(line, state)); ok {
			t.Errorf("state %s parsed as live: %+v", state, got)
		}
	}
	if got, ok := parseStat(fmt.Sprintf(line, "S")); !ok || got.name != "claude" {
		t.Errorf("a sleeping process: got %+v ok=%v", got, ok)
	}
}

func TestParseStatRejectsAShortLine(t *testing.T) {
	if _, ok := parseStat("4242 (sleep) S 1 4242"); ok {
		t.Fatal("a line without a start time parsed")
	}
}

func TestAStartTimeYearsAfterBootDoesNotOverflow(t *testing.T) {
	boot := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := map[uint64]time.Time{
		0:                     boot,
		150:                   boot.Add(1500 * time.Millisecond),
		5 * 365 * 86400 * 100: time.Date(2024, 12, 30, 0, 0, 0, 0, time.UTC),
	}
	for ticks, want := range cases {
		if got := startedAt(boot, ticks); !got.Equal(want) {
			t.Errorf("startedAt(boot, %d) = %v, want %v", ticks, got, want)
		}
	}
}

func TestTTYNumbersDecodeToMajorAndMinor(t *testing.T) {
	cases := map[int64]TTY{0: {}, 34819: {136, 3}, 1083436: {136, 300}, 1025: {4, 1}}
	for n, want := range cases {
		if got := fromTTYNr(n); got != want {
			t.Errorf("fromTTYNr(%d) = %+v, want %+v", n, got, want)
		}
	}
}

func TestTdevDecodesToMajorAndMinor(t *testing.T) {
	cases := map[int32]TTY{-1: {}, 0: {}, 16<<24 | 14: {16, 14}, 16<<24 | 300: {16, 300}}
	for dev, want := range cases {
		if got := fromTdev(dev); got != want {
			t.Errorf("fromTdev(%d) = %+v, want %+v", dev, got, want)
		}
	}
}

func TestLookupFindsThisProcessAndNotAReapedOne(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process facts are read on macOS and Linux only")
	}
	me, ok := Lookup(os.Getpid())
	if !ok || me.PID != os.Getpid() || me.Name == "" {
		t.Fatalf("got %+v ok=%v", me, ok)
	}
	if age := time.Since(me.Start); age < -time.Second || age > time.Minute {
		t.Fatalf("this process started %v ago", age)
	}
	child := exec.Command("true")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	if p, ok := Lookup(child.Process.Pid); ok {
		t.Fatalf("a reaped child is still found: %+v", p)
	}
}

func TestLookupDoesNotFindAnExitedChildNotYetReaped(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process facts are read on macOS and Linux only")
	}
	child := exec.Command("sleep", "0")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Wait()
	time.Sleep(200 * time.Millisecond)
	if p, ok := Lookup(child.Process.Pid); ok {
		t.Fatalf("a zombie child is found as live: %+v", p)
	}
}

func TestNamedFindsThisProcessByItsName(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process facts are read on macOS and Linux only")
	}
	me, _ := Lookup(os.Getpid())
	found := slices.ContainsFunc(Named(me.Name[:3]), func(p Process) bool { return p.PID == os.Getpid() })
	if !found {
		t.Fatalf("Named(%q) left out this process", me.Name[:3])
	}
}

func TestArgsReadsThisProcessArguments(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process facts are read on macOS and Linux only")
	}
	if got := Args(os.Getpid()); !slices.Equal(got, os.Args) {
		t.Fatalf("got %q, want %q", got, os.Args)
	}
}

func TestTerminalOfReadsADeviceNumberAndRejectsAFile(t *testing.T) {
	want, known := map[string]TTY{"darwin": {3, 2}, "linux": {1, 3}}[runtime.GOOS]
	if !known {
		t.Skip("device numbers are read on macOS and Linux only")
	}
	if got, ok := TerminalOf("/dev/null"); !ok || got != want {
		t.Fatalf("TerminalOf(/dev/null) = %+v ok=%v, want %+v", got, ok, want)
	}
	file := filepath.Join(t.TempDir(), "pts")
	os.WriteFile(file, nil, 0o600)
	if got, ok := TerminalOf(file); ok {
		t.Fatalf("a plain file is a terminal: %+v", got)
	}
}
