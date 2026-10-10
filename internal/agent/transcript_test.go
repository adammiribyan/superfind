package agent

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestALongAgentRunIsJoinedIntoOneTurnInLinearTime(t *testing.T) {
	lines := []string{typed}
	piece := strings.Repeat("x", 8<<10)
	for i := range 1000 {
		lines = append(lines, fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"text","text":"%04d%s"}]}}`, i, piece))
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	tr := readLines(t, Named("claude"), path, lines...)
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 256<<20 {
		t.Errorf("reading 8 MB allocated %d MB", allocated>>20)
	}
	if len(tr.Turns) != 2 || len(tr.Turns[1].Text) != 1000*(4+len(piece))+999*2 {
		t.Fatalf("got %d turns", len(tr.Turns))
	}
	if !strings.HasPrefix(tr.Turns[1].Text, "0000x") || !strings.Contains(tr.Turns[1].Text, "x\n\n0001x") || !strings.HasSuffix(tr.Turns[1].Text, "0999"+piece) {
		t.Fatal("the run's messages were not joined in order")
	}
}
