//go:build testkeys

package release

import "testing"

func TestATestkeysBuildTrustsOnlyTheTestKey(t *testing.T) {
	id, err := Trusted().Verify(read(t, "testdata/release/checksums.txt"), read(t, "testdata/release/checksums.txt.minisig"), mustVersion(t, "0.11.1"))
	if err != nil || id.String() != "F8D8C9C1F5F2C355" || len(trusted) != 1 {
		t.Fatalf("Verify = %s, %v with %d trusted keys", id, err, len(trusted))
	}
}
