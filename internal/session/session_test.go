package session

import (
	"os"
	"path/filepath"
	"slices"
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

func TestASessionIsHereFromItsFolderAndEverySubfolder(t *testing.T) {
	session := Session{Cwd: "/tmp/app"}
	for pwd, want := range map[string]bool{"/tmp/app": true, "/tmp/app/web/src": true, "/tmp/application": false, "/tmp": false} {
		t.Setenv("PWD", pwd)
		if got := IsHere(session); got != want {
			t.Errorf("from %s, IsHere = %v, want %v", pwd, got, want)
		}
	}
	t.Setenv("PWD", "/tmp/app")
	if IsHere(Session{}) {
		t.Error("a session with no folder counted as here")
	}
}

func TestOnlyTheRootAndHomeFoldersAreHereJustFromThemselves(t *testing.T) {
	t.Setenv("HOME", "/Users/me")
	cases := []struct {
		cwd, pwd string
		want     bool
	}{
		{"/tmp/app/", "/tmp/app/web/", true},
		{"/tmp/app", "/tmp/app/", true},
		{"/", "/tmp/app", false},
		{"/", "/", true},
		{"/Users/me", "/Users/me/shop", false},
		{"/Users/me/", "/Users/me", true},
		{"/Users/me/shop", "/Users/me/shop/web", true},
	}
	for _, c := range cases {
		t.Setenv("PWD", c.pwd)
		if got := IsHere(Session{Cwd: c.cwd}); got != c.want {
			t.Errorf("a session from %s opened in %s: IsHere = %v, want %v", c.cwd, c.pwd, got, c.want)
		}
	}
}

func TestSessionsFromAParentFolderListFirst(t *testing.T) {
	t.Setenv("PWD", "/work/app/web")
	rows := []Row{
		{Path: "elsewhere", Session: Session{Cwd: "/work/other", Mtime: 300, Prompts: 5}},
		{Path: "parent", Session: Session{Cwd: "/work/app", Mtime: 100, Prompts: 5}},
	}
	if got := pathsOf(RecentFirst(rows)); !slices.Equal(got, []string{"parent", "elsewhere"}) {
		t.Fatalf("order = %v, want the session from the parent folder first", got)
	}
}

func pathsOf(rows []Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Path)
	}
	return out
}
