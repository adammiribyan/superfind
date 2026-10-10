package agent

import (
	"os/exec"
	"testing"
)

func TestAQuotedValueReadsBackUnchangedInEveryShell(t *testing.T) {
	values := []string{`plain`, `two words`, `it's`, `back\slash`, `end\`, `\'`, `\\'x'\\`, `$HOME`, "`date`", `a;echo PWNED`, `%self`, `=sh`, `a=b`, "line\nbreak", `"quoted"`}
	for _, shell := range []string{"sh", "bash", "zsh", "fish"} {
		if _, err := exec.LookPath(shell); err != nil {
			t.Logf("%s is not installed", shell)
			continue
		}
		for _, value := range values {
			out, err := exec.Command(shell, "-c", "printf '%s' "+ShellQuote(value)).Output()
			if err != nil || string(out) != value {
				t.Errorf("%s read %q back as %q (%v)", shell, value, out, err)
			}
		}
	}
}
