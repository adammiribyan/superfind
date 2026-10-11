package main

import (
	"testing"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/open"
	"github.com/adammiribyan/superfind/internal/render"
	"github.com/adammiribyan/superfind/internal/session"
)

func TestTheHandoffTitleSaysWhenTheSessionIsOpenElsewhere(t *testing.T) {
	cases := []struct {
		row  session.Row
		want string
	}{
		{session.Row{Session: session.Session{Title: "fix ledger", Mtime: session.Now() - 86400}}, "fix ledger"},
		{session.Row{Session: session.Session{Title: "fix ledger", Mtime: session.Now()}},
			"fix ledger" + render.Reset + render.Dim + " · may still be open in another window"},
		{session.Row{Session: session.Session{Title: "fix ledger", Mtime: session.Now() - 86400}, Open: &open.State{PID: 4242}},
			"fix ledger" + render.Reset + render.Dim + " · already open in another window"},
		{session.Row{Session: session.Session{Title: "fix ledger", Mtime: session.Now()}, Open: &open.State{PID: 4242}},
			"fix ledger" + render.Reset + render.Dim + " · already open in another window"},
	}
	for _, c := range cases {
		if got := handoffTitle(c.row); got != c.want {
			t.Errorf("handoffTitle(open=%v) = %q, want %q", c.row.Open != nil, got, c.want)
		}
	}
}

func TestPickingASessionOpenInATmuxPaneSwitchesToThePane(t *testing.T) {
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1,0")
	row := session.Row{Agent: agent.Named("claude"), Home: "/home/me/.claude",
		Session: session.Session{ID: "0f6cab12-3a4b-4c5d-8e9f-001122334455", Title: "fix ledger", Cwd: "/tmp", Mtime: session.Now()},
		Open:    &open.State{PID: 4242, Status: "idle", Tmux: &open.Pane{ID: "%3", Session: "work\x1b[2J", Window: 1}}}
	want := "fix ledger" + render.Reset + render.Dim + " · in tmux session work, window 1\ntmux switch-client -t '%3'\n"
	if got := handoff(row); got != want {
		t.Errorf("handoff = %q, want %q", got, want)
	}
	row.Open = nil
	row.Session.Mtime = session.Now() - 86400
	want = "fix ledger\ncd /tmp && CLAUDE_CONFIG_DIR=/home/me/.claude claude --resume 0f6cab12-3a4b-4c5d-8e9f-001122334455\n"
	if got := handoff(row); got != want {
		t.Errorf("handoff of a closed session = %q, want %q", got, want)
	}
}

func TestPickingASessionOpenInATmuxPaneOutsideTmuxResumesIt(t *testing.T) {
	t.Setenv("TMUX", "")
	row := session.Row{Agent: agent.Named("claude"), Home: "/home/me/.claude",
		Session: session.Session{ID: "0f6cab12-3a4b-4c5d-8e9f-001122334455", Title: "fix ledger", Cwd: "/tmp", Mtime: session.Now() - 86400},
		Open:    &open.State{PID: 4242, Status: "idle", Tmux: &open.Pane{ID: "%3", Session: "work", Window: 1}}}
	want := "fix ledger" + render.Reset + render.Dim + " · already open in another window\ncd /tmp && CLAUDE_CONFIG_DIR=/home/me/.claude claude --resume 0f6cab12-3a4b-4c5d-8e9f-001122334455\n"
	if got := handoff(row); got != want {
		t.Errorf("handoff outside tmux = %q, want %q", got, want)
	}
}

func TestLastHandsOffSwitchesTabsAsksOrSaysWhereBySessionAndTerminal(t *testing.T) {
	pane := &open.Pane{ID: "%3", Session: "work", Window: 1}
	cases := []struct {
		name     string
		state    *open.State
		inTmux   bool
		ghostty  bool
		terminal bool
		want     lastAction
	}{
		{"closed", nil, false, true, false, handItOff},
		{"in a pane of this tmux", &open.State{PID: 4242, Tmux: pane}, true, true, false, handItOff},
		{"in a Ghostty tab", &open.State{PID: 4242}, false, true, false, switchedTab},
		{"in a pane, superfind outside tmux, in Ghostty", &open.State{PID: 4242, Tmux: pane}, false, true, true, switchedTab},
		{"elsewhere, at a terminal", &open.State{PID: 4242}, true, false, true, askFirst},
		{"elsewhere, in a script", &open.State{PID: 4242}, false, false, false, sayWhereItIs},
		{"in a pane, superfind outside tmux, in a script", &open.State{PID: 4242, Tmux: pane}, false, false, false, sayWhereItIs},
	}
	for _, c := range cases {
		got := lastStep(c.state, c.inTmux, func(open.State) bool { return c.ghostty }, c.terminal)
		if got != c.want {
			t.Errorf("%s: last does %d, want %d", c.name, got, c.want)
		}
	}
}
