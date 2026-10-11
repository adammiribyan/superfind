package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func lookup(m map[string]string) Env {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

func TestTelemetryIsOnOnlyWhenNothingSaysOff(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		file     map[string]string
		writable bool
		why      string
	}{
		{"nothing set", nil, nil, true, ""},
		{"empty variable", map[string]string{"SUPERFIND_TELEMETRY": ""}, nil, true, ""},
		{"on", map[string]string{"SUPERFIND_TELEMETRY": "on"}, nil, true, ""},
		{"1", map[string]string{"SUPERFIND_TELEMETRY": "1"}, nil, true, ""},
		{"TRUE", map[string]string{"SUPERFIND_TELEMETRY": "TRUE"}, nil, true, ""},
		{"off", map[string]string{"SUPERFIND_TELEMETRY": "off"}, nil, true, `SUPERFIND_TELEMETRY="off" is in your environment`},
		{"a typo", map[string]string{"SUPERFIND_TELEMETRY": "onn"}, nil, true, `SUPERFIND_TELEMETRY="onn" is in your environment`},
		{"0", map[string]string{"SUPERFIND_TELEMETRY": "0"}, nil, true, `SUPERFIND_TELEMETRY="0" is in your environment`},
		{"the file says off", nil, map[string]string{"SUPERFIND_TELEMETRY": "off"}, true, `SUPERFIND_TELEMETRY="off" is in ~/.config/superfind/env`},
		{"the file says on", nil, map[string]string{"SUPERFIND_TELEMETRY": "on"}, true, ""},
		{"the variable wins over the file", map[string]string{"SUPERFIND_TELEMETRY": "on"}, map[string]string{"SUPERFIND_TELEMETRY": "off"}, true, ""},
		{"an empty variable wins over the file", map[string]string{"SUPERFIND_TELEMETRY": ""}, map[string]string{"SUPERFIND_TELEMETRY": "off"}, true, ""},
		{"the variable can't override DO_NOT_TRACK", map[string]string{"SUPERFIND_TELEMETRY": "on", "DO_NOT_TRACK": "1"}, nil, true, "DO_NOT_TRACK is set"},
		{"DO_NOT_TRACK=true", map[string]string{"DO_NOT_TRACK": "true"}, nil, true, "DO_NOT_TRACK is set"},
		{"DO_NOT_TRACK=yes", map[string]string{"DO_NOT_TRACK": "yes"}, nil, true, "DO_NOT_TRACK is set"},
		{"DO_NOT_TRACK=0", map[string]string{"DO_NOT_TRACK": "0"}, nil, true, ""},
		{"DO_NOT_TRACK=false", map[string]string{"DO_NOT_TRACK": "false"}, nil, true, ""},
		{"DO_NOT_TRACK empty", map[string]string{"DO_NOT_TRACK": ""}, nil, true, ""},
		{"CI", map[string]string{"CI": "true"}, nil, true, "CI is set, so this looks like CI"},
		{"CI=1", map[string]string{"CI": "1"}, nil, true, "CI is set, so this looks like CI"},
		{"CI=false", map[string]string{"CI": "false"}, nil, true, ""},
		{"GitHub Actions", map[string]string{"GITHUB_ACTIONS": "true"}, nil, true, "GITHUB_ACTIONS is set, so this looks like CI"},
		{"GitLab", map[string]string{"GITLAB_CI": "true"}, nil, true, "GITLAB_CI is set, so this looks like CI"},
		{"Buildkite", map[string]string{"BUILDKITE": "true"}, nil, true, "BUILDKITE is set, so this looks like CI"},
		{"CircleCI", map[string]string{"CIRCLECI": "true"}, nil, true, "CIRCLECI is set, so this looks like CI"},
		{"Jenkins", map[string]string{"JENKINS_URL": "https://ci.example"}, nil, true, "JENKINS_URL is set, so this looks like CI"},
		{"Azure", map[string]string{"TF_BUILD": "True"}, nil, true, "TF_BUILD is set, so this looks like CI"},
		{"TeamCity", map[string]string{"TEAMCITY_VERSION": "2026.1"}, nil, true, "TEAMCITY_VERSION is set, so this looks like CI"},
		{"the variable can't override CI", map[string]string{"SUPERFIND_TELEMETRY": "on", "CI": "true"}, nil, true, "CI is set, so this looks like CI"},
		{"the variable's off is named before CI", map[string]string{"SUPERFIND_TELEMETRY": "off", "CI": "true"}, nil, true, `SUPERFIND_TELEMETRY="off" is in your environment`},
		{"a read-only state folder", nil, nil, false, "superfind can't write to its state folder, so it has nowhere to keep an install ID"},
		{"the variable can't override a read-only state folder", map[string]string{"SUPERFIND_TELEMETRY": "on"}, nil, false, "superfind can't write to its state folder, so it has nowhere to keep an install ID"},
	}
	for _, c := range cases {
		got := Resolve(lookup(c.env), lookup(c.file), func() bool { return c.writable })
		if got.Why() != c.why || got.On() != (c.why == "") {
			t.Errorf("%s: Resolve = on %v, %q; want on %v, %q", c.name, got.On(), got.Why(), c.why == "", c.why)
		}
		if _, ok := got.Enabled(); ok != (c.why == "") {
			t.Errorf("%s: Enabled = %v", c.name, ok)
		}
	}
}

func TestAnEnumMarshalsOnlyANameFromItsTable(t *testing.T) {
	for _, c := range []struct {
		value any
		want  string
	}{
		{Script, `"script"`}, {Homebrew, `"homebrew"`}, {GoInstall, `"go"`}, {Checkout, `"checkout"`}, {Unknown, `"unknown"`},
		{Installed, `"installed"`}, {Unchanged, `"unchanged"`}, {Failed, `"failed"`},
		{Method(0), ""}, {Method(6), ""}, {Method(255), ""}, {Outcome(0), ""}, {Outcome(4), ""},
		{Upgraded{Method: Script, Outcome: Installed}, `{"method":"script","outcome":"installed"}`},
		{Upgraded{Method: Script}, ""},
	} {
		raw, err := json.Marshal(c.value)
		if (err == nil) != (c.want != "") || string(raw) != c.want {
			t.Errorf("Marshal(%#v) = %q, %v; want %q", c.value, raw, err, c.want)
		}
	}
	for name, want := range map[string]Method{"script": Script, "homebrew": Homebrew, "go": GoInstall, "checkout": Checkout, "unknown": Unknown, "npm": Unknown, "": Unknown} {
		if got := ParseMethod(name); got != want {
			t.Errorf("ParseMethod(%q) = %d, want %d", name, got, want)
		}
	}
}

func unsafeFields(event any) []string {
	enumType := reflect.TypeOf((*enum)(nil)).Elem()
	var bad []string
	st := reflect.TypeOf(event)
	for i := range st.NumField() {
		f := st.Field(i)
		safe := f.Type.Kind() == reflect.Bool || (f.Type.Kind() == reflect.Uint8 && f.Type.Implements(enumType))
		if !safe {
			bad = append(bad, fmt.Sprintf("%s.%s is a %s", st.Name(), f.Name, f.Type))
		}
	}
	return bad
}

func TestEveryEventFieldIsAnEnumOrABool(t *testing.T) {
	for _, e := range Events {
		if bad := unsafeFields(e); bad != nil {
			t.Errorf("%v: only enum and bool fields can leave the machine", bad)
		}
	}
	type leaky struct {
		Query string
		Count int
		Props map[string]string
		Raw   uint8
		Ok    bool
	}
	if got := unsafeFields(leaky{}); !reflect.DeepEqual(got, []string{"leaky.Query is a string", "leaky.Count is a int", "leaky.Props is a map[string]string", "leaky.Raw is a uint8"}) {
		t.Errorf("the check let %v through", got)
	}
}

func home(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("SUPERFIND_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "")
	for _, name := range ciVariables {
		t.Setenv(name, "")
	}
	return dir
}

func state(dir, name string) string { return filepath.Join(dir, ".local", "state", "superfind", name) }

func queued(t *testing.T, dir, name, text string) {
	path := state(dir, filepath.Join("events", name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func queue(t *testing.T, dir string) []string {
	paths, _ := filepath.Glob(state(dir, filepath.Join("events", "*")))
	for i, p := range paths {
		paths[i] = filepath.Base(p)
	}
	return paths
}

const eventID = "0f8e2c1a-5b7d-4e3f-9a21-6c4d8b0e7f12"

var now = time.Date(2026, 10, 11, 14, 30, 0, 0, time.UTC)

func TestThePingIsTheWireBodyWithEventsOldestFirst(t *testing.T) {
	dir := home(t)
	if got := string(NextPing(Build{Version: "0.12.0", Method: Script}, now).JSON()); got != `{"schema":1,"id":"","version":"0.12.0","os":"`+runtime.GOOS+`","arch":"`+runtime.GOARCH+`","method":"script","events":[]}`+"\n" {
		t.Errorf("with nothing on disk the ping is %s", got)
	}
	os.MkdirAll(state(dir, ""), 0o700)
	os.WriteFile(state(dir, "id"), []byte("6f1c0e1a-3b4f-4b8e-9a52-0c7d6e2b9f11\n"), 0o600)
	queued(t, dir, "1760190000000000000-100.json", `{"id":"`+eventID+`","name":"upgraded","at":"2026-10-11T11:00:00Z","props":{"method":"script","outcome":"installed"}}`+"\n")
	queued(t, dir, "1760100000000000000-99.json", `{"id":"1f8e2c1a-5b7d-4e3f-9a21-6c4d8b0e7f12","name":"upgraded","at":"2026-10-09T14:00:00Z","props":{"method":"script","outcome":"failed"}}`+"\n")
	queued(t, dir, "1760190000000000001-100.json", `{"id":"`+eventID+`","name":"upgraded","at":"2099-01-01T00:00:00Z","props":{"method":"script","outcome":"unchanged"}}`+"\n")
	queued(t, dir, "1760190000000000002-100.json", "not json\n")
	queued(t, dir, "1760190000000000003-100.json", `{"id":"not-a-uuid","name":"upgraded","at":"2026-10-11T11:00:00Z","props":{}}`+"\n")
	queued(t, dir, "1760190000000000004-100.json", `{"id":"`+eventID+`","name":"upgraded","at":"2026-10-11T11:00:00Z","props":"text"}`+"\n")
	p := NextPing(Build{Version: "0.12.0", Method: Homebrew}, now)
	want := `{"schema":1,"id":"6f1c0e1a-3b4f-4b8e-9a52-0c7d6e2b9f11","version":"0.12.0","os":"` + runtime.GOOS + `","arch":"` + runtime.GOARCH + `","method":"homebrew","events":[` +
		`{"id":"1f8e2c1a-5b7d-4e3f-9a21-6c4d8b0e7f12","name":"upgraded","hours_ago":48,"props":{"method":"script","outcome":"failed"}},` +
		`{"id":"` + eventID + `","name":"upgraded","hours_ago":3,"props":{"method":"script","outcome":"installed"}},` +
		`{"id":"` + eventID + `","name":"upgraded","hours_ago":0,"props":{"method":"script","outcome":"unchanged"}}]}` + "\n"
	if got := string(p.JSON()); got != want {
		t.Errorf("ping is\n%s\nwant\n%s", got, want)
	}
	if got := queue(t, dir); !reflect.DeepEqual(got, []string{"1760100000000000000-99.json", "1760190000000000000-100.json", "1760190000000000001-100.json"}) {
		t.Errorf("after reading, the queue holds %v; unreadable files should be gone", got)
	}
}

func TestTheQueueKeepsTheNewest200AndRecordKeepsNothingWhenOff(t *testing.T) {
	dir := home(t)
	for i := range 205 {
		queued(t, dir, fmt.Sprintf("%019d-7.json", 1760000000000000000+i), `{"id":"`+eventID+`","name":"upgraded","at":"2026-10-11T11:00:00Z","props":{"method":"script","outcome":"installed"}}`+"\n")
	}
	p := NextPing(Build{Version: "0.12.0", Method: Script}, now)
	if len(p.Events) != 200 || len(queue(t, dir)) != 200 {
		t.Errorf("the ping holds %d events and the queue %d files, want 200 and 200", len(p.Events), len(queue(t, dir)))
	}
	if got := queue(t, dir)[0]; got != "1760000000000000005-7.json" {
		t.Errorf("the oldest kept file is %s, want the sixth", got)
	}

	dir = home(t)
	t.Setenv("DO_NOT_TRACK", "1")
	Record(Upgraded{Method: Script, Outcome: Installed})
	Flush()
	if got := queue(t, dir); len(got) != 0 {
		t.Errorf("with DO_NOT_TRACK set, Record wrote %v", got)
	}
	t.Setenv("DO_NOT_TRACK", "")
	Record(Upgraded{Method: Script, Outcome: Installed})
	Record(Upgraded{Method: Script, Outcome: Failed})
	Flush()
	files := queue(t, dir)
	if len(files) != 2 {
		t.Fatalf("after two records and a flush the queue holds %v", files)
	}
	p = NextPing(Build{Version: "0.12.0", Method: Script}, time.Now())
	if len(p.Events) != 2 || string(p.Events[0].Props) != `{"method":"script","outcome":"installed"}` || string(p.Events[1].Props) != `{"method":"script","outcome":"failed"}` || p.Events[0].ID == p.Events[1].ID || !uuidV4.MatchString(p.Events[0].ID) {
		t.Errorf("the flushed events read back as %+v", p.Events)
	}
}

type worker struct {
	status   int
	body     string
	location string
	mu       sync.Mutex
	got      []string
	seen     []Check
}

func (w *worker) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	body := make([]byte, r.ContentLength)
	r.Body.Read(body)
	w.mu.Lock()
	w.got = append(w.got, r.Method+" "+r.URL.Path+" "+r.Header.Get("User-Agent")+" "+r.Header.Get("Content-Type")+"\n"+string(body))
	c, _ := LastCheck()
	w.seen = append(w.seen, c)
	w.mu.Unlock()
	if w.location != "" {
		rw.Header().Set("Location", w.location)
	}
	rw.WriteHeader(w.status)
	rw.Write([]byte(w.body))
}

func TestSendDeletesTheQueueAfterAnAnswerAndKeepsItAfterAFailure(t *testing.T) {
	for _, c := range []struct {
		name     string
		status   int
		body     string
		location string
		kept     bool
		reply    Reply
		err      string
	}{
		{"an answer", 200, `{"v":1,"latest":"0.13.0"}`, "", false, Reply{Latest: "0.13.0"}, ""},
		{"an answer with no latest", 200, `{"v":1,"latest":null}`, "", false, Reply{}, ""},
		{"an answer with a loose version", 200, `{"v":1,"latest":"v0.13.0"}`, "", false, Reply{}, ""},
		{"an answer that asks for a week off", 200, `{"v":1,"latest":"0.13.0","retry_after_s":99999999}`, "", false, Reply{Latest: "0.13.0", RetryAfter: 7 * 24 * time.Hour}, ""},
		{"an answer that asks for a minute off", 200, `{"v":1,"latest":"0.13.0","retry_after_s":60}`, "", false, Reply{Latest: "0.13.0", RetryAfter: time.Hour}, ""},
		{"a 204", 204, "", "", true, Reply{}, "answered HTTP 204 with something other than a check-in reply"},
		{"HTML from a captive portal", 200, "<html>", "", true, Reply{}, "answered HTTP 200 with something other than a check-in reply"},
		{"a reply of another version", 200, `{"v":2,"latest":"0.13.0"}`, "", true, Reply{}, "answered HTTP 200 with something other than a check-in reply"},
		{"a redirect, not followed", 302, "", "https://github.com/adammiribyan/superfind", true, Reply{}, "answered HTTP 302"},
		{"a 400", 400, `{"error":"upgrade"}`, "", false, Reply{}, "answered HTTP 400"},
		{"a 404", 404, "", "", false, Reply{}, "answered HTTP 404"},
		{"a 500", 500, "", "", true, Reply{}, "answered HTTP 500"},
		{"a 503", 503, "", "", true, Reply{}, "answered HTTP 503"},
	} {
		dir := home(t)
		queued(t, dir, "1760190000000000000-100.json", `{"id":"`+eventID+`","name":"upgraded","at":"2026-10-11T11:00:00Z","props":{"method":"script","outcome":"installed"}}`+"\n")
		w := &worker{status: c.status, body: c.body, location: c.location}
		server := httptest.NewServer(w)
		p := NextPing(Build{Version: "0.12.0", Method: Script}, now)
		p.ID = "6f1c0e1a-3b4f-4b8e-9a52-0c7d6e2b9f11"
		enabled, _ := Current().Enabled()
		reply, err := Send(context.Background(), enabled, server.URL+"/v1/checkin", p)
		server.Close()
		problem := ""
		if err != nil {
			problem = err.Error()
		}
		if reply != c.reply || !strings.HasSuffix(problem, c.err) || (c.err == "") != (err == nil) {
			t.Errorf("%s: Send = %+v, %q; want %+v, %q", c.name, reply, problem, c.reply, c.err)
		}
		if got := len(queue(t, dir)) == 1; got != c.kept {
			t.Errorf("%s: queue kept = %v, want %v", c.name, got, c.kept)
		}
		want := "POST /v1/checkin superfind/0.12.0 application/json\n" + string(p.JSON())
		if len(w.got) != 1 || w.got[0] != want {
			t.Errorf("%s: the server got %q, want %q", c.name, w.got, want)
		}
	}
}

func TestSendNeedsAnEnabledSettingAndNeverAsksTheNetworkWithout(t *testing.T) {
	home(t)
	w := &worker{status: 200, body: `{"v":1,"latest":"0.13.0"}`}
	server := httptest.NewServer(w)
	defer server.Close()
	if _, err := Send(context.Background(), Enabled{}, server.URL, NextPing(Build{Version: "0.12.0", Method: Script}, now)); err == nil || err.Error() != "telemetry is off" {
		t.Errorf("Send with a zero Enabled = %v", err)
	}
	if len(w.got) != 0 {
		t.Errorf("the server got %v", w.got)
	}
}

func TestDueHonorsTheNextTimeAndAClockThatMovedBack(t *testing.T) {
	for _, c := range []struct {
		name string
		c    Check
		want bool
	}{
		{"never checked", Check{}, true},
		{"next is now", Check{Next: now}, true},
		{"next was an hour ago", Check{Next: now.Add(-time.Hour)}, true},
		{"next is in an hour", Check{Next: now.Add(time.Hour)}, false},
		{"next is in a week", Check{Next: now.Add(7 * 24 * time.Hour)}, false},
		{"next is past any wait, so the clock moved back", Check{Next: now.Add(7*24*time.Hour + time.Second)}, true},
	} {
		if got := Due(c.c, now); got != c.want {
			t.Errorf("%s: Due = %v, want %v", c.name, got, c.want)
		}
	}
	for failures, want := range map[int]time.Duration{0: 20 * time.Hour, 1: time.Hour, 2: 2 * time.Hour, 3: 4 * time.Hour, 4: 8 * time.Hour, 5: 16 * time.Hour, 6: 20 * time.Hour, 40: 20 * time.Hour} {
		if got := wait(failures); got != want {
			t.Errorf("wait(%d) = %v, want %v", failures, got, want)
		}
	}
}

func TestACheckinRecordsTheAttemptBeforeAskingAndTheAnswerAfter(t *testing.T) {
	dir := home(t)
	queued(t, dir, "1760190000000000000-100.json", `{"id":"`+eventID+`","name":"upgraded","at":"2026-10-11T11:00:00Z","props":{"method":"script","outcome":"installed"}}`+"\n")
	w := &worker{status: 200, body: `{"v":1,"latest":"99.0.0"}`}
	server := httptest.NewServer(w)
	defer server.Close()
	t.Setenv("SUPERFIND_CHECKIN_URL", server.URL+"/v1/checkin")
	build := Build{Version: "0.12.0", Method: Script}
	before := time.Now()
	Checkin(context.Background(), build)
	id, ok := LoadID()
	if !ok {
		t.Fatal("the check-in made no install ID")
	}
	if len(w.got) != 1 || !strings.Contains(w.got[0], `"id":"`+id+`","version":"0.12.0"`) || !strings.Contains(w.got[0], `"outcome":"installed"`) {
		t.Fatalf("the server got %q", w.got)
	}
	seen := w.seen[0]
	if seen.Failures != 1 || seen.Latest != "" || seen.Next.Before(before.Add(time.Hour)) || seen.Next.After(time.Now().Add(time.Hour)) {
		t.Errorf("while the request was in flight check.json held %+v, want one failure and a retry in an hour", seen)
	}
	c, _ := LastCheck()
	if c.Failures != 0 || c.Latest != "99.0.0" || c.Next.Before(before.Add(20*time.Hour)) || c.Next.After(time.Now().Add(20*time.Hour)) {
		t.Errorf("after the answer check.json holds %+v, want latest 99.0.0 and the next check in 20h", c)
	}
	if got := queue(t, dir); len(got) != 0 {
		t.Errorf("the sent event is still queued: %v", got)
	}
	Checkin(context.Background(), build)
	if len(w.got) != 1 {
		t.Errorf("a second check-in the same day asked again: %d requests", len(w.got))
	}
	if again, _ := LoadID(); again != id {
		t.Errorf("the install ID changed from %s to %s", id, again)
	}
}

func TestACheckinThatFailsKeepsTheQueueAndBacksOff(t *testing.T) {
	dir := home(t)
	queued(t, dir, "1760190000000000000-100.json", `{"id":"`+eventID+`","name":"upgraded","at":"2026-10-11T11:00:00Z","props":{"method":"script","outcome":"installed"}}`+"\n")
	os.WriteFile(state(dir, "check.json"), []byte(`{"next":"2026-10-11T10:00:00Z","latest":"0.12.1","failures":2}`+"\n"), 0o600)
	w := &worker{status: 500}
	server := httptest.NewServer(w)
	defer server.Close()
	t.Setenv("SUPERFIND_CHECKIN_URL", server.URL+"/v1/checkin")
	before := time.Now()
	Checkin(context.Background(), Build{Version: "0.12.0", Method: Script})
	c, _ := LastCheck()
	if c.Failures != 3 || c.Latest != "0.12.1" || c.Next.Before(before.Add(4*time.Hour)) || c.Next.After(time.Now().Add(4*time.Hour)) {
		t.Errorf("after a third failure check.json holds %+v, want the last latest kept and a retry in 4h", c)
	}
	if got := queue(t, dir); len(got) != 1 {
		t.Errorf("after a 500 the queue holds %v, want the event kept", got)
	}
	w.status, w.body = 400, `{"error":"upgrade"}`
	os.WriteFile(state(dir, "check.json"), []byte(`{"next":"2026-10-11T10:00:00Z","failures":0}`+"\n"), 0o600)
	Checkin(context.Background(), Build{Version: "0.12.0", Method: Script})
	if got := queue(t, dir); len(got) != 0 {
		t.Errorf("after a 400 the queue holds %v, want it dropped", got)
	}
	if len(w.got) != 2 {
		t.Errorf("the server got %d requests, want 2", len(w.got))
	}
}

func TestACheckinWithTelemetryOffTouchesNothing(t *testing.T) {
	dir := home(t)
	t.Setenv("SUPERFIND_TELEMETRY", "off")
	w := &worker{status: 200, body: `{"v":1,"latest":"99.0.0"}`}
	server := httptest.NewServer(w)
	defer server.Close()
	t.Setenv("SUPERFIND_CHECKIN_URL", server.URL+"/v1/checkin")
	Checkin(context.Background(), Build{Version: "0.12.0", Method: Script})
	if len(w.got) != 0 {
		t.Errorf("the server got %v", w.got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".local")); !os.IsNotExist(err) {
		t.Errorf("the check-in wrote under %s/.local", dir)
	}
}

func TestTurnOffWritesTheSettingAndForgetsEverythingCollected(t *testing.T) {
	dir := home(t)
	os.MkdirAll(filepath.Join(dir, ".config", "superfind"), 0o700)
	os.WriteFile(filepath.Join(dir, ".config", "superfind", "env"), []byte("TYPESAFE_API_KEY=abc\n"), 0o600)
	os.MkdirAll(state(dir, ""), 0o700)
	os.WriteFile(state(dir, "id"), []byte("6f1c0e1a-3b4f-4b8e-9a52-0c7d6e2b9f11\n"), 0o600)
	os.WriteFile(state(dir, "check.json"), []byte(`{"next":"2026-10-11T10:00:00Z","failures":0}`+"\n"), 0o600)
	os.WriteFile(state(dir, "install.json"), []byte(`{"method":"script","path":"/x"}`+"\n"), 0o600)
	queued(t, dir, "1760190000000000000-100.json", "{}\n")
	os.Unsetenv("SUPERFIND_TELEMETRY")
	if err := TurnOff(); err != nil {
		t.Fatal(err)
	}
	if got := Current(); got.Why() != `SUPERFIND_TELEMETRY="off" is in ~/.config/superfind/env` {
		t.Errorf("after TurnOff, Current = %q", got.Why())
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".config", "superfind", "env"))
	if string(env) != "TYPESAFE_API_KEY=abc\nSUPERFIND_TELEMETRY=off\n" {
		t.Errorf("env file holds %q", env)
	}
	entries, _ := os.ReadDir(state(dir, ""))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !reflect.DeepEqual(names, []string{"install.json"}) {
		t.Errorf("the state folder holds %v, want only the install receipt", names)
	}
	if err := TurnOn(); err != nil {
		t.Fatal(err)
	}
	if got := Current(); !got.On() {
		t.Errorf("after TurnOn, Current = %q", got.Why())
	}
	env, _ = os.ReadFile(filepath.Join(dir, ".config", "superfind", "env"))
	if string(env) != "TYPESAFE_API_KEY=abc\n" {
		t.Errorf("env file holds %q", env)
	}
}
