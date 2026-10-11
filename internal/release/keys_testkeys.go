//go:build testkeys

package release

import (
	"embed"
	"io/fs"
	"os"
)

//go:embed testdata/test.pub
var embedded embed.FS

var keyFiles, _ = fs.Sub(embedded, "testdata")

func releasesBase() string {
	if base := os.Getenv("SUPERFIND_RELEASES_URL"); base != "" {
		return base
	}
	return githubReleases
}
