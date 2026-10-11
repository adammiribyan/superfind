package news

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/adammiribyan/superfind/internal/release"
	"github.com/adammiribyan/superfind/internal/store"
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

func versionMarker() string { return store.StatePath(filepath.Join("shown", "version")) }

func Arrive(v release.Version) {
	if _, err := os.Stat(store.CachePath("index.json")); !errors.Is(err, fs.ErrNotExist) {
		return
	}
	store.CreateOnce(versionMarker(), []byte(v.String()+"\n"))
}

func Open(b Build) string {
	whatsNew, stale := whatsNewState(b)
	switch pick(due{whatsNew: whatsNew}) {
	case WhatsNew:
		if markVersion(b.Version) != nil {
			return ""
		}
		return "New in " + b.Version.String() + " · " + b.Headline
	}
	if stale && !whatsNew {
		markVersion(b.Version)
	}
	return ""
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

func Poll() (line string, changed bool) { return "", false }
