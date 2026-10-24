package rex

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Pane struct {
	Session string `json:"session"`
	Window  string `json:"window"`
	Block   string `json:"block"`
}

type Activity struct{ State, Need, Doing string }

type Held struct {
	Pane     Pane
	Activity *Activity
}

const (
	snapshotTimeout = 500 * time.Millisecond
	actionTimeout   = time.Second
	terminalShell   = "com.superlogical.terminal.shell"
)

//go:embed snapshot.lua
var snapshotScript string

func Inside() bool { return os.Getenv("REX_SERVER") != "" && os.Getenv("REX_SESSION") != "" }

func Session() string { return os.Getenv("REX_SESSION") }

func Snapshot() map[int]Held {
	if !Inside() {
		return map[int]Held{}
	}
	out, err := run(snapshotTimeout, "do", "-e", snapshotScript, "--json")
	if err != nil {
		return map[int]Held{}
	}
	return parseSnapshot(out)
}

func parseSnapshot(raw []byte) map[int]Held {
	var snap struct {
		Panes []struct {
			Session string          `json:"session"`
			Window  string          `json:"window"`
			Block   string          `json:"block"`
			PID     int             `json:"pid"`
			Records json.RawMessage `json:"records"`
		} `json:"panes"`
	}
	held := map[int]Held{}
	if json.Unmarshal(raw, &snap) != nil {
		return held
	}
	for _, p := range snap.Panes {
		pane := Pane{p.Session, p.Window, p.Block}
		if p.PID > 0 && pane.valid() {
			held[p.PID] = Held{pane, activityOf(p.Records, p.PID)}
		}
	}
	return held
}

func (p Pane) valid() bool {
	return strings.HasPrefix(p.Session, "session:") && strings.HasPrefix(p.Window, "window:") && strings.HasPrefix(p.Block, "block:")
}

func activityOf(raw json.RawMessage, pid int) *Activity {
	var records []struct {
		State string          `json:"state"`
		Kind  string          `json:"kind"`
		Msg   string          `json:"msg"`
		ID    json.RawMessage `json:"id"`
		Owner struct {
			PID int `json:"pid"`
		} `json:"owner"`
	}
	if json.Unmarshal(raw, &records) != nil {
		return nil
	}
	for _, r := range records {
		if r.Owner.PID != pid || len(r.ID) > 0 && string(r.ID) != "null" || r.State == "" {
			continue
		}
		a := &Activity{State: r.State, Doing: r.Msg}
		if r.State == "blocked" {
			a.Need = r.Kind
		}
		return a
	}
	return nil
}

func Focus(p Pane) bool {
	if !Inside() || !p.valid() {
		return false
	}
	if _, err := run(actionTimeout, "focus", "-s", p.Session, p.Block); err != nil {
		return false
	}
	return show(p.Session, p.Window) == nil
}

func OpenTab(session, label, cwd, command string) error {
	out, err := run(actionTimeout, "api", "call", "-s", session, "session.new_window", string(newWindow(session, label, cwd, command)))
	if err != nil {
		return failure(err)
	}
	var made struct {
		WindowID string `json:"window_id"`
	}
	if json.Unmarshal(out, &made) != nil || made.WindowID == "" {
		return errors.New("rex didn't say which tab it opened")
	}
	show(session, made.WindowID)
	return nil
}

func newWindow(session, label, cwd, command string) []byte {
	args, _ := json.Marshal(map[string]any{
		"session_id": session, "window_label": label, "focus": true,
		"layout": map[string]any{"block": map[string]any{
			"flavor": terminalShell, "options": map[string]string{"cwd": cwd, "initial_input": command + "\n"},
		}},
	})
	return args
}

func show(session, window string) error {
	_, err := run(actionTimeout, "do", "session.select", "session_id="+session, "window_id="+window)
	return err
}

func failure(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if line, _, _ := bytes.Cut(bytes.TrimSpace(exit.Stderr), []byte("\n")); len(line) > 0 {
			return errors.New(string(line))
		}
	}
	return err
}

func run(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "rex", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = timeout / 10
	return cmd.Output()
}
