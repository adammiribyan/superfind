package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/adammiribyan/superfind/internal/agent"
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
	return append(places, filepath.Join(agent.Home(), ".config", "superfind", "env"))
}

func parse(text, name string) (string, bool) {
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "export"); ok && rest != strings.TrimLeft(rest, " \t") {
			line = strings.TrimLeft(rest, " \t")
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != name {
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
