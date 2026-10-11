package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/adammiribyan/superfind/internal/store"
)

const (
	checkFile     = "check.json"
	checkLock     = "check.lock"
	checkInterval = 20 * time.Hour
)

type Check struct {
	Next     time.Time `json:"next"`
	Latest   string    `json:"latest,omitempty"`
	Failures int       `json:"failures"`
}

func LastCheck() (c Check, written time.Time) {
	path := store.StatePath(checkFile)
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return Check{}, time.Time{}
	}
	if info, err := os.Stat(path); err == nil {
		written = info.ModTime()
	}
	return c, written
}

func writeCheck(c Check) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return store.WriteAtomically(store.StatePath(checkFile), append(raw, '\n'))
}

func Due(c Check, now time.Time) bool {
	return !now.Before(c.Next) || c.Next.After(now.Add(retryMax))
}

func wait(failures int) time.Duration {
	if failures < 1 {
		return checkInterval
	}
	return min(time.Hour<<(min(failures, 8)-1), checkInterval)
}

func Checkin(ctx context.Context, b Build) {
	enabled, ok := Current().Enabled()
	if !ok {
		return
	}
	unlock, held, err := store.TryLock(store.StatePath(checkLock))
	if err != nil || !held {
		return
	}
	defer unlock()
	now := time.Now().UTC()
	c, _ := LastCheck()
	if !Due(c, now) {
		return
	}
	if writeCheck(Check{Next: now.Add(wait(c.Failures + 1)), Latest: c.Latest, Failures: c.Failures + 1}) != nil {
		return
	}
	id, err := EnsureID()
	if err != nil {
		return
	}
	p := NextPing(b, now)
	p.ID = id
	reply, err := Send(ctx, enabled, URL(), p)
	if err != nil {
		return
	}
	done := Check{Next: now.Add(checkInterval), Latest: c.Latest}
	if reply.RetryAfter > 0 {
		done.Next = now.Add(reply.RetryAfter)
	}
	if reply.Latest != "" {
		done.Latest = reply.Latest
	}
	writeCheck(done)
}
