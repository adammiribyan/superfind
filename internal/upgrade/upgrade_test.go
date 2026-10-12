package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/adammiribyan/superfind/internal/release"
	"github.com/adammiribyan/superfind/internal/store"
)

type releases struct {
	latest string
	files  map[string][]byte
	seen   []string
}

func (r *releases) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.seen = append(r.seen, req.Method+" "+req.URL.Path)
	if req.URL.Path == "/latest" {
		http.Redirect(w, req, "/tag/v"+r.latest, http.StatusFound)
		return
	}
	body, ok := r.files[strings.TrimPrefix(req.URL.Path, "/download/")]
	if !ok {
		http.NotFound(w, req)
		return
	}
	w.Write(body)
}

func script(version string) string { return "#!/bin/sh\necho 'superfind " + version + "'\n" }

func (r *releases) publish(t *testing.T, version string) {
	t.Helper()
	var archive bytes.Buffer
	zw := gzip.NewWriter(&archive)
	tw := tar.NewWriter(zw)
	body := script(version)
	tw.WriteHeader(&tar.Header{Name: "superfind", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))})
	tw.Write([]byte(body))
	tw.Close()
	zw.Close()
	name := fmt.Sprintf("superfind_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive.Bytes())
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	key, err := release.ParseSecretKey(read(t, "../release/testdata/test.key"))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := release.ParseVersion(version)
	r.files["v"+version+"/"+name] = archive.Bytes()
	r.files["v"+version+"/checksums.txt"] = checksums
	r.files["v"+version+"/checksums.txt.minisig"] = release.Sign(checksums, key, v)
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type fixture struct {
	home, bin, state string
	server           *httptest.Server
	releases         *releases
	upgrader         Upgrader
	progress         []string
}

func setUp(t *testing.T, current, latest string) *fixture {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{home: home}
	f.bin = filepath.Join(f.home, ".local", "bin")
	f.state = filepath.Join(f.home, ".local", "state", "superfind")
	t.Setenv("XDG_STATE_HOME", filepath.Join(f.home, ".local", "state"))
	os.MkdirAll(f.bin, 0o755)
	os.WriteFile(filepath.Join(f.bin, "superfind"), []byte(script(current)), 0o755)
	if err := WriteReceipt(Receipt{Method: Script, Path: filepath.Join(f.bin, "superfind")}); err != nil {
		t.Fatal(err)
	}
	f.releases = &releases{latest: latest, files: map[string][]byte{}}
	f.server = httptest.NewServer(f.releases)
	t.Cleanup(f.server.Close)
	pub, err := release.ParsePublicKey(read(t, "../release/testdata/test.pub"))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := release.ParseVersion(current)
	f.upgrader = Upgrader{Current: v, Install: Detect(Facts{Executable: filepath.Join(f.bin, "superfind"), Receipt: ReadReceipt()}),
		Source: release.Source{Base: f.server.URL, Keys: release.Keys{pub.ID: pub}}, Progress: func(s string) { f.progress = append(f.progress, s) }}
	return f
}

func (f *fixture) installed(t *testing.T) string {
	t.Helper()
	v, err := release.VersionOf(context.Background(), filepath.Join(f.bin, "superfind"))
	if err != nil {
		t.Fatal(err)
	}
	return v.String()
}

func (f *fixture) previous(t *testing.T) string {
	t.Helper()
	v, err := release.VersionOf(context.Background(), filepath.Join(f.state, "previous", "superfind"))
	if err != nil {
		return ""
	}
	return v.String()
}

func (f *fixture) listing(dir string) []string {
	entries, _ := os.ReadDir(dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestUpgradeReplacesTheBinaryKeepsTheOldOneAndRollsBackTwice(t *testing.T) {
	f := setUp(t, "0.12.0", "0.13.0")
	f.releases.publish(t, "0.13.0")
	receipt, _ := os.ReadFile(filepath.Join(f.state, "install.json"))
	os.WriteFile(filepath.Join(f.bin, ".superfind.upgrade.2147483646"), nil, 0o755)
	os.WriteFile(filepath.Join(f.bin, ".superfind.upgrade.1"), nil, 0o755)

	got, err := f.upgrader.To(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "superfind 0.12.0 → 0.13.0, signed and verified\nundo with superfind upgrade --rollback"
	if got != want {
		t.Fatalf("To = %q\nwant %q", got, want)
	}
	if f.installed(t) != "0.13.0" || f.previous(t) != "0.12.0" {
		t.Fatalf("installed %s, previous %s", f.installed(t), f.previous(t))
	}
	if names := f.listing(f.bin); strings.Join(names, " ") != ".superfind.upgrade.1 superfind" {
		t.Fatalf("bin holds %v: the dead pid's staged file should be swept and the live pid's kept", names)
	}
	if after, _ := os.ReadFile(filepath.Join(f.state, "install.json")); !bytes.Equal(after, receipt) {
		t.Fatalf("the receipt changed from %q to %q", receipt, after)
	}
	if names := f.listing(f.state); strings.Join(names, " ") != "install.json previous upgrade.lock" {
		t.Fatalf("state holds %v", names)
	}
	if strings.Join(f.progress, "|") != "checking the newest release|downloading superfind 0.13.0 for "+runtime.GOOS+" "+runtime.GOARCH+"|checking that the new superfind runs" {
		t.Fatalf("progress = %q", f.progress)
	}

	f.upgrader.Current, _ = release.ParseVersion("0.13.0")
	got, err = f.upgrader.Rollback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "rolled back superfind 0.13.0 → 0.12.0\nundo with superfind upgrade --rollback"; got != want {
		t.Fatalf("Rollback = %q\nwant %q", got, want)
	}
	if f.installed(t) != "0.12.0" || f.previous(t) != "0.13.0" {
		t.Fatalf("after rollback: installed %s, previous %s", f.installed(t), f.previous(t))
	}
	if _, err := os.Stat(filepath.Join(f.state, "skip", "0.13.0")); err != nil {
		t.Fatalf("no skip marker for the version the user left: %v", err)
	}

	f.upgrader.Current, _ = release.ParseVersion("0.12.0")
	got, err = f.upgrader.Rollback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "rolled back superfind 0.12.0 → 0.13.0\nundo with superfind upgrade --rollback"; got != want {
		t.Fatalf("second Rollback = %q\nwant %q", got, want)
	}
	if f.installed(t) != "0.13.0" || f.previous(t) != "0.12.0" {
		t.Fatalf("after rolling forward: installed %s, previous %s", f.installed(t), f.previous(t))
	}
	if names := f.listing(filepath.Join(f.state, "previous")); strings.Join(names, " ") != "superfind" {
		t.Fatalf("previous holds %v", names)
	}
	if names := f.listing(filepath.Join(f.state, "skip")); strings.Join(names, " ") != "0.13.0" {
		t.Fatalf("skip holds %v", names)
	}
}

func TestUpgradeHasNothingToDoWhenCurrentOrNothingKept(t *testing.T) {
	f := setUp(t, "0.13.0", "0.13.0")
	got, err := f.upgrader.To(context.Background(), nil)
	if err != nil || got != "superfind 0.13.0 is already installed" {
		t.Fatalf("To = %q, %v", got, err)
	}
	got, err = f.upgrader.Rollback(context.Background())
	if err != nil || got != "nothing to roll back to: superfind upgrade keeps the copy it replaces, and it hasn't replaced one here" {
		t.Fatalf("Rollback = %q, %v", got, err)
	}
	if names := f.listing(f.state); strings.Join(names, " ") != "install.json upgrade.lock" {
		t.Fatalf("state holds %v", names)
	}
}

func TestAPinnedOlderReleaseIsADowngradeThatMarksTheVersionLeft(t *testing.T) {
	f := setUp(t, "0.13.0", "0.13.0")
	f.releases.publish(t, "0.12.0")
	pinned, _ := release.ParseVersion("0.12.0")
	got, err := f.upgrader.To(context.Background(), &pinned)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "superfind 0.13.0 → 0.12.0, signed and verified\n") {
		t.Fatalf("To = %q", got)
	}
	if f.installed(t) != "0.12.0" || f.previous(t) != "0.13.0" {
		t.Fatalf("installed %s, previous %s", f.installed(t), f.previous(t))
	}
	if names := f.listing(filepath.Join(f.state, "skip")); strings.Join(names, " ") != "0.13.0" {
		t.Fatalf("skip holds %v", names)
	}
	if strings.Join(f.releases.seen, " ") != "GET /download/v0.12.0/checksums.txt GET /download/v0.12.0/checksums.txt.minisig GET /download/v0.12.0/superfind_0.12.0_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz" {
		t.Fatalf("a pinned version still asked for the latest: %v", f.releases.seen)
	}
}

func TestAFailedDownloadLeavesEverythingAsItWas(t *testing.T) {
	f := setUp(t, "0.12.0", "0.13.0")
	f.releases.publish(t, "0.13.0")
	f.releases.files["v0.13.0/checksums.txt"][0] ^= 1
	before, _ := os.ReadFile(filepath.Join(f.bin, "superfind"))
	_, err := f.upgrader.To(context.Background(), nil)
	if err == nil || err.Error() != "checksums.txt.minisig does not match checksums.txt; nothing was installed" {
		t.Fatalf("To = %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(f.bin, "superfind"))
	if !bytes.Equal(before, after) || strings.Join(f.listing(f.bin), " ") != "superfind" {
		t.Fatalf("bin holds %v after a refused upgrade", f.listing(f.bin))
	}
	if f.previous(t) != "" {
		t.Fatalf("previous was written before the download was verified")
	}
}

func TestAnUnwritableBinFolderStopsBeforeAnyDownload(t *testing.T) {
	f := setUp(t, "0.12.0", "0.13.0")
	f.releases.publish(t, "0.13.0")
	os.Chmod(f.bin, 0o500)
	t.Cleanup(func() { os.Chmod(f.bin, 0o755) })
	_, err := f.upgrader.To(context.Background(), nil)
	want := "can't write to " + f.bin + " (permission denied), so superfind was left as it is. To install the newest release in ~/.local/bin:\n  curl -fsSL https://superfind.dev/install | sh"
	if err == nil || err.Error() != want {
		t.Fatalf("To = %v\nwant %s", err, want)
	}
	if strings.Join(f.releases.seen, " ") != "HEAD /latest" {
		t.Fatalf("requests = %v, want only the version check", f.releases.seen)
	}
}

func TestASecondUpgradeAtOnceIsRefused(t *testing.T) {
	f := setUp(t, "0.12.0", "0.13.0")
	unlock, held, err := store.TryLock(filepath.Join(f.state, "upgrade.lock"))
	if err != nil || !held {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := f.upgrader.To(context.Background(), nil); err == nil || err.Error() != "another superfind upgrade is running" {
		t.Fatalf("To = %v", err)
	}
	if _, err := f.upgrader.Rollback(context.Background()); err == nil || err.Error() != "another superfind upgrade is running" {
		t.Fatalf("Rollback = %v", err)
	}
	if len(f.releases.seen) != 0 {
		t.Fatalf("requests = %v", f.releases.seen)
	}
}

func TestCheckSaysWhatIsOutAndHowToGetItAndChangesNothing(t *testing.T) {
	f := setUp(t, "0.12.0", "0.13.0")
	before := f.listing(f.state)
	for _, c := range []struct {
		install Install
		want    string
	}{
		{f.upgrader.Install, "superfind 0.13.0 is out; you have 0.12.0. superfind upgrade installs it"},
		{Install{Method: Homebrew, Path: "/opt/homebrew/Caskroom/superfind/0.12.0/superfind"},
			"superfind 0.13.0 is out; you have 0.12.0. Homebrew installed this copy, so install it with: brew upgrade --cask adammiribyan/tap/superfind"},
		{Install{Method: GoInstall, Path: "/home/ada/go/bin/superfind"},
			"superfind 0.13.0 is out; you have 0.12.0. go install built this copy, so install it with: go install github.com/adammiribyan/superfind@latest"},
		{Install{Method: Unknown, Path: "/opt/superfind"},
			"superfind 0.13.0 is out; you have 0.12.0. superfind doesn't know what installed this copy at /opt/superfind, so install it with: curl -fsSL https://superfind.dev/install | sh"},
	} {
		u := f.upgrader
		u.Install = c.install
		got, err := u.Check(context.Background())
		if err != nil || got != c.want {
			t.Errorf("%s: Check = %q, %v\nwant %s", c.install.Method, got, err, c.want)
		}
	}
	f.releases.latest = "0.12.0"
	if got, _ := f.upgrader.Check(context.Background()); got != "superfind 0.12.0 is up to date" {
		t.Errorf("Check = %q", got)
	}
	f.releases.latest = "0.11.1"
	if got, _ := f.upgrader.Check(context.Background()); got != "superfind 0.12.0 is newer than the newest release, 0.11.1" {
		t.Errorf("Check = %q", got)
	}
	if after := f.listing(f.state); strings.Join(after, " ") != strings.Join(before, " ") || f.installed(t) != "0.12.0" {
		t.Fatalf("--check changed the state folder from %v to %v", before, after)
	}
}

func TestOtherInstallMethodsAreLeftAloneWithTheirCommand(t *testing.T) {
	f := setUp(t, "0.12.0", "0.13.0")
	pinned, _ := release.ParseVersion("0.12.0")
	for _, c := range []struct {
		install  Install
		pinned   *release.Version
		to, back string
	}{
		{Install{Method: Homebrew, Path: "/opt/homebrew/Caskroom/superfind/0.12.0/superfind"}, nil,
			"Homebrew installed this copy, so superfind left it alone. To upgrade:\n  brew upgrade --cask adammiribyan/tap/superfind",
			"Homebrew installed this copy, so superfind upgrade never replaced it, and there's nothing to roll back"},
		{Install{Method: GoInstall, Path: "/home/ada/go/bin/superfind"}, &pinned,
			"go install built this copy, so superfind left it alone. To install 0.12.0:\n  go install github.com/adammiribyan/superfind@v0.12.0",
			"go install built this copy, so superfind upgrade never replaced it, and there's nothing to roll back"},
		{Install{Method: Checkout, Path: "/home/ada/.local/bin/superfind", Clone: "/home/ada/src/superfind"}, nil,
			"this copy was built from the superfind clone at /home/ada/src/superfind, so superfind left it alone. To upgrade:\n  git -C /home/ada/src/superfind pull && /home/ada/src/superfind/install.sh",
			"this copy was built from the superfind clone at /home/ada/src/superfind, so superfind upgrade never replaced it, and there's nothing to roll back"},
		{Install{Method: Unknown, Path: "/opt/superfind"}, nil,
			"superfind doesn't know what installed this copy at /opt/superfind, so superfind left it alone. To upgrade in ~/.local/bin:\n  curl -fsSL https://superfind.dev/install | sh",
			"superfind doesn't know what installed this copy at /opt/superfind, so superfind upgrade never replaced it, and there's nothing to roll back"},
	} {
		u := f.upgrader
		u.Install = c.install
		got, err := u.To(context.Background(), c.pinned)
		if err != nil || got != c.to {
			t.Errorf("%s: To = %q, %v\nwant %s", c.install.Method, got, err, c.to)
		}
		got, err = u.Rollback(context.Background())
		if err != nil || got != c.back {
			t.Errorf("%s: Rollback = %q, %v\nwant %s", c.install.Method, got, err, c.back)
		}
	}
	if len(f.releases.seen) != 0 || f.installed(t) != "0.12.0" {
		t.Fatalf("requests = %v, installed %s", f.releases.seen, f.installed(t))
	}
}
