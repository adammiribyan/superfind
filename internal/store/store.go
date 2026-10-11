package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/adammiribyan/superfind/internal/agent"
)

func CacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "superfind")
	}
	return filepath.Join(agent.Home(), ".cache", "superfind")
}

func CachePath(name string) string { return filepath.Join(CacheDir(), name) }

func StateDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "superfind")
	}
	return filepath.Join(agent.Home(), ".local", "state", "superfind")
}

func StatePath(name string) string { return filepath.Join(StateDir(), name) }

func MakeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	os.Chmod(dir, 0o700)
	return nil
}

func WriteAtomically(path string, data []byte) error {
	tmp, err := writeTemp(path, data)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Rename(tmp, path)
}

func CreateOnce(path string, data []byte) ([]byte, error) {
	if held, err := os.ReadFile(path); err == nil {
		return held, nil
	}
	tmp, err := writeTemp(path, data)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp)
	if err := os.Link(tmp, path); err == nil {
		return data, nil
	} else if !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	return os.ReadFile(path)
}

func writeTemp(path string, data []byte) (string, error) {
	if err := MakeDir(filepath.Dir(path)); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func Lock(path string) (release func(), err error) {
	release, _, err = lock(path, syscall.LOCK_EX)
	return release, err
}

func TryLock(path string) (release func(), held bool, err error) {
	return lock(path, syscall.LOCK_EX|syscall.LOCK_NB)
}

func lock(path string, how int) (func(), bool, error) {
	if err := MakeDir(filepath.Dir(path)); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() { f.Close() }, true, nil
}

func Writable(dir string) bool {
	if MakeDir(dir) != nil {
		return false
	}
	probe, err := os.CreateTemp(dir, ".probe.*")
	if err != nil {
		return false
	}
	probe.Close()
	os.Remove(probe.Name())
	return true
}

func Unwritable(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS)
}
