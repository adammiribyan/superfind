package main

import (
	"testing"

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
