package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"strings"
	"sync"
	"syscall"
)

type statusKind struct {
	name        string
	label       string
	description string
}

var statusKinds = []statusKind{
	{"waiting", "waiting on you", "Claude cannot finish the developer's current request without them: it asked " +
		"a question it needs answered, or needs their approval, credentials, or a choice before continuing."},
	{"unfinished", "unfinished", "Work on the developer's last request stopped before it was done: Claude was " +
		"mid-step, was interrupted, hit an error or limit, or never answered."},
	{"finished", "", "The developer's last request was completed. An optional offer of more work, such as " +
		"\"want me to also…?\", does not mean Claude is waiting."},
	{"unclear", "", "The ending does not show which of the other states applies."},
}

func statusLabelFor(name string) string {
	for _, k := range statusKinds {
		if k.name == name {
			return k.label
		}
	}
	return ""
}

const waitingLabel = "waiting on you"

var lastEvents = map[string]string{
	"assistant_text": "Claude's last action was writing a reply to the developer.",
	"tool_call":      "Claude's last action was starting a tool (a command, edit, or search) whose result never arrived.",
	"user_prompt":    "The developer's last action was sending a request that Claude never answered.",
	"interrupted":    "The developer interrupted Claude while it was working.",
}

type endingTurn struct {
	Who  string `json:"who"`
	Text string `json:"text"`
}

type sessionEnding struct {
	LastEvent   string       `json:"last_event"`
	RecentTurns []endingTurn `json:"recent_turns"`
}

func ending(path string) sessionEnding {
	last := "assistant_text"
	eachLine(path, func(raw []byte) {
		switch {
		case bytes.Contains(raw, markAssistant):
			if bytes.Contains(raw, markToolUse) {
				last = "tool_call"
			} else {
				last = "assistant_text"
			}
		case bytes.Contains(raw, markUser):
			switch {
			case bytes.Contains(raw, markInterrupt):
				last = "interrupted"
			case bytes.Contains(raw, markToolResult):
				last = "tool_call"
			case promptText(strings.TrimSpace(messageText(decode(raw)))) != "":
				last = "user_prompt"
			}
		}
	})
	turns := conversation(path)
	var recent []endingTurn
	for _, t := range turns[max(len(turns)-6, 0):] {
		who := "claude"
		if t.role == "user" {
			who = "developer"
		}
		r := []rune(t.text)
		recent = append(recent, endingTurn{who, string(r[max(len(r)-700, 0):])})
	}
	return sessionEnding{lastEvents[last], recent}
}

func judgeStatus(key, path string) (string, float64, error) {
	criteria := map[string]string{}
	for _, k := range statusKinds {
		criteria[k.name] = k.description
	}
	answers, err := jev(key, ending(path), map[string]any{"status": map[string]any{
		"type": "choice",
		"instructions": "This is the end of a coding session between a developer and Claude. " +
			"In what state did the session end?",
		"criteria": criteria,
	}})
	if err != nil {
		return "", 0, err
	}
	a := answers["status"]
	return a.Choice, a.Probabilities[a.Choice], nil
}

type statusEntry struct {
	Mtime  float64 `json:"mtime"`
	Size   int64   `json:"size"`
	Status string  `json:"status"`
	P      float64 `json:"p"`
}

func statuses() map[string]statusEntry {
	known := map[string]statusEntry{}
	if raw, err := os.ReadFile(cachePath("status.json")); err == nil {
		json.Unmarshal(raw, &known)
	}
	return known
}

func statusLabel(path string, known map[string]statusEntry) string {
	e, ok := known[path]
	if !ok || e.P < statusShownAt {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	age := now() - stamp(info)
	if !sameFile(e.Mtime, e.Size, info) || age < activeSeconds || age >= labelDays*86400 {
		return ""
	}
	return statusLabelFor(e.Status)
}

func labelSessions(key string, rows []Row) bool {
	lock, err := os.OpenFile(cachePath("status.lock"), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return false
	}
	known := statuses()
	type job struct {
		path string
		info os.FileInfo
	}
	var todo []job
	for _, r := range rows {
		info, err := os.Stat(r.Path)
		if err != nil {
			continue
		}
		e, ok := known[r.Path]
		recent := now()-stamp(info) < labelDays*86400
		if recent && !r.Session.Throwaway() && !r.Session.Active() && !(ok && sameFile(e.Mtime, e.Size, info)) {
			todo = append(todo, job{r.Path, info})
		}
	}
	if len(todo) == 0 {
		return false
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	work := make(chan job)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				status, p, err := judgeStatus(key, j.path)
				if err != nil {
					continue
				}
				mu.Lock()
				known[j.path] = statusEntry{stamp(j.info), j.info.Size(), status, math.Round(p*1000) / 1000}
				mu.Unlock()
			}
		}()
	}
	for _, j := range todo {
		work <- j
	}
	close(work)
	wg.Wait()
	data, _ := json.Marshal(known)
	writeAtomically(cachePath("status.json"), data)
	return true
}
