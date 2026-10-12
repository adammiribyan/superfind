package news

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/adammiribyan/superfind/internal/release"
	"github.com/adammiribyan/superfind/internal/store"
	"github.com/adammiribyan/superfind/internal/telemetry"
)

type Build struct {
	Version  release.Version
	Headline string
}

type Notice int

const (
	None Notice = iota
	TelemetryOn
	WhatsNew
	UpdateOut
)

const TelemetryNotice = "Daily anonymous check-ins are on · superfind telemetry"

type due struct {
	telemetry, whatsNew, update bool
}

func pick(d due) Notice {
	switch {
	case d.telemetry:
		return TelemetryOn
	case d.whatsNew:
		return WhatsNew
	case d.update:
		return UpdateOut
	}
	return None
}

func versionMarker() string   { return store.StatePath(filepath.Join("shown", "version")) }
func telemetryMarker() string { return store.StatePath(filepath.Join("shown", "telemetry")) }

func Arrive(v release.Version) {
	if _, err := os.Stat(store.CachePath("index.json")); !errors.Is(err, fs.ErrNotExist) {
		return
	}
	store.CreateOnce(versionMarker(), []byte(v.String()+"\n"))
}

type Feed struct {
	build  Build
	line   string
	pinned bool
	seen   time.Time
}

func Open(b Build) *Feed {
	start := time.Now()
	f := &Feed{build: b}
	setting := telemetry.Current()
	toldAt := stamp(telemetryMarker())
	whatsNew, stale := whatsNewState(b)
	check, written := telemetry.LastCheck()
	f.seen = written
	switch pick(due{telemetry: setting.On() && toldAt.IsZero(), whatsNew: whatsNew, update: updateDue(b.Version, check.Latest)}) {
	case TelemetryOn:
		if Tell() == nil {
			f.line, f.pinned = TelemetryNotice, true
		}
	case WhatsNew:
		if markVersion(b.Version) == nil {
			f.line, f.pinned = "New in "+b.Version.String()+" · "+b.Headline, true
		}
	case UpdateOut:
		f.line = updateLine(check.Latest)
	}
	if stale && !whatsNew {
		markVersion(b.Version)
	}
	if setting.On() && !toldAt.IsZero() && toldAt.Before(start) && telemetry.Due(check, start) {
		spawn()
	}
	return f
}

func (f *Feed) Line() string {
	if f == nil {
		return ""
	}
	return f.line
}

func (f *Feed) Poll() (line string, changed bool) {
	if f == nil || f.pinned {
		return "", false
	}
	check, written := telemetry.LastCheck()
	if !written.After(f.seen) {
		return "", false
	}
	f.seen = written
	if updateDue(f.build.Version, check.Latest) {
		line = updateLine(check.Latest)
	}
	if line == f.line {
		return "", false
	}
	f.line = line
	return line, true
}

func Tell() error {
	path := telemetryMarker()
	if err := store.MakeDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	now := time.Now()
	return os.Chtimes(path, now, now)
}

func Told() bool { return !stamp(telemetryMarker()).IsZero() }

func stamp(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func updateDue(current release.Version, latest string) bool {
	v, err := release.ParseVersion(latest)
	if err != nil || v.String() != latest || !current.Less(v) {
		return false
	}
	_, err = os.Stat(store.StatePath(filepath.Join("skip", v.String())))
	return errors.Is(err, fs.ErrNotExist)
}

func updateLine(latest string) string { return latest + " is out · superfind upgrade" }

var spawn = func() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "checkin")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Start()
}

func markVersion(v release.Version) error {
	return store.WriteAtomically(versionMarker(), []byte(v.String()+"\n"))
}

func whatsNewState(b Build) (due, stale bool) {
	raw, err := os.ReadFile(versionMarker())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, false
	}
	seen, bad := release.ParseVersion(strings.TrimSpace(string(raw)))
	if err == nil && bad == nil {
		if seen == b.Version {
			return false, false
		}
		return seen.Less(b.Version) && b.Headline != "", true
	}
	return b.Headline != "", true
}
