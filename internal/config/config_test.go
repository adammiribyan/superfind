package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnEnvFileLineReadsTheWayAShellWould(t *testing.T) {
	for _, c := range []struct {
		text, value string
		set         bool
	}{
		{"SUPERFIND_TELEMETRY=off\n", "off", true},
		{"export SUPERFIND_TELEMETRY=off", "off", true},
		{"  export\tSUPERFIND_TELEMETRY = off  ", "off", true},
		{`SUPERFIND_TELEMETRY="off"`, "off", true},
		{`SUPERFIND_TELEMETRY='o f f'`, "o f f", true},
		{`SUPERFIND_TELEMETRY="off`, "off", true},
		{"SUPERFIND_TELEMETRY=off # quiet please", "off", true},
		{`SUPERFIND_TELEMETRY="off" # quiet please`, "off", true},
		{"SUPERFIND_TELEMETRY=off\r\nOTHER=1\r\n", "off", true},
		{"SUPERFIND_TELEMETRY=\nOTHER=1\n", "", true},
		{"SUPERFIND_TELEMETRY=off\nSUPERFIND_TELEMETRY=on\n", "off", true},
		{"# SUPERFIND_TELEMETRY=off\n\n\nSUPERFIND_TELEMETRY=on\n", "on", true},
		{"exportSUPERFIND_TELEMETRY=off", "", false},
		{"SUPERFIND_TELEMETRY_X=off\nX_SUPERFIND_TELEMETRY=off\n", "", false},
		{"SUPERFIND_TELEMETRY off\n", "", false},
		{"", "", false},
	} {
		value, set := parse(c.text, "SUPERFIND_TELEMETRY")
		if value != c.value || set != c.set {
			t.Errorf("parse(%q) = %q, %v; want %q, %v", c.text, value, set, c.value, c.set)
		}
	}
}

func TestTheVariableWinsOverTheFileEvenWhenEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".config", "superfind"), 0o700)
	os.WriteFile(filepath.Join(home, ".config", "superfind", "env"), []byte("export SUPERFIND_TEST_SETTING='from file'\n"), 0o600)

	os.Unsetenv("SUPERFIND_TEST_SETTING")
	if value, set := Lookup("SUPERFIND_TEST_SETTING"); value != "from file" || !set {
		t.Errorf("unset variable: Lookup = %q, %v; want the file's value", value, set)
	}
	t.Setenv("SUPERFIND_TEST_SETTING", "")
	if value, set := Lookup("SUPERFIND_TEST_SETTING"); value != "" || !set {
		t.Errorf("empty variable: Lookup = %q, %v; want \"\", true", value, set)
	}
	t.Setenv("SUPERFIND_TEST_SETTING", "from env")
	if value, set := Lookup("SUPERFIND_TEST_SETTING"); value != "from env" || !set {
		t.Errorf("set variable: Lookup = %q, %v; want the variable", value, set)
	}
	os.Unsetenv("SUPERFIND_TEST_SETTING")
	if value, set := Lookup("SUPERFIND_TEST_MISSING"); value != "" || set {
		t.Errorf("missing setting: Lookup = %q, %v; want \"\", false", value, set)
	}
}
