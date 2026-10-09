package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/adammiribyan/hop/internal/session"

	"github.com/adammiribyan/hop/internal/agent"
)

const (
	ansiAccent = "\033[34m"
	ansiDim    = "\033[2m"
	ansiBold   = "\033[1m"
	ansiReset  = "\033[0m"
)

func oneLine(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if displayWidth(text) <= width {
		return text
	}
	head, _ := cutToWidth(text, max(width-1, 0))
	return head + "…"
}

func tilde(path string) string {
	h := agent.Home()
	if path == h || strings.HasPrefix(path, h+"/") {
		return "~" + path[len(h):]
	}
	return path
}

func ago(mtime float64) string {
	seconds := session.Now() - mtime
	switch {
	case seconds < 60:
		return "just now"
	case seconds < 3600:
		return fmt.Sprintf("%dm ago", int(seconds/60))
	case seconds < 86400:
		return fmt.Sprintf("%dh ago", int(seconds/3600))
	case seconds < 7*86400:
		return fmt.Sprintf("%dd ago", int(seconds/86400))
	}
	t := time.Unix(int64(mtime), 0)
	if t.Year() == time.Now().Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}

func when(s session.Session) string {
	if s.Active() {
		return "active now"
	}
	return ago(s.Mtime)
}

type rowText struct {
	title, waiting string
	context        []string
}

func rowTextOf(r session.Row, shown tags) rowText {
	t := rowText{title: strings.Join(strings.Fields(r.Session.Title), " ")}
	if r.Status == session.WaitingLabel {
		t.waiting = r.Status
	} else if r.Status != "" {
		t.context = append(t.context, r.Status)
	}
	t.context = append(t.context, when(r.Session), r.Session.Project())
	if r.FolderGone {
		t.context = append(t.context, "folder removed")
	}
	t.context = append(t.context, shown.of(r)...)
	return t
}

func metaParts(r session.Row, shown tags) []string {
	s := r.Session
	parts := []string{s.Project()}
	if r.Status != "" {
		parts = append(parts, r.Status)
	}
	parts = append(parts, when(s))
	if s.Branch != "" && s.Branch != "HEAD" {
		parts = append(parts, s.Branch)
	}
	noun := "prompts"
	if s.Prompts == 1 {
		noun = "prompt"
	}
	parts = append(parts, fmt.Sprintf("%d %s", s.Prompts, noun))
	return append(parts, shown.of(r)...)
}
