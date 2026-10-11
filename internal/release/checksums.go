package release

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func CheckAssets(dir string, checksums []byte) error {
	lines := strings.Split(strings.TrimRight(string(checksums), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return errors.New("checksums.txt lists no files")
	}
	var problems []error
	for _, line := range lines {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 || strings.Trim(sum, "0123456789abcdef") != "" || name != filepath.Base(name) || name == "." || name == ".." {
			return fmt.Errorf("checksums.txt has a line that is not `<sha256>  <file name>`: %q", line)
		}
		got, err := sha256File(filepath.Join(dir, name))
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", name, err))
		} else if got != sum {
			problems = append(problems, fmt.Errorf("%s: sha256 is %s, but checksums.txt says %s", name, got, sum))
		}
	}
	return errors.Join(problems...)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
