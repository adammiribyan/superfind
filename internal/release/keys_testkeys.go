//go:build testkeys

package release

import (
	"embed"
	"io/fs"
)

//go:embed testdata/test.pub
var embedded embed.FS

var keyFiles, _ = fs.Sub(embedded, "testdata")
