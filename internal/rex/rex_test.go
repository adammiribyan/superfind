package rex

import (
	"os"
	"reflect"
	"testing"
)

func TestParseSnapshotJoinsEachPaneToItsForegroundPid(t *testing.T) {
	raw, err := os.ReadFile("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	main := "session:01a16c92-b180-72f5-ae24-c4eaf1eec3e7"
	want := map[int]Held{
		1521: {Pane{main, "window:01a16c92-b18b-79f2-9d0b-b259961aa85e", "block:01a16c92-b180-735f-b2b5-35dad3c2e2d7"},
			&Activity{State: "working"}},
		42273: {Pane{main, "window:01a16c92-b18b-79f2-9d0b-b259961aa85e", "block:01a16d8f-0cea-73c4-8228-9567ffe3d848"},
			&Activity{State: "idle"}},
		24154: {Pane{main, "window:01a16e2c-9d42-71e4-93dd-d72dfdc277ba", "block:01a16e2c-9d25-7664-8a1f-fc04fbfa6779"},
			&Activity{State: "working", Doing: "Saving a real Rex snapshot sample and creating the build worktree"}},
		49503: {Pane{main, "window:01a16e3c-c840-78d5-be65-ec8bd2880a45", "block:01a16e3c-c835-7ebd-8cb1-ffc66f33e161"}, nil},
		43882: {Pane{"session:01a16cdc-9819-7367-8f00-f470612d1171", "window:01a16cdf-81d6-791c-a103-4ea6a5a64560",
			"block:01a16cde-39bc-78f1-8d1a-af281ffb7878"}, &Activity{State: "done"}},
	}
	if got := parseSnapshot(raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSnapshot(sample) = %+v, want %+v", got, want)
	}
}

func TestParseSnapshotReadsTheAgentsOwnRecord(t *testing.T) {
	pane := `"session": "session:s", "window": "window:w", "block": "block:b"`
	cases := []struct {
		name, records string
		want          *Activity
	}{
		{"blocked on a permission", `[{"state": "blocked", "kind": "permission", "msg": "Approve deploy?", "owner": {"pid": 7}}]`,
			&Activity{State: "blocked", Need: "permission", Doing: "Approve deploy?"}},
		{"a subagent's record comes first", `[{"state": "working", "id": "agent-1", "msg": "Searching", "owner": {"pid": 7}},
			{"state": "idle", "owner": {"pid": 7}}]`, &Activity{State: "idle"}},
		{"a kind outside blocked is dropped", `[{"state": "working", "kind": "question", "owner": {"pid": 7}}]`,
			&Activity{State: "working"}},
		{"only another program's record", `[{"state": "working", "owner": {"pid": 8}}]`, nil},
		{"no records, written as an empty Lua table", `{}`, nil},
	}
	for _, c := range cases {
		got := parseSnapshot([]byte(`{"panes": [{` + pane + `, "pid": 7, "records": ` + c.records + `}]}`))
		if want := (map[int]Held{7: {Pane{"session:s", "window:w", "block:b"}, c.want}}); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got[7].Activity, c.want)
		}
	}
}

func TestParseSnapshotSkipsPanesItCannotAddress(t *testing.T) {
	for _, raw := range []string{
		`{"panes": [{"session": "session:s", "window": "window:w", "block": "block:b"}]}`,
		`{"panes": [{"session": "--server=evil", "window": "window:w", "block": "block:b", "pid": 7}]}`,
		`{"panes": [{"session": "session:s", "window": "window:w", "block": "-x", "pid": 7}]}`,
		`not json`,
	} {
		if got := parseSnapshot([]byte(raw)); len(got) != 0 {
			t.Errorf("parseSnapshot(%s) = %+v, want nothing", raw, got)
		}
	}
}

func TestNewWindowTypesTheCommandIntoAShellInTheFolder(t *testing.T) {
	got := string(newWindow("session:s", `Fix "quotes"`, "/tmp/a b", "cd '/tmp/a b' && claude --resume x"))
	want := `{"focus":true,"layout":{"block":{"flavor":"com.superlogical.terminal.shell","options":{"cwd":"/tmp/a b",` +
		`"initial_input":"cd '/tmp/a b' \u0026\u0026 claude --resume x\n"}}},"session_id":"session:s","window_label":"Fix \"quotes\""}`
	if got != want {
		t.Fatalf("newWindow =\n%s\nwant\n%s", got, want)
	}
}

func TestOutsideRexNothingRuns(t *testing.T) {
	t.Setenv("REX_SERVER", "")
	t.Setenv("REX_SESSION", "")
	t.Setenv("PATH", t.TempDir())
	if got := Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot() outside Rex = %+v, want nothing", got)
	}
	if Focus(Pane{"session:s", "window:w", "block:b"}) {
		t.Fatal("Focus outside Rex reported success")
	}
}
