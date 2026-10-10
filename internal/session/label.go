package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"sync"
	"syscall"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/typesafe"
)

type statusKind struct {
	name        string
	label       string
	description string
}

var statusKinds = []statusKind{
	{"waiting", "waiting on you", "{agent} cannot finish the developer's current request without them: it asked " +
		"a question it needs answered, or needs their approval, credentials, or a choice before continuing."},
	{"unfinished", "unfinished", "Work on the developer's last request stopped before it was done: {agent} was " +
		"mid-step, was interrupted, hit an error or limit, or never answered."},
	{"finished", "", "The developer's last request was completed. An optional offer of more work, such as " +
		"\"want me to also…?\", does not mean {agent} is waiting."},
	{"unclear", "", "The ending does not show which of the other states applies."},
}

func statusKindOf(name string) *statusKind {
	for i := range statusKinds {
		if statusKinds[i].name == name {
			return &statusKinds[i]
		}
	}
	return nil
}

func statusLabelFor(name string) string {
	if k := statusKindOf(name); k != nil {
		return k.label
	}
	return ""
}

const WaitingLabel = "waiting on you"

var lastEvents = map[agent.Event]string{
	agent.Replied:     "{agent}'s last action was writing a reply to the developer.",
	agent.CalledTool:  "{agent}'s last action was starting a tool (a command, edit, or search) whose result never arrived.",
	agent.Prompted:    "The developer's last action was sending a request that {agent} never answered.",
	agent.Interrupted: "The developer interrupted {agent} while it was working.",
}

type endingTurn struct {
	Who  string `json:"who"`
	Text string `json:"text"`
}

type sessionEnding struct {
	LastEvent   string       `json:"last_event"`
	RecentTurns []endingTurn `json:"recent_turns"`
}

func ending(a *agent.Agent, path string) (sessionEnding, error) {
	t, err := agent.ReadTranscript(a, path)
	if err != nil {
		return sessionEnding{}, err
	}
	var recent []endingTurn
	for _, said := range t.Turns[max(len(t.Turns)-6, 0):] {
		who := a.ID
		if said.Role == "user" {
			who = "developer"
		}
		r := []rune(said.Text)
		recent = append(recent, endingTurn{who, string(r[max(len(r)-700, 0):])})
	}
	return sessionEnding{a.Phrase(lastEvents[t.Last]), recent}, nil
}

func judgeStatus(key string, r Row) (string, float64, error) {
	criteria := map[string]string{}
	for _, k := range statusKinds {
		criteria[k.name] = r.Agent.Phrase(k.description)
	}
	end, err := ending(r.Agent, r.Path)
	if err != nil {
		return "", 0, err
	}
	answers, err := typesafe.Jev(context.Background(), key, end, map[string]any{"status": map[string]any{
		"type": "choice",
		"instructions": r.Agent.Phrase("This is the end of a coding session between a developer and {agent}. " +
			"In what state did the session end?"),
		"criteria": criteria,
	}})
	if err != nil {
		return "", 0, err
	}
	a, ok := answers["status"]
	p, known := a.Probabilities[a.Choice]
	if !ok || !known || statusKindOf(a.Choice) == nil {
		return "", 0, fmt.Errorf("TypeSafe returned no usable status")
	}
	return a.Choice, p, nil
}

type statusEntry struct {
	agent.FileID
	Status string  `json:"status"`
	P      float64 `json:"p"`
}

func statuses() map[string]statusEntry {
	var known map[string]statusEntry
	if raw, err := os.ReadFile(CachePath("status.json")); err == nil && json.Unmarshal(raw, &known) == nil && known != nil {
		return known
	}
	return map[string]statusEntry{}
}

func statusLabel(r Row, known map[string]statusEntry) string {
	e, ok := known[r.Path]
	if !ok || e.P < statusShownAt {
		return ""
	}
	id, mtime, err := r.Agent.Stat(r.Path)
	if err != nil {
		return ""
	}
	age := Now() - mtime
	if e.FileID != id || age < activeSeconds || age >= labelDays*86400 {
		return ""
	}
	return statusLabelFor(e.Status)
}

func Label(key string, rows []Row) error {
	lock, err := os.OpenFile(CachePath("status.lock"), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return nil
	}
	known := statuses()
	pruned := false
	for path := range known {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			delete(known, path)
			pruned = true
		}
	}
	type job struct {
		row Row
		id  agent.FileID
	}
	var todo []job
	for _, r := range rows {
		id, mtime, err := r.Agent.Stat(r.Path)
		if err != nil {
			continue
		}
		e, ok := known[r.Path]
		recent := Now()-mtime < labelDays*86400
		if recent && !r.Session.Throwaway() && !r.Session.Active() && !(ok && e.FileID == id) {
			todo = append(todo, job{r, id})
		}
	}
	if len(todo) == 0 && !pruned {
		return nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var failures []error
	work := make(chan job)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				status, p, err := judgeStatus(key, j.row)
				mu.Lock()
				if err != nil {
					failures = append(failures, err)
				} else {
					known[j.row.Path] = statusEntry{j.id, status, math.Round(p*1000) / 1000}
				}
				mu.Unlock()
			}
		}()
	}
	for _, j := range todo {
		work <- j
	}
	close(work)
	wg.Wait()
	data, err := json.Marshal(known)
	if err != nil {
		return err
	}
	if err := writeAtomically(CachePath("status.json"), data); err != nil {
		return fmt.Errorf("saving labels: %w", err)
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d of %d sessions couldn't be labeled: %w", len(failures), len(todo), failures[0])
	}
	return nil
}
