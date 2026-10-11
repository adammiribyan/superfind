//go:build testkeys

package release

import "testing"

func TestATestkeysBuildDownloadsFromSUPERFIND_RELEASES_URL(t *testing.T) {
	t.Setenv("SUPERFIND_RELEASES_URL", "http://127.0.0.1:1/releases")
	if got := DefaultSource().Base; got != "http://127.0.0.1:1/releases" {
		t.Fatalf("DefaultSource().Base = %q", got)
	}
	t.Setenv("SUPERFIND_RELEASES_URL", "")
	if got := DefaultSource().Base; got != "https://github.com/adammiribyan/superfind/releases" {
		t.Fatalf("DefaultSource().Base = %q with the variable empty", got)
	}
}

func TestATestkeysBuildTrustsOnlyTheTestKey(t *testing.T) {
	id, err := Trusted().Verify(read(t, "testdata/release/checksums.txt"), read(t, "testdata/release/checksums.txt.minisig"), mustVersion(t, "0.11.1"))
	if err != nil || id.String() != "F8D8C9C1F5F2C355" || len(trusted) != 1 {
		t.Fatalf("Verify = %s, %v with %d trusted keys", id, err, len(trusted))
	}
}
