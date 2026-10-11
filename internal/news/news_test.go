package news

import (
	"os"
	"path/filepath"
	"testing"

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
	return dir
}

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
		if got := Open(v0110); got != "New in 0.11.0 · Handoff, blame, and last" {
			t.Errorf("seen %q: first picker says %q, want what's new", seen, got)
		}
		if got := Open(v0110); got != "" {
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
	if got := Open(v0110); got != "" {
		t.Errorf("a fresh install's first picker says %q, want nothing", got)
	}
	if got := marker(t, dir); got != "0.11.0\n" {
		t.Errorf("marker %q, want 0.11.0", got)
	}
}

func TestGoingBackToAnOlderVersionSaysNothingAndRemembersIt(t *testing.T) {
	dir := home(t)
	write(t, filepath.Join(dir, ".local", "state", "superfind", "shown", "version"), "0.12.0\n")
	if got := Open(v0110); got != "" {
		t.Errorf("after a rollback the picker says %q, want nothing", got)
	}
	if got := marker(t, dir); got != "0.11.0\n" {
		t.Errorf("marker %q, want 0.11.0", got)
	}
}

func TestAVersionWithNoHeadlineSaysNothing(t *testing.T) {
	dir := home(t)
	if got := Open(Build{Version: v0110.Version}); got != "" {
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
	if got := Open(v0110); got != "" {
		t.Errorf("with a read-only state folder the picker says %q, want nothing", got)
	}
}
