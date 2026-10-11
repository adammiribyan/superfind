package blame

import (
	"os/exec"
	"strings"
	"testing"
)

func TestAShortHashThatFitsTwoCommitsMatchesNeither(t *testing.T) {
	top := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", top).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	first, second := "abcdef1"+strings.Repeat("1", 33), "abcdef1"+strings.Repeat("2", 33)
	rp := &repo{top: top, log: []entry{{commit: commit{SHA: first}}, {commit: commit{SHA: second}}}, onDefault: map[string]bool{}, onRemote: map[string]bool{}}
	if e, ok := rp.bySHA("abcdef1"); ok {
		t.Errorf("abcdef1 matched %s", e.SHA)
	}
	if e, ok := rp.bySHA("abcdef12"); !ok || e.SHA != second {
		t.Errorf("abcdef12 matched %q, %v; want %s", e.SHA, ok, second)
	}
}
