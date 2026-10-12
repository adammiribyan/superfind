package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	idA = "aaaaaaaa-1111-4111-8111-111111111111"
	idB = "bbbbbbbb-2222-4222-8222-222222222222"
	idC = "cccccccc-3333-4333-8333-333333333333"
)

var historyLines = []string{
	`{"display":"fix the billing retry loop","pastedContents":{},"timestamp":1757685780000,"project":"/work/billing","sessionId":"` + idA + `"}`,
	`{"display":"/model","pastedContents":{},"timestamp":1757685790000,"project":"/work/billing","sessionId":"` + idB + `"}`,
	`{"display":"an old prompt with no session","pastedContents":{},"timestamp":1757685795000,"project":"/work/billing"}`,
	`{"display":"/clear","pastedContents":{},"timestamp":1757685800000,"project":"/work/billing","sessionId":"` + idB + `"}`,
	`{"display":"/tmp/x is broken","pastedContents":{},"timestamp":1757685810000,"project":"/work/infra","sessionId":"` + idC + `"}`,
	`{"display":"now add a test [Pasted text #2 +3 lines] and [Pasted text #1]","pastedContents":{"2":{"id":2,"type":"text","content":"second paste"},"1":{"id":1,"type":"text","contentHash":"ab12"}},"timestamp":1757686800000,"project":"/work/billing","sessionId":"` + idA + `"}`,
	`{"display":"/review the auth change","pastedContents":{},"timestamp":1757686900000,"project":"/work/billing","sessionId":"` + idA + `"}`,
	`{ "display": "spaced out", "timestamp": 1757687000000, "project": "/work/infra", "sessionId": "` + idC + `" }`,
}

func writeHistory(t *testing.T, home string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "history.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func size(lines ...int) int64 {
	n := 0
	for _, i := range lines {
		n += len(historyLines[i]) + 1
	}
	return int64(n)
}

func TestPromptsOnlyGroupsHistoryBySessionAndSkipsWhatIsNotAPrompt(t *testing.T) {
	home := t.TempDir()
	writeHistory(t, home, historyLines...)
	got, err := Named("claude").PromptsOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	ids := map[string]FileID{}
	mtimes := map[string]float64{}
	for _, s := range got {
		paths = append(paths, s.Path)
		ids[filepath.Base(s.Path)] = s.ID
		mtimes[filepath.Base(s.Path)] = s.Mtime
	}
	if want := []string{home + "/history.jsonl/" + idA, home + "/history.jsonl/" + idC}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %q, want %q", paths, want)
	}
	if want := (FileID{1757686800000 * 1e6, size(0, 5, 6)}); ids[idA] != want {
		t.Fatalf("A = %+v, want %+v", ids[idA], want)
	}
	if want := (FileID{1757687000000 * 1e6, size(4, 7)}); ids[idC] != want {
		t.Fatalf("C = %+v, want %+v", ids[idC], want)
	}
	if mtimes[idA] != 1757686800 {
		t.Fatalf("A mtime = %v", mtimes[idA])
	}
}

func TestAPromptTypedInOneSessionChangesOnlyThatSessionsID(t *testing.T) {
	home := t.TempDir()
	writeHistory(t, home, historyLines...)
	before, _ := Named("claude").PromptsOnly(home)
	writeHistory(t, home, append(historyLines,
		`{"display":"one more","pastedContents":{},"timestamp":1757690000000,"project":"/work/infra","sessionId":"`+idC+`"}`)...)
	after, _ := Named("claude").PromptsOnly(home)
	if before[0].ID != after[0].ID {
		t.Fatalf("A changed: %+v -> %+v", before[0].ID, after[0].ID)
	}
	if before[1].ID == after[1].ID {
		t.Fatalf("C did not change: %+v", after[1].ID)
	}
}

func TestAPromptsOnlySessionReadsAsTimedUserTurns(t *testing.T) {
	home := t.TempDir()
	writeHistory(t, home, historyLines...)
	found, _ := Named("claude").PromptsOnly(home)
	indexed, trail, err := found[0].Read()
	if err != nil {
		t.Fatal(err)
	}
	reread, err := ReadTranscript(Named("claude"), found[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Turn{
		{Role: "user", Text: "fix the billing retry loop", At: time.UnixMilli(1757685780000)},
		{Role: "user", Text: "now add a test [Pasted text #2 +3 lines] and [Pasted text #1]\n\nsecond paste", At: time.UnixMilli(1757686800000)},
	}
	for _, got := range []Transcript{indexed, reread} {
		if !reflect.DeepEqual(got.Turns, want) || got.Cwd != "/work/billing" || got.Last != Deleted || len(got.Prompts) != 2 {
			t.Fatalf("got %+v", got)
		}
	}
	if !reflect.DeepEqual(trail, Trail{}) {
		t.Fatalf("trail = %+v", trail)
	}
	ending, err := ReadEnding(Named("claude"), found[0].Path, func([]Turn) bool { return true })
	if err != nil || !reflect.DeepEqual(ending, want) {
		t.Fatalf("ending = %+v, %v", ending, err)
	}
	trail, err = ReadTrail(Named("claude"), found[0].Path)
	if err != nil || !reflect.DeepEqual(trail, Trail{}) {
		t.Fatalf("ReadTrail = %+v, %v", trail, err)
	}
	session, _, err := ReadSession(Named("claude"), found[1].Path)
	if err != nil || len(session.Turns) != 2 || session.Turns[1].Text != "spaced out" || session.Cwd != "/work/infra" {
		t.Fatalf("ReadSession = %+v, %v", session, err)
	}
}

func TestAPurgedSessionNoLongerReads(t *testing.T) {
	home := t.TempDir()
	writeHistory(t, home, historyLines...)
	found, _ := Named("claude").PromptsOnly(home)
	writeHistory(t, home, historyLines[4], historyLines[7])
	_, err := ReadTranscript(Named("claude"), found[0].Path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
	if left, _ := Named("claude").PromptsOnly(home); len(left) != 1 || left[0].Path != found[1].Path {
		t.Fatalf("left = %+v", left)
	}
}

func TestNoHistoryMeansNoPromptsOnlySessions(t *testing.T) {
	got, err := Named("claude").PromptsOnly(t.TempDir())
	if got != nil || err != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got, err := Named("codex").PromptsOnly(t.TempDir()); got != nil || err != nil {
		t.Fatalf("codex got %+v, %v", got, err)
	}
}

func TestALocatorIsAClaudeSessionThatCantBeResumed(t *testing.T) {
	t.Setenv("HOME", "/Users/you")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claude := Named("claude")
	path := "/Users/you/.claude/history.jsonl/" + idA
	if !TranscriptGone(path) || TranscriptGone("/Users/you/.claude/projects/-work/"+idA+".jsonl") {
		t.Fatal("TranscriptGone")
	}
	if claude.IDOf(path) != idA {
		t.Fatalf("IDOf = %q", claude.IDOf(path))
	}
	places := []Place{{claude, "/Users/you/.claude"}, {Named("codex"), "/Users/you/.codex"}}
	if p, ok := Locate(path, places); !ok || p != places[0] {
		t.Fatalf("Locate = %+v, %v", p, ok)
	}
	if _, ok := Locate("/Users/you/.codex/history.jsonl/"+idA, places); ok {
		t.Fatal("codex has no prompt history")
	}
	command, err := ResumeCommand(claude, "/Users/you/.claude", path, "/work/billing")
	if command != "" || err == nil || err.Error() != "Claude Code deleted this session's transcript, so Claude can't resume it. "+
		"superfind show "+idA+" prints your prompts. To keep future transcripts longer, set cleanupPeriodDays in ~/.claude/settings.json." {
		t.Fatalf("got %q, %v", command, err)
	}
	if GoneNote("/srv/claude-work") != "Claude Code deleted this session's transcript, so only your prompts are left. "+
		"To keep future transcripts longer, set cleanupPeriodDays in /srv/claude-work/settings.json." {
		t.Fatal(GoneNote("/srv/claude-work"))
	}
	command, err = ResumeCommand(claude, "/Users/you/.claude", "/Users/you/.claude/projects/-work/"+idA+".jsonl", "/")
	if command != "cd / && claude --resume "+idA || err != nil {
		t.Fatalf("got %q, %v", command, err)
	}
}

func TestEverySlashCommandIsDroppedWhateverFollowsIt(t *testing.T) {
	home := t.TempDir()
	var lines []string
	for i, display := range []string{`/`, `/compact\nkeep the tests`, `/model\topus`, `/exit`, `/plugin:run-it now`, `/tmp/x is broken`, `/é`} {
		lines = append(lines, `{"display":"`+display+`","pastedContents":{},"timestamp":`+strconv.Itoa(1757685780000+i)+`,"project":"/w","sessionId":"`+idA+`"}`)
	}
	writeHistory(t, home, lines...)
	found, _ := Named("claude").PromptsOnly(home)
	got, _, _ := found[0].Read()
	if want := []string{"/", "/tmp/x is broken", "/é"}; !reflect.DeepEqual(got.Prompts, want) {
		t.Fatalf("prompts = %q, want %q", got.Prompts, want)
	}
	if found[0].ID.ModNanos != (1757685780000+6)*1e6 {
		t.Fatalf("ID = %+v", found[0].ID)
	}
}
