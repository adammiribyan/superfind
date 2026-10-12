package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func TestTheFoldersFollowXDGThenHOME(t *testing.T) {
	t.Setenv("HOME", "/home/ada")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	if got := CacheDir(); got != "/home/ada/.cache/superfind" {
		t.Errorf("CacheDir = %q", got)
	}
	if got := StateDir(); got != "/home/ada/.local/state/superfind" {
		t.Errorf("StateDir = %q", got)
	}
	t.Setenv("XDG_CACHE_HOME", "/xdg/cache")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	if got := CachePath("index.json"); got != "/xdg/cache/superfind/index.json" {
		t.Errorf("CachePath = %q", got)
	}
	if got := StateDir(); got != "/xdg/state/superfind" {
		t.Errorf("StateDir = %q", got)
	}
}

func TestAWriteMakesAPrivateFolderAndLeavesNoTempFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "superfind")
	path := filepath.Join(dir, "receipt")
	if err := WriteAtomically(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomically(path, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "two" {
		t.Errorf("file holds %q, want two", got)
	}
	info, _ := os.Stat(dir)
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("folder mode %o, want 700", mode)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("folder holds %d files, want just the receipt", len(entries))
	}
}

func TestRacingFirstWritersAgreeOnOneContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "superfind", "install-id")
	const writers = 16
	got := make([]string, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			held, err := CreateOnce(path, []byte(fmt.Sprintf("id-%d", i)))
			if err != nil {
				t.Error(err)
			}
			got[i] = string(held)
		}()
	}
	wg.Wait()
	onDisk, _ := os.ReadFile(path)
	for i, g := range got {
		if g != string(onDisk) {
			t.Errorf("writer %d saw %q, the file holds %q", i, g, onDisk)
		}
	}
	again, _ := CreateOnce(path, []byte("later"))
	if string(again) != string(onDisk) {
		t.Errorf("a later write replaced %q with %q", onDisk, again)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("folder holds %d files, want just the ID", len(entries))
	}
}

func TestTryLockGivesUpAtOnceWhileTheLockIsHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "superfind", "status.lock")
	release, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, held, err := TryLock(path); held || err != nil {
		t.Fatalf("TryLock on a held lock = held %v, err %v; want false, nil", held, err)
	}
	release()
	unlock, held, err := TryLock(path)
	if !held || err != nil {
		t.Fatalf("TryLock on a free lock = held %v, err %v; want true, nil", held, err)
	}
	unlock()
}

func TestAReadOnlyHomeCountsAsUnwritable(t *testing.T) {
	home := t.TempDir()
	os.Chmod(home, 0o500)
	t.Cleanup(func() { os.Chmod(home, 0o700) })
	err := WriteAtomically(filepath.Join(home, ".cache", "superfind", "index.json"), []byte("{}"))
	if !Unwritable(err) {
		t.Errorf("Unwritable(%v) = false", err)
	}
	if Unwritable(os.ErrNotExist) {
		t.Error("a missing file counted as unwritable")
	}
}
