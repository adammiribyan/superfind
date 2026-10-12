package telemetry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/adammiribyan/superfind/internal/store"
)

const (
	eventsDir  = "events"
	queueLimit = 200
)

var recorded []Event

func Record(e Event) {
	if !Current().On() {
		return
	}
	recorded = append(recorded, e)
}

func Flush() {
	for _, e := range recorded {
		props, err := json.Marshal(e)
		if err != nil {
			continue
		}
		body, err := json.Marshal(stored{ID: newUUID(), Name: e.Name(), At: time.Now().UTC().Truncate(time.Hour), Props: props})
		if err != nil {
			continue
		}
		for nanos := time.Now().UnixNano(); ; nanos++ {
			path := store.StatePath(filepath.Join(eventsDir, strconv.FormatInt(nanos, 10)+"-"+strconv.Itoa(os.Getpid())+".json"))
			if _, err := os.Stat(path); err == nil {
				continue
			}
			store.WriteAtomically(path, append(body, '\n'))
			break
		}
	}
	recorded = nil
}

type stored struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	At    time.Time       `json:"at"`
	Props json.RawMessage `json:"props"`
}

func readQueue(now time.Time) ([]Queued, []string) {
	paths, _ := filepath.Glob(store.StatePath(filepath.Join(eventsDir, "*.json")))
	sort.Strings(paths)
	for len(paths) > queueLimit {
		os.Remove(paths[0])
		paths = paths[1:]
	}
	events, files := []Queued{}, []string{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		var s stored
		if err != nil || json.Unmarshal(raw, &s) != nil || !uuidV4.MatchString(s.ID) || s.Name == "" || !bytes.HasPrefix(bytes.TrimSpace(s.Props), []byte("{")) {
			os.Remove(path)
			continue
		}
		events = append(events, Queued{ID: s.ID, Name: s.Name, HoursAgo: max(int(now.Sub(s.At).Hours()), 0), Props: s.Props})
		files = append(files, path)
	}
	return events, files
}
