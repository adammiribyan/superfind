package ask

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func stubClaude(t *testing.T, script string) string {
	dir := t.TempDir()
	pids := filepath.Join(dir, "pids")
	body := "#!/bin/sh\necho $$ >> " + pids + "\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return pids
}

func stillRunning(t *testing.T, pids string) []int {
	raw, _ := os.ReadFile(pids)
	var alive []int
	for _, field := range strings.Fields(string(raw)) {
		pid, _ := strconv.Atoi(field)
		if syscall.Kill(pid, 0) == nil {
			alive = append(alive, pid)
			t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
		}
	}
	return alive
}

func TestAnAskThatRunsOutOfTimeStopsClaudeAndEverythingItStarted(t *testing.T) {
	pids := stubClaude(t, "sleep 30 &\necho $! >> $(dirname $0)/pids\nwait")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	runClaude(ctx, nil, "")
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("runClaude returned after %s", took)
	}
	time.Sleep(200 * time.Millisecond)
	if alive := stillRunning(t, pids); len(alive) > 0 {
		t.Fatalf("processes %v outlived the ask", alive)
	}
}

func TestAnAskReturnsEvenWhenSomethingClaudeStartedKeepsItsOutputOpen(t *testing.T) {
	pids := stubClaude(t, "python3 -c 'import os, time; os.setsid(); time.sleep(30)' &\necho $! >> $(dirname $0)/pids\nwait")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	runClaude(ctx, nil, "")
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("runClaude returned after %s", took)
	}
	for _, pid := range stillRunning(t, pids) {
		syscall.Kill(pid, syscall.SIGKILL)
	}
}
