package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"time"
)

const githubReleases = "https://github.com/adammiribyan/superfind/releases"

var FirstSigned = Version{0, 11, 1}

var UserAgent = "superfind"

var binaryLimit int64 = 128 << 20

const (
	archiveLimit = 128 << 20
	textLimit    = 1 << 20
)

type Source struct {
	Base string
	Keys Keys
}

func DefaultSource() Source { return Source{Base: releasesBase(), Keys: Trusted()} }

type Verified struct {
	version Version
	path    string
	key     KeyID
}

func (v Verified) Version() Version { return v.version }
func (v Verified) Path() string     { return v.path }
func (v Verified) Key() KeyID       { return v.key }

type ErrUnsigned struct{ Version Version }

func (e ErrUnsigned) Error() string {
	return fmt.Sprintf("%s came out before releases were signed, so superfind upgrade won't install it. To install it anyway:\n  curl -fsSL https://superfind.dev/install | SUPERFIND_VERSION=%s sh", e.Version, e.Version)
}

var errMissing = errors.New("not found")

func (s Source) Latest(ctx context.Context) (Version, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	target := s.Base + "/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return Version{}, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return Version{}, unreachable(target, err)
	}
	resp.Body.Close()
	location, err := resp.Location()
	if err != nil || resp.StatusCode < 300 || resp.StatusCode > 399 {
		return Version{}, fmt.Errorf("%s didn't point at a release (HTTP %d)", target, resp.StatusCode)
	}
	v, err := ParseVersion(path.Base(location.Path))
	if err != nil {
		return Version{}, fmt.Errorf("%s points at %s, which is not a release", target, location)
	}
	return v, nil
}

func (s Source) Download(ctx context.Context, v Version, into string, progress func(string)) (Verified, error) {
	if v.Less(FirstSigned) {
		return Verified{}, ErrUnsigned{v}
	}
	checksums, err := s.asset(ctx, v, "checksums.txt", textLimit)
	if errors.Is(err, errMissing) {
		return Verified{}, fmt.Errorf("there is no superfind %s release at %s", v, s.Base)
	}
	if err != nil {
		return Verified{}, err
	}
	sig, err := s.asset(ctx, v, "checksums.txt.minisig", textLimit)
	if errors.Is(err, errMissing) {
		return Verified{}, fmt.Errorf("superfind %s has no checksums.txt.minisig, so its signature can't be checked; nothing was installed", v)
	}
	if err != nil {
		return Verified{}, err
	}
	id, err := s.Keys.Verify(checksums, sig, v)
	if err != nil {
		return Verified{}, fmt.Errorf("%w; nothing was installed", err)
	}
	name := fmt.Sprintf("superfind_%s_%s_%s.tar.gz", v, runtime.GOOS, runtime.GOARCH)
	listed, err := parseChecksums(checksums)
	if err != nil {
		return Verified{}, err
	}
	want := ""
	for _, c := range listed {
		if c.name == name {
			want = c.sum
		}
	}
	if want == "" {
		return Verified{}, fmt.Errorf("superfind %s has no build for %s %s: checksums.txt doesn't list %s", v, runtime.GOOS, runtime.GOARCH, name)
	}
	progress(fmt.Sprintf("downloading superfind %s for %s %s", v, runtime.GOOS, runtime.GOARCH))
	archive, err := s.asset(ctx, v, name, archiveLimit)
	if errors.Is(err, errMissing) {
		return Verified{}, fmt.Errorf("superfind %s has no build for %s %s: %s is missing from the release", v, runtime.GOOS, runtime.GOARCH, name)
	}
	if err != nil {
		return Verified{}, err
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return Verified{}, fmt.Errorf("%s: sha256 is %s, but checksums.txt says %s; nothing was installed", name, got, want)
	}
	progress("checking that the new superfind runs")
	if err := extract(archive, into); err != nil {
		return Verified{}, fmt.Errorf("%s: %w; nothing was installed", name, err)
	}
	got, err := VersionOf(ctx, into)
	if err != nil {
		return Verified{}, fmt.Errorf("the downloaded superfind doesn't run here: %w; nothing was installed", err)
	}
	if got != v {
		return Verified{}, fmt.Errorf("the downloaded superfind says it is %s, not %s; nothing was installed", got, v)
	}
	return Verified{version: v, path: into, key: id}, nil
}

func VersionOf(ctx context.Context, binary string) (Version, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return Version{}, fmt.Errorf("%s --version failed: %w", binary, err)
	}
	line, ok := strings.CutPrefix(strings.TrimSuffix(string(out), "\n"), "superfind ")
	v, parsed := ParseVersion(line)
	if !ok || parsed != nil || v.String() != line {
		return Version{}, fmt.Errorf("%s --version printed %q, not a superfind version", binary, string(out))
	}
	return v, nil
}

func (s Source) asset(ctx context.Context, v Version, name string, limit int64) ([]byte, error) {
	target := fmt.Sprintf("%s/download/v%s/%s", s.Base, v, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, unreachable(target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", target, errMissing)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered HTTP %d", target, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, unreachable(target, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s is larger than %d MiB, which no superfind release is", target, limit>>20)
	}
	return body, nil
}

var client = &http.Client{Timeout: 5 * time.Minute}

func unreachable(target string, err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		err = u.Err
	}
	return fmt.Errorf("couldn't reach %s: %w", target, err)
}

func extract(archive []byte, into string) error {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("not a gzip archive: %w", err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return errors.New("the archive holds no file named superfind")
		}
		if err != nil {
			return fmt.Errorf("the archive can't be read: %w", err)
		}
		if path.Clean(h.Name) != "superfind" {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return errors.New("the archive's superfind is not a regular file")
		}
		if h.Size > binaryLimit {
			return fmt.Errorf("the archive's superfind is larger than %d MiB", binaryLimit>>20)
		}
		f, err := os.OpenFile(into, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		n, err := io.Copy(f, io.LimitReader(tr, binaryLimit+1))
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if n > binaryLimit {
			return fmt.Errorf("the archive's superfind is larger than %d MiB", binaryLimit>>20)
		}
		return os.Chmod(into, 0o755)
	}
}
