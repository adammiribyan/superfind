package news

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adammiribyan/superfind/internal/release"
)

func TestTheTelemetryNoticeOutranksWhatsNewWhichOutranksAnUpdate(t *testing.T) {
	cases := []struct {
		d    due
		want Notice
	}{
		{due{}, None},
		{due{update: true}, UpdateOut},
		{due{whatsNew: true}, WhatsNew},
		{due{whatsNew: true, update: true}, WhatsNew},
		{due{telemetry: true}, TelemetryOn},
		{due{telemetry: true, update: true}, TelemetryOn},
		{due{telemetry: true, whatsNew: true}, TelemetryOn},
		{due{telemetry: true, whatsNew: true, update: true}, TelemetryOn},
	}
	for _, c := range cases {
		if got := pick(c.d); got != c.want {
			t.Errorf("pick(%+v) = %d, want %d", c.d, got, c.want)
		}
	}
}

var v0110 = Build{Version: release.Version{Major: 0, Minor: 11}, Headline: "Handoff, blame, and last"}

func home(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("SUPERFIND_TELEMETRY", "off")
	return dir
}

func line(b Build) string { return Open(b).Line() }

func write(t *testing.T, path, text string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func marker(t *testing.T, dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, ".local", "state", "superfind", "shown", "version"))
	if err != nil {
		return "missing"
	}
	return string(raw)
}

func TestWhatsNewShowsOnceToSomeoneWhoUsedAnEarlierVersion(t *testing.T) {
	for _, seen := range []string{"", "0.10.0\n", "not a version"} {
		dir := home(t)
		write(t, filepath.Join(dir, ".cache", "superfind", "index.json"), "{}")
		if seen != "" {
			write(t, filepath.Join(dir, ".local", "state", "superfind", "shown", "version"), seen)
		}
		Arrive(v0110.Version)
		if got := line(v0110); got != "New in 0.11.0 · Handoff, blame, and last" {
			t.Errorf("seen %q: first picker says %q, want what's new", seen, got)
		}
		if got := line(v0110); got != "" {
			t.Errorf("seen %q: second picker says %q, want nothing", seen, got)
		}
		if got := marker(t, dir); got != "0.11.0\n" {
			t.Errorf("seen %q: marker %q, want 0.11.0", seen, got)
		}
	}
}

func TestAFreshInstallMarksTheVersionWithoutSayingAnything(t *testing.T) {
	dir := home(t)
	Arrive(v0110.Version)
	write(t, filepath.Join(dir, ".cache", "superfind", "index.json"), "{}")
	if got := line(v0110); got != "" {
		t.Errorf("a fresh install's first picker says %q, want nothing", got)
	}
	if got := marker(t, dir); got != "0.11.0\n" {
		t.Errorf("marker %q, want 0.11.0", got)
	}
}

func TestGoingBackToAnOlderVersionSaysNothingAndRemembersIt(t *testing.T) {
	dir := home(t)
	write(t, filepath.Join(dir, ".local", "state", "superfind", "shown", "version"), "0.12.0\n")
	if got := line(v0110); got != "" {
		t.Errorf("after a rollback the picker says %q, want nothing", got)
	}
	if got := marker(t, dir); got != "0.11.0\n" {
		t.Errorf("marker %q, want 0.11.0", got)
	}
}

func TestAVersionWithNoHeadlineSaysNothing(t *testing.T) {
	dir := home(t)
	if got := line(Build{Version: v0110.Version}); got != "" {
		t.Errorf("picker says %q, want nothing", got)
	}
	if got := marker(t, dir); got != "0.11.0\n" {
		t.Errorf("marker %q, want 0.11.0", got)
	}
}

func TestAReadOnlyStateFolderMeansNoNotice(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes through read-only folders")
	}
	dir := home(t)
	state := filepath.Join(dir, ".local", "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(state, 0o700) })
	if got := line(v0110); got != "" {
		t.Errorf("with a read-only state folder the picker says %q, want nothing", got)
	}
}

func telemetryOn(t *testing.T) *int {
	t.Setenv("SUPERFIND_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	spawned := 0
	was := spawn
	spawn = func() { spawned++ }
	t.Cleanup(func() { spawn = was })
	return &spawned
}

func TestTheTelemetryNoticeShowsOnceAndThatSessionSendsNothing(t *testing.T) {
	dir := home(t)
	spawned := telemetryOn(t)
	write(t, filepath.Join(dir, ".cache", "superfind", "index.json"), "{}")
	write(t, filepath.Join(dir, ".local", "state", "superfind", "shown", "version"), "0.10.0\n")
	if got := line(v0110); got != "Daily update check and anonymous usage · superfind telemetry" {
		t.Errorf("the first picker says %q, want the telemetry notice", got)
	}
	told := filepath.Join(dir, ".local", "state", "superfind", "shown", "telemetry")
	if _, err := os.Stat(told); err != nil {
		t.Fatalf("the notice left no marker: %v", err)
	}
	if got := marker(t, dir); got != "0.10.0\n" {
		t.Errorf("the version marker moved to %q while the telemetry notice was showing", got)
	}
	if *spawned != 0 {
		t.Fatalf("the picker that showed the notice spawned %d check-ins", *spawned)
	}
	past := time.Now().Add(-time.Minute)
	os.Chtimes(told, past, past)
	if got := line(v0110); got != "New in 0.11.0 · Handoff, blame, and last" {
		t.Errorf("the second picker says %q, want what's new", got)
	}
	if *spawned != 1 {
		t.Errorf("the second picker spawned %d check-ins, want 1", *spawned)
	}
	if got := line(v0110); got != "" {
		t.Errorf("the third picker says %q, want nothing", got)
	}
	if *spawned != 2 {
		t.Errorf("the third picker, still with no check on disk, spawned %d check-ins in all, want 2", *spawned)
	}
	write(t, filepath.Join(dir, ".local", "state", "superfind", "check.json"), `{"next":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`","failures":0}`+"\n")
	line(v0110)
	if *spawned != 2 {
		t.Errorf("with the next check in the future the picker still spawned: %d in all", *spawned)
	}
	t.Setenv("DO_NOT_TRACK", "1")
	os.Remove(filepath.Join(dir, ".local", "state", "superfind", "check.json"))
	if got := line(v0110); got != "" || *spawned != 2 {
		t.Errorf("with DO_NOT_TRACK the picker says %q and spawned %d in all", got, *spawned)
	}
}

func TestANewerReleaseIsNoticedUnlessTheUserLeftIt(t *testing.T) {
	for _, c := range []struct {
		latest, skipped, want string
	}{
		{"0.12.1", "", "0.12.1 is out · superfind upgrade"},
		{"0.12.1", "0.12.1", ""},
		{"0.12.1", "0.12.0", "0.12.1 is out · superfind upgrade"},
		{"0.11.0", "", ""},
		{"0.10.9", "", ""},
		{"", "", ""},
		{"v0.12.1", "", ""},
	} {
		dir := home(t)
		write(t, filepath.Join(dir, ".local", "state", "superfind", "shown", "version"), "0.11.0\n")
		write(t, filepath.Join(dir, ".local", "state", "superfind", "check.json"), `{"next":"2999-01-01T00:00:00Z","latest":"`+c.latest+`","failures":0}`+"\n")
		if c.skipped != "" {
			write(t, filepath.Join(dir, ".local", "state", "superfind", "skip", c.skipped), "")
		}
		if got := line(v0110); got != c.want {
			t.Errorf("latest %q, skipped %q: the picker says %q, want %q", c.latest, c.skipped, got, c.want)
		}
	}
}

func TestAnOpenPickerPicksUpTheCheckThatJustLanded(t *testing.T) {
	dir := home(t)
	write(t, filepath.Join(dir, ".local", "state", "superfind", "shown", "version"), "0.11.0\n")
	f := Open(v0110)
	if got := f.Line(); got != "" {
		t.Fatalf("on open the picker says %q", got)
	}
	if got, changed := f.Poll(); got != "" || changed {
		t.Errorf("with nothing new Poll = %q, %v", got, changed)
	}
	check := filepath.Join(dir, ".local", "state", "superfind", "check.json")
	write(t, check, `{"next":"2999-01-01T00:00:00Z","latest":"99.0.0","failures":0}`+"\n")
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(check, later, later)
	if got, changed := f.Poll(); got != "99.0.0 is out · superfind upgrade" || !changed {
		t.Errorf("after the check landed Poll = %q, %v", got, changed)
	}
	if got, changed := f.Poll(); got != "" || changed {
		t.Errorf("polling again Poll = %q, %v", got, changed)
	}
	var none *Feed
	if got, changed := none.Poll(); got != "" || changed || none.Line() != "" {
		t.Errorf("a nil feed answered %q, %v", got, changed)
	}

	dir = home(t)
	telemetryOn(t)
	f = Open(v0110)
	if f.Line() != TelemetryNotice {
		t.Fatalf("on open the picker says %q", f.Line())
	}
	write(t, filepath.Join(dir, ".local", "state", "superfind", "check.json"), `{"next":"2999-01-01T00:00:00Z","latest":"99.0.0","failures":0}`+"\n")
	if got, changed := f.Poll(); got != "" || changed {
		t.Errorf("a check landing under the telemetry notice changed the line to %q", got)
	}
}
