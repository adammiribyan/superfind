package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/store"
)

func Lookup(name string) (value string, set bool) {
	if value, set := os.LookupEnv(name); set {
		return value, true
	}
	for _, path := range files() {
		if raw, err := os.ReadFile(path); err == nil {
			if value, set := parse(string(raw), name); set {
				return value, true
			}
		}
	}
	return "", false
}

func files() []string {
	var places []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			places = append(places, filepath.Join(filepath.Dir(real), ".env"), filepath.Join(filepath.Dir(filepath.Dir(real)), ".env"))
		}
	}
	return append(places, File())
}

func File() string { return filepath.Join(agent.Home(), ".config", "superfind", "env") }

func Set(name, value string) error {
	raw, err := os.ReadFile(File())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return store.WriteAtomically(File(), []byte(rewrite(string(raw), name, value)))
}

func rewrite(text, name, value string) string {
	var out strings.Builder
	kept := false
	for line := range strings.Lines(text) {
		if key, _, ok := split(line); ok && key == name {
			if !kept && value != "" {
				out.WriteString(name + "=" + value + "\n")
			}
			kept = true
			continue
		}
		out.WriteString(line)
	}
	if !kept && value != "" {
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteString("\n")
		}
		out.WriteString(name + "=" + value + "\n")
	}
	return out.String()
}

func split(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if rest, ok := strings.CutPrefix(line, "export"); ok && rest != strings.TrimLeft(rest, " \t") {
		line = strings.TrimLeft(rest, " \t")
	}
	key, value, ok = strings.Cut(line, "=")
	return strings.TrimSpace(key), value, ok
}

func parse(text, name string) (string, bool) {
	for line := range strings.Lines(text) {
		key, value, ok := split(line)
		if !ok || key != name {
			continue
		}
		value = strings.TrimLeft(value, " \t")
		if value != "" && (value[0] == '"' || value[0] == '\'') {
			quoted, _, _ := strings.Cut(value[1:], value[:1])
			return quoted, true
		}
		value, _, _ = strings.Cut(value, " ")
		value, _, _ = strings.Cut(value, "\t")
		return value, true
	}
	return "", false
}
