package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/adammiribyan/superfind/internal/agent"
)

func TestAShortPreviewBodyKeepsYourMessageAboveTheEndOfTheReply(t *testing.T) {
	var steps []string
	for i := 1; i <= 30; i++ {
		steps = append(steps, fmt.Sprintf("Step %d is done.", i))
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	transcript := `{"type":"user","cwd":"/w","entrypoint":"cli","message":{"content":"walk the migration through every step"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"` + strings.Join(steps, `\n\n`) + `"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := PreviewBody(PreviewRequest{Agent: agent.Named("claude"), Path: path, Width: 60, Height: 8})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range body {
		got = append(got, ansi.Strip(line))
	}
	want := []string{
		"you",
		"  walk the migration through every step",
		"",
		"claude",
		"  … earlier in this reply",
		"  Step 29 is done.",
		"  ",
		"  Step 30 is done.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the body reads\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestTheShortestPreviewBodyStillShowsTheStartOfYourMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	ask := strings.Repeat("the refund webhook runs twice when the provider retries it ", 4)
	transcript := `{"type":"user","cwd":"/w","entrypoint":"cli","message":{"content":"` + ask + `"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Recorded the event id first.\n\nRetries are skipped now.\n\nAll 41 tests pass."}]}}` + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := PreviewBody(PreviewRequest{Agent: agent.Named("claude"), Path: path, Width: 40, Height: 6})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range body {
		got = append(got, ansi.Strip(line))
	}
	want := []string{
		"you",
		"  the refund webhook runs twice when…",
		"",
		"claude",
		"  … earlier in this reply",
		"  All 41 tests pass.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the body reads\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestAPreviewBodyShorterThanItsSpeakersStillStartsWithYou(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	transcript := `{"type":"user","cwd":"/w","entrypoint":"cli","message":{"content":"rotate the signing key"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Rotated."}]}}` + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	for height := 1; height <= 4; height++ {
		body, err := PreviewBody(PreviewRequest{Agent: agent.Named("claude"), Path: path, Width: 40, Height: height})
		if err != nil {
			t.Fatal(err)
		}
		if got := ansi.Strip(body[0]); got != "you" {
			t.Errorf("at height %d the body starts with %q, want \"you\"", height, got)
		}
	}
}
