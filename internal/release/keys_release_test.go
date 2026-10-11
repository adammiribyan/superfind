//go:build !testkeys

package release

import (
	"os"
	"testing"
)

func TestAReleaseKeyIsCommitted(t *testing.T) {
	if len(trusted) > 0 {
		return
	}
	if os.Getenv("SUPERFIND_ALLOW_NO_RELEASE_KEY") == "1" {
		t.Skip("internal/release/keys has no release key; SUPERFIND_ALLOW_NO_RELEASE_KEY=1 allows that")
	}
	t.Fatal("internal/release/keys has no release key, so no release could be verified. Run `go run ./internal/release/cmd/keygen ~/superfind-release.key > internal/release/keys/superfind-2026.pub` (CONTRIBUTING.md, Releasing), or set SUPERFIND_ALLOW_NO_RELEASE_KEY=1 to run the tests without one")
}
