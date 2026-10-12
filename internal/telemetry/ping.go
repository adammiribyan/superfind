package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/adammiribyan/superfind/internal/release"
)

type Ping struct {
	Schema  int      `json:"schema"`
	ID      string   `json:"id"`
	Version string   `json:"version"`
	OS      string   `json:"os"`
	Arch    string   `json:"arch"`
	Method  Method   `json:"method"`
	Events  []Queued `json:"events"`

	files []string
}

type Queued struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	HoursAgo int             `json:"hours_ago"`
	Props    json.RawMessage `json:"props"`
}

type Build struct {
	Version string
	Method  Method
}

func NextPing(b Build, now time.Time) Ping {
	id, _ := LoadID()
	events, files := readQueue(now)
	return Ping{Schema: 1, ID: id, Version: b.Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Method: b.Method, Events: events, files: files}
}

func (p Ping) JSON() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil
	}
	return buf.Bytes()
}

func (p Ping) forget() {
	for _, path := range p.files {
		os.Remove(path)
	}
}

const DefaultURL = "https://superfind.dev/v1/checkin"

func URL() string {
	if u := os.Getenv("SUPERFIND_CHECKIN_URL"); u != "" {
		return u
	}
	return DefaultURL
}

type Reply struct {
	Latest     string
	RetryAfter time.Duration
}

const (
	sendTimeout = 8 * time.Second
	retryMin    = time.Hour
	retryMax    = 7 * 24 * time.Hour
	replyLimit  = 64 << 10
)

var client = &http.Client{
	Transport:     &http.Transport{Proxy: http.ProxyFromEnvironment},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func Send(ctx context.Context, e Enabled, url string, p Ping) (Reply, error) {
	if !e.on {
		return Reply{}, errors.New("telemetry is off")
	}
	body := p.JSON()
	if body == nil {
		return Reply{}, errors.New("the check-in can't be encoded")
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Reply{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "superfind/"+p.Version)
	resp, err := client.Do(req)
	if err != nil {
		return Reply{}, err
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, replyLimit))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		reply, ok := parseReply(answer)
		if !ok {
			return Reply{}, fmt.Errorf("%s answered HTTP %d with something other than a check-in reply", url, resp.StatusCode)
		}
		p.forget()
		return reply, nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		p.forget()
	}
	return Reply{}, fmt.Errorf("%s answered HTTP %d", url, resp.StatusCode)
}

func parseReply(answer []byte) (Reply, bool) {
	var raw struct {
		V          int     `json:"v"`
		Latest     *string `json:"latest"`
		RetryAfter *int64  `json:"retry_after_s"`
	}
	if json.Unmarshal(answer, &raw) != nil || raw.V != 1 {
		return Reply{}, false
	}
	reply := Reply{}
	if raw.Latest != nil {
		if v, err := release.ParseVersion(*raw.Latest); err == nil && v.String() == *raw.Latest {
			reply.Latest = *raw.Latest
		}
	}
	if raw.RetryAfter != nil {
		reply.RetryAfter = min(max(time.Duration(*raw.RetryAfter)*time.Second, retryMin), retryMax)
	}
	return reply, true
}
