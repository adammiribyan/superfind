//go:build !testkeys

package release

import (
	"os"
	"testing"
)

func TestAReleaseBuildDownloadsFromGitHubWhateverTheEnvironmentSays(t *testing.T) {
	t.Setenv("SUPERFIND_RELEASES_URL", "http://127.0.0.1:1/releases")
	if got := DefaultSource().Base; got != "https://github.com/adammiribyan/superfind/releases" {
		t.Fatalf("DefaultSource().Base = %q with SUPERFIND_RELEASES_URL set", got)
	}
}

func TestAReleaseKeyIsCommitted(t *testing.T) {
	if len(trusted) > 0 {
		return
	}
	if os.Getenv("SUPERFIND_ALLOW_NO_RELEASE_KEY") == "1" {
		t.Skip("internal/release/keys has no release key; SUPERFIND_ALLOW_NO_RELEASE_KEY=1 allows that")
	}
	t.Fatal("internal/release/keys has no release key, so no release could be verified. Run `go run ./internal/release/cmd/keygen ~/superfind-release.key > internal/release/keys/superfind-2026.pub` (CONTRIBUTING.md, Releasing), or set SUPERFIND_ALLOW_NO_RELEASE_KEY=1 to run the tests without one")
}
