package upgrade

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/adammiribyan/superfind/internal/release"
	"github.com/adammiribyan/superfind/internal/store"
)

type Upgrader struct {
	Current  release.Version
	Install  Install
	Source   release.Source
	Progress func(string)
}

func (u Upgrader) Check(ctx context.Context) (string, error) {
	latest, err := u.Source.Latest(ctx)
	if err != nil {
		return "", err
	}
	switch {
	case latest == u.Current:
		return fmt.Sprintf("superfind %s is up to date", u.Current), nil
	case latest.Less(u.Current):
		return fmt.Sprintf("superfind %s is newer than the newest release, %s", u.Current, latest), nil
	case u.Install.Method == Script:
		return fmt.Sprintf("superfind %s is out; you have %s. superfind upgrade installs it", latest, u.Current), nil
	}
	return fmt.Sprintf("superfind %s is out; you have %s. %s, so install it with: %s", latest, u.Current, u.Install.origin(), u.Install.Command(nil)), nil
}

func (u Upgrader) To(ctx context.Context, pinned *release.Version) (string, error) {
	if u.Install.Method != Script {
		return u.Install.leftAlone(pinned), nil
	}
	unlock, err := lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	var target release.Version
	if pinned != nil {
		target = *pinned
	} else {
		u.say("checking the newest release")
		if target, err = u.Source.Latest(ctx); err != nil {
			return "", err
		}
	}
	if target == u.Current {
		return fmt.Sprintf("superfind %s is already installed", target), nil
	}
	dir, previous := filepath.Dir(u.Install.Path), previousPath()
	sweep(dir)
	sweep(filepath.Dir(previous))
	staged := stagedIn(dir)
	f, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return "", cantWrite(dir, err)
	}
	f.Close()
	defer os.Remove(staged)
	verified, err := u.Source.Download(ctx, target, staged, u.say)
	if err != nil {
		return "", err
	}
	if err := store.MakeDir(filepath.Dir(previous)); err != nil {
		return "", cantWrite(store.StateDir(), err)
	}
	kept := stagedIn(filepath.Dir(previous))
	if err := copyTo(u.Install.Path, kept); err != nil {
		return "", cantWrite(store.StateDir(), err)
	}
	defer os.Remove(kept)
	if err := os.Rename(kept, previous); err != nil {
		return "", cantWrite(store.StateDir(), err)
	}
	if err := os.Rename(verified.Path(), u.Install.Path); err != nil {
		return "", cantWrite(dir, err)
	}
	return fmt.Sprintf("installed superfind %s in place of %s at %s\nthe release is signed with release key %s and matches its checksums.txt; superfind upgrade --rollback goes back to %s%s",
		target, u.Current, u.Install.Path, verified.Key(), u.Current, u.settle(target)), nil
}

func (u Upgrader) Rollback(ctx context.Context) (string, error) {
	if u.Install.Method != Script {
		return u.Install.origin() + ", so superfind upgrade never replaced it, and there's nothing to roll back", nil
	}
	unlock, err := lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	previous := previousPath()
	if _, err := os.Stat(previous); errors.Is(err, fs.ErrNotExist) {
		return "nothing to roll back to: superfind upgrade keeps the copy it replaces, and it hasn't replaced one here", nil
	}
	was, err := release.VersionOf(ctx, previous)
	if err != nil {
		return "", fmt.Errorf("the copy superfind upgrade kept doesn't run, so nothing was changed: %w", err)
	}
	dir := filepath.Dir(u.Install.Path)
	sweep(dir)
	sweep(filepath.Dir(previous))
	incoming := stagedIn(dir)
	if err := copyTo(previous, incoming); err != nil {
		return "", cantWrite(dir, err)
	}
	defer os.Remove(incoming)
	outgoing := stagedIn(filepath.Dir(previous))
	if err := copyTo(u.Install.Path, outgoing); err != nil {
		return "", cantWrite(store.StateDir(), err)
	}
	defer os.Remove(outgoing)
	if err := os.Rename(incoming, u.Install.Path); err != nil {
		return "", cantWrite(dir, err)
	}
	if err := os.Rename(outgoing, previous); err != nil {
		return "", cantWrite(store.StateDir(), err)
	}
	return fmt.Sprintf("replaced superfind %s with %s at %s; superfind upgrade --rollback goes back to %s%s",
		u.Current, was, u.Install.Path, u.Current, u.settle(was)), nil
}

func (u Upgrader) settle(now release.Version) string {
	notes := ""
	if now.Less(u.Current) {
		if err := store.WriteAtomically(store.StatePath(filepath.Join("skip", u.Current.String())), nil); err != nil {
			notes += "\nnote: couldn't note that you left " + u.Current.String() + ": " + err.Error()
		}
	}
	if err := WriteReceipt(Receipt{Method: Script, Path: u.Install.Path}); err != nil {
		notes += "\nnote: couldn't rewrite " + store.StatePath(receiptName) + ": " + err.Error()
	}
	return notes
}

func (u Upgrader) say(line string) {
	if u.Progress != nil {
		u.Progress(line)
	}
}

func previousPath() string { return store.StatePath(filepath.Join("previous", "superfind")) }

func lock() (func(), error) {
	unlock, held, err := store.TryLock(store.StatePath("upgrade.lock"))
	if err != nil {
		return nil, cantWrite(store.StateDir(), err)
	}
	if !held {
		return nil, errors.New("another superfind upgrade is running")
	}
	return unlock, nil
}

func cantWrite(dir string, err error) error {
	var p *fs.PathError
	if errors.As(err, &p) {
		err = p.Err
	}
	return fmt.Errorf("can't write to %s (%v), so superfind was left as it is. To install the newest release in ~/.local/bin:\n  %s", dir, err, curlCommand)
}

const stagedPrefix = ".superfind.upgrade."

func stagedIn(dir string) string { return filepath.Join(dir, stagedPrefix+strconv.Itoa(os.Getpid())) }

func sweep(dir string) {
	stale, _ := filepath.Glob(filepath.Join(dir, stagedPrefix+"*"))
	for _, p := range stale {
		pid, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(p), stagedPrefix))
		if err == nil && pid != os.Getpid() && !running(pid) {
			os.Remove(p)
		}
	}
}

func running(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func copyTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(dst)
		return err
	}
	return os.Chmod(dst, 0o755)
}
