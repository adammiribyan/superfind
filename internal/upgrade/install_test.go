package upgrade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adammiribyan/superfind/internal/release"
)

func TestDetectTellsTheInstallMethodFromThePathThenTheReceiptThenTheBuild(t *testing.T) {
	exe := "/home/ada/.local/bin/superfind"
	for _, c := range []struct {
		name  string
		facts Facts
		want  Install
	}{
		{"a Homebrew cask", Facts{Executable: "/opt/homebrew/Caskroom/superfind/0.12.0/superfind"},
			Install{Method: Homebrew, Path: "/opt/homebrew/Caskroom/superfind/0.12.0/superfind"}},
		{"a Homebrew cask beats a receipt for the same path", Facts{Executable: "/opt/homebrew/Caskroom/superfind/0.12.0/superfind",
			Receipt: &Receipt{Method: Script, Path: "/opt/homebrew/Caskroom/superfind/0.12.0/superfind"}},
			Install{Method: Homebrew, Path: "/opt/homebrew/Caskroom/superfind/0.12.0/superfind"}},
		{"the install script's receipt", Facts{Executable: exe, Receipt: &Receipt{Method: Script, Path: exe}},
			Install{Method: Script, Path: exe}},
		{"a clone's receipt", Facts{Executable: exe, Receipt: &Receipt{Method: Checkout, Path: exe, Clone: "/home/ada/src/superfind"}},
			Install{Method: Checkout, Path: exe, Clone: "/home/ada/src/superfind"}},
		{"a receipt for another path is ignored", Facts{Executable: "/usr/local/bin/superfind", Receipt: &Receipt{Method: Script, Path: exe}},
			Install{Method: Unknown, Path: "/usr/local/bin/superfind"}},
		{"a receipt beats build info", Facts{Executable: exe, Receipt: &Receipt{Method: Script, Path: exe}, ModuleSum: "h1:abc", FromClone: true},
			Install{Method: Script, Path: exe}},
		{"go install", Facts{Executable: "/home/ada/go/bin/superfind", ModuleSum: "h1:6bMVaeDx95YM292WCqFwZtUVEQPV3if7Cz/iCRljovA="},
			Install{Method: GoInstall, Path: "/home/ada/go/bin/superfind"}},
		{"go build in a clone, copied anywhere", Facts{Executable: "/usr/local/bin/superfind", FromClone: true},
			Install{Method: Checkout, Path: "/usr/local/bin/superfind"}},
		{"anything else", Facts{Executable: "/usr/local/bin/superfind"},
			Install{Method: Unknown, Path: "/usr/local/bin/superfind"}},
	} {
		if got := Detect(c.facts); got != c.want {
			t.Errorf("%s: Detect = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestTheReceiptRoundTripsAndResolvesSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	os.MkdirAll(filepath.Join(home, "real"), 0o755)
	os.WriteFile(filepath.Join(home, "real", "superfind"), []byte("#!/bin/sh\n"), 0o755)
	os.Symlink(filepath.Join(home, "real"), filepath.Join(home, "bin"))
	if got := ReadReceipt(); got != nil {
		t.Fatalf("ReadReceipt with no receipt = %+v", got)
	}
	if err := WriteReceipt(Receipt{Method: Script, Path: filepath.Join(home, "bin", "superfind")}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "state", "superfind", "install.json"))
	if want := `{"method":"script","path":"` + filepath.Join(home, "bin", "superfind") + `"}` + "\n"; string(raw) != want {
		t.Fatalf("install.json holds %q, want %q", raw, want)
	}
	real, _ := filepath.EvalSymlinks(filepath.Join(home, "real", "superfind"))
	if got := ReadReceipt(); got == nil || *got != (Receipt{Method: Script, Path: real}) {
		t.Fatalf("ReadReceipt = %+v, want the real path %s", got, real)
	}
	for _, bad := range []string{`{"method":"homebrew","path":"/x"}`, `{"method":"script","path":"relative"}`, `{"method":"script"`, ``} {
		os.WriteFile(filepath.Join(home, "state", "superfind", "install.json"), []byte(bad), 0o600)
		if got := ReadReceipt(); got != nil {
			t.Errorf("ReadReceipt of %q = %+v, want nil", bad, got)
		}
	}
}

func TestEachInstallMethodGetsItsOwnCommand(t *testing.T) {
	pinned := release.Version{Major: 0, Minor: 12, Patch: 0}
	for _, c := range []struct {
		install Install
		v       *release.Version
		want    string
	}{
		{Install{Method: Homebrew}, nil, "brew upgrade --cask adammiribyan/tap/superfind"},
		{Install{Method: Homebrew}, &pinned, "brew upgrade --cask adammiribyan/tap/superfind"},
		{Install{Method: GoInstall}, nil, "go install github.com/adammiribyan/superfind@latest"},
		{Install{Method: GoInstall}, &pinned, "go install github.com/adammiribyan/superfind@v0.12.0"},
		{Install{Method: Checkout, Clone: "/home/ada/src/superfind"}, nil, "git -C /home/ada/src/superfind pull && /home/ada/src/superfind/install.sh"},
		{Install{Method: Checkout, Clone: "/home/ada/my src/superfind"}, &pinned,
			"git -C '/home/ada/my src/superfind' fetch --tags && git -C '/home/ada/my src/superfind' checkout v0.12.0 && '/home/ada/my src/superfind'/install.sh"},
		{Install{Method: Checkout}, nil, "git pull && ./install.sh"},
		{Install{Method: Unknown, Path: "/opt/superfind"}, nil, "curl -fsSL https://superfind.dev/install | sh"},
		{Install{Method: Unknown, Path: "/opt/superfind"}, &pinned, "curl -fsSL https://superfind.dev/install | SUPERFIND_VERSION=0.12.0 sh"},
	} {
		if got := c.install.Command(c.v); got != c.want {
			t.Errorf("%s with %v: Command = %q, want %q", c.install.Method, c.v, got, c.want)
		}
	}
	got := Install{Method: Checkout}.leftAlone(nil)
	if want := "this copy was built from a clone of superfind, so superfind left it alone. To upgrade, in that clone:\n  git pull && ./install.sh"; got != want {
		t.Errorf("leftAlone = %q, want %q", got, want)
	}
	got = Install{Method: Unknown, Path: "/opt/superfind"}.leftAlone(&pinned)
	if want := "superfind doesn't know what installed this copy at /opt/superfind, so superfind left it alone. To install 0.12.0 in ~/.local/bin:\n  curl -fsSL https://superfind.dev/install | SUPERFIND_VERSION=0.12.0 sh"; got != want {
		t.Errorf("leftAlone = %q, want %q", got, want)
	}
}
