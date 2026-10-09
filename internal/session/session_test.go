package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheCacheStaysPutWithoutHOME(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	os.Unsetenv("HOME")
	if dir := CacheDir(); !filepath.IsAbs(dir) {
		t.Fatalf("with HOME unset the cache would go to %q, inside whatever folder superfind runs from", dir)
	}
}
