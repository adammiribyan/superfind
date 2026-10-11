//go:build !testkeys

package release

import (
	"embed"
	"io/fs"
)

//go:embed keys
var embedded embed.FS

var keyFiles, _ = fs.Sub(embedded, "keys")
