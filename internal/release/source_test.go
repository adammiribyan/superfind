package release

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
	"strings"
	"testing"
)

type fakeReleases struct {
	latest string
	files  map[string][]byte
	seen   []string
}

func (f *fakeReleases) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.seen = append(f.seen, r.Method+" "+r.URL.Path)
	if r.URL.Path == "/latest" {
		http.Redirect(w, r, "/tag/v"+f.latest, http.StatusFound)
		return
	}
	body, ok := f.files[strings.TrimPrefix(r.URL.Path, "/download/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Write(body)
}

func publish(t *testing.T, f *fakeReleases, version string, entries map[string]tar.Header, contents map[string]string, signedAs string) {
	t.Helper()
	var archive bytes.Buffer
	zw := gzip.NewWriter(&archive)
	tw := tar.NewWriter(zw)
	for name, h := range entries {
		body := contents[name]
		h.Name, h.Size = name, int64(len(body))
		if h.Mode == 0 {
			h.Mode = 0o755
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	name := fmt.Sprintf("superfind_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive.Bytes())
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	f.files["v"+version+"/"+name] = archive.Bytes()
	f.files["v"+version+"/checksums.txt"] = checksums
	if signedAs != "" {
		f.files["v"+version+"/checksums.txt.minisig"] = Sign(checksums, testSigningKey(t), mustVersion(t, signedAs))
	}
}

func script(version string) string { return "#!/bin/sh\necho 'superfind " + version + "'\n" }

func regular() map[string]tar.Header {
	return map[string]tar.Header{"superfind": {Typeflag: tar.TypeReg}}
}

func TestLatestFollowsTheRedirectOfReleasesLatest(t *testing.T) {
	f := &fakeReleases{latest: "0.13.0", files: map[string][]byte{}}
	server := httptest.NewServer(f)
	defer server.Close()
	v, err := Source{Base: server.URL}.Latest(context.Background())
	if err != nil || v.String() != "0.13.0" {
		t.Fatalf("Latest = %s, %v", v, err)
	}
	if len(f.seen) != 1 || f.seen[0] != "HEAD /latest" {
		t.Fatalf("Latest sent %v, want one HEAD /latest", f.seen)
	}
	f.latest = "nightly"
	if _, err := (Source{Base: server.URL}).Latest(context.Background()); err == nil || !strings.HasSuffix(err.Error(), "/latest points at "+server.URL+"/tag/vnightly, which is not a release") {
		t.Fatalf("Latest with a bad tag: %v", err)
	}
	_, err = Source{Base: "http://127.0.0.1:1"}.Latest(context.Background())
	if err == nil || !strings.HasPrefix(err.Error(), "couldn't reach http://127.0.0.1:1/latest: ") {
		t.Fatalf("Latest offline: %v", err)
	}
}

func TestDownloadStagesAVerifiedBinary(t *testing.T) {
	f := &fakeReleases{latest: "0.13.0", files: map[string][]byte{}}
	publish(t, f, "0.13.0", regular(), map[string]string{"superfind": script("0.13.0")}, "0.13.0")
	server := httptest.NewServer(f)
	defer server.Close()
	into := filepath.Join(t.TempDir(), ".superfind.upgrade.1")
	var progress []string
	got, err := Source{Base: server.URL, Keys: testKeys(t)}.Download(context.Background(), mustVersion(t, "0.13.0"), into, func(s string) { progress = append(progress, s) })
	if err != nil {
		t.Fatal(err)
	}
	if got.Version().String() != "0.13.0" || got.Path() != into || got.Key().String() != "F8D8C9C1F5F2C355" {
		t.Fatalf("Download = %+v", got)
	}
	if body, _ := os.ReadFile(into); string(body) != script("0.13.0") {
		t.Fatalf("staged file holds %q", body)
	}
	if info, _ := os.Stat(into); info.Mode().Perm() != 0o755 {
		t.Fatalf("staged file mode %o, want 755", info.Mode().Perm())
	}
	want := []string{"downloading superfind 0.13.0 for " + runtime.GOOS + " " + runtime.GOARCH, "checking that the new superfind runs"}
	if strings.Join(progress, "|") != strings.Join(want, "|") {
		t.Fatalf("progress = %q", progress)
	}
	wantSeen := []string{"GET /download/v0.13.0/checksums.txt", "GET /download/v0.13.0/checksums.txt.minisig", "GET /download/v0.13.0/superfind_0.13.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"}
	if strings.Join(f.seen, "|") != strings.Join(wantSeen, "|") {
		t.Fatalf("requests = %q", f.seen)
	}
}

func TestDownloadRefusesWhatItCannotVerify(t *testing.T) {
	binaryLimit = 64
	t.Cleanup(func() { binaryLimit = 128 << 20 })
	platform := runtime.GOOS + "_" + runtime.GOARCH
	for _, c := range []struct {
		name    string
		version string
		publish func(f *fakeReleases)
		want    string
	}{
		{"an unsigned release", "0.11.0", func(*fakeReleases) {},
			"0.11.0 came out before releases were signed, so superfind upgrade won't install it. To install it anyway:\n  curl -fsSL https://superfind.dev/install | SUPERFIND_VERSION=0.11.0 sh"},
		{"a release that doesn't exist", "9.9.9", func(*fakeReleases) {}, "there is no superfind 9.9.9 release at "},
		{"a missing signature", "0.13.0", func(f *fakeReleases) {
			publish(t, f, "0.13.0", regular(), map[string]string{"superfind": script("0.13.0")}, "")
		}, "superfind 0.13.0 has no checksums.txt.minisig, so its signature can't be checked; nothing was installed"},
		{"a signature for another version", "0.13.0", func(f *fakeReleases) {
			publish(t, f, "0.13.0", regular(), map[string]string{"superfind": script("0.13.0")}, "0.12.0")
		}, `checksums.txt.minisig is signed for "superfind v0.12.0 checksums.txt", not "superfind v0.13.0 checksums.txt"; nothing was installed`},
		{"a tampered checksums.txt", "0.13.0", func(f *fakeReleases) {
			publish(t, f, "0.13.0", regular(), map[string]string{"superfind": script("0.13.0")}, "0.13.0")
			f.files["v0.13.0/checksums.txt"][0] ^= 1
		}, "checksums.txt.minisig does not match checksums.txt; nothing was installed"},
		{"a tampered archive", "0.13.0", func(f *fakeReleases) {
			publish(t, f, "0.13.0", regular(), map[string]string{"superfind": script("0.13.0")}, "0.13.0")
			f.files["v0.13.0/superfind_0.13.0_"+platform+".tar.gz"] = append(f.files["v0.13.0/superfind_0.13.0_"+platform+".tar.gz"], 0)
		}, "superfind_0.13.0_" + platform + ".tar.gz: sha256 is "},
		{"no build for this machine", "0.13.0", func(f *fakeReleases) {
			publish(t, f, "0.13.0", regular(), map[string]string{"superfind": script("0.13.0")}, "0.13.0")
			f.files["v0.13.0/checksums.txt"] = []byte(strings.Repeat("a", 64) + "  superfind_0.13.0_plan9_mips.tar.gz\n")
			f.files["v0.13.0/checksums.txt.minisig"] = Sign(f.files["v0.13.0/checksums.txt"], testSigningKey(t), mustVersion(t, "0.13.0"))
		}, "superfind 0.13.0 has no build for " + runtime.GOOS + " " + runtime.GOARCH + ": checksums.txt doesn't list superfind_0.13.0_" + platform + ".tar.gz"},
		{"an archive without superfind", "0.13.0", func(f *fakeReleases) {
			publish(t, f, "0.13.0", map[string]tar.Header{"LICENSE": {Typeflag: tar.TypeReg}}, map[string]string{"LICENSE": "x"}, "0.13.0")
		}, "superfind_0.13.0_" + platform + ".tar.gz: " + "the archive holds no file named superfind; nothing was installed"},
		{"a superfind that is a symlink", "0.13.0", func(f *fakeReleases) {
			publish(t, f, "0.13.0", map[string]tar.Header{"superfind": {Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"}}, nil, "0.13.0")
		}, "superfind_0.13.0_" + platform + ".tar.gz: " + "the archive's superfind is not a regular file; nothing was installed"},
		{"a superfind that escapes the folder", "0.13.0", func(f *fakeReleases) {
			publish(t, f, "0.13.0", map[string]tar.Header{"../superfind": {Typeflag: tar.TypeReg}}, map[string]string{"../superfind": script("0.13.0")}, "0.13.0")
		}, "superfind_0.13.0_" + platform + ".tar.gz: " + "the archive holds no file named superfind; nothing was installed"},
		{"a superfind over the size limit", "0.13.0", func(f *fakeReleases) {
			publish(t, f, "0.13.0", regular(), map[string]string{"superfind": script("0.13.0") + strings.Repeat("#", 64)}, "0.13.0")
		}, "superfind_0.13.0_" + platform + ".tar.gz: " + "the archive's superfind is larger than 0 MiB; nothing was installed"},
		{"a superfind that lies about its version", "0.13.0", func(f *fakeReleases) {
			publish(t, f, "0.13.0", regular(), map[string]string{"superfind": script("0.12.0")}, "0.13.0")
		}, "the downloaded superfind says it is 0.12.0, not 0.13.0; nothing was installed"},
		{"a superfind that doesn't run", "0.13.0", func(f *fakeReleases) {
			publish(t, f, "0.13.0", regular(), map[string]string{"superfind": "#!/bin/sh\nexit 3\n"}, "0.13.0")
		}, "the downloaded superfind doesn't run here: "},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeReleases{latest: "0.13.0", files: map[string][]byte{}}
			c.publish(f)
			server := httptest.NewServer(f)
			defer server.Close()
			into := filepath.Join(t.TempDir(), ".superfind.upgrade.1")
			_, err := Source{Base: server.URL, Keys: testKeys(t)}.Download(context.Background(), mustVersion(t, c.version), into, func(string) {})
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Fatalf("Download error = %v\nwant a prefix of %s", err, c.want)
			}
		})
	}
}

func TestVersionOfRequiresExactlyOneSuperfindVersionLine(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ prints, want string }{
		{"superfind 0.13.0", "0.13.0"},
		{"superfind v0.13.0", ""},
		{"superfind 0.13.0\\nhello", ""},
		{"0.13.0", ""},
		{"superfind 01.13.0", ""},
	} {
		binary := filepath.Join(dir, "superfind")
		os.WriteFile(binary, []byte("#!/bin/sh\nprintf '"+c.prints+"\\n'\n"), 0o755)
		v, err := VersionOf(context.Background(), binary)
		if c.want == "" && err == nil {
			t.Errorf("VersionOf(%q) = %s, want an error", c.prints, v)
		}
		if c.want != "" && (err != nil || v.String() != c.want) {
			t.Errorf("VersionOf(%q) = %s, %v", c.prints, v, err)
		}
	}
}
