package main

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/config"
	briefing "github.com/adammiribyan/superfind/internal/handoff"
	"github.com/adammiribyan/superfind/internal/mask"
	"github.com/adammiribyan/superfind/internal/session"
	"github.com/adammiribyan/superfind/internal/share"
	"github.com/adammiribyan/superfind/internal/show"
	"github.com/adammiribyan/superfind/internal/telemetry"
)

func page(tty *os.File, text string) {
	cmd := exec.Command("/bin/sh", "-c", cmp.Or(os.Getenv("PAGER"), "less"))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(text), tty, tty
	if cmd.Run() != nil {
		fmt.Fprint(tty, text)
	}
}

func runShare(args []string) {
	maxBytes, out, yes, target := briefing.DefaultMaxBytes, "", false, ""
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--yes":
			yes = true
		case arg == "--out" || arg == "--max-bytes":
			if i+1 == len(args) {
				usageError("superfind share: " + arg + " needs a value. See superfind help share.")
			}
			i++
			if arg == "--out" {
				out = args[i]
				continue
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				usageError("superfind share: --max-bytes needs a whole number, like --max-bytes 12000. See superfind help share.")
			}
			maxBytes = n
		case strings.HasPrefix(arg, "-"):
			usageError("superfind share: there is no " + arg + " option. See superfind help share.")
		case target != "":
			usageError("superfind share: one session at a time, not " + arg + " too.")
		default:
			target = arg
		}
	}
	if target == "" {
		usageError("superfind share: which session? Pass its id, or a path that superfind search prints first.")
	}
	tty := terminal()
	if tty == nil && !yes {
		usageError("superfind share: review a share before it leaves your machine. Run superfind share SESSION --out FILE " +
			"at a terminal, or pass --yes after reviewing the file.")
	}
	r, indexed, _ := findSession("share", target)
	if !indexed {
		fail("superfind share: not an indexed session: " + target)
	}
	t, err := agent.ReadTranscript(r.Agent, r.Path)
	if err != nil {
		fail("superfind share: couldn't read " + field(r.Path) + ": " + err.Error())
	}
	s := shareOf(r, t, maxBytes, time.Now())
	if out == "" {
		out = slug(s.Manifest.Title) + ".share.md"
	}
	taken := "superfind share: " + out + " already exists. Pass another --out FILE."
	if _, err := os.Lstat(out); err == nil {
		fail(taken)
	}
	var file bytes.Buffer
	if err := share.Write(&file, s); err != nil {
		fail("superfind share: " + err.Error())
	}
	secrets := "secrets"
	if s.Manifest.MaskedCount == 1 {
		secrets = "secret"
	}
	warning := fmt.Sprintf("Masked %d %s in known formats. Masking can't find customer data, unlabelled credentials, "+
		"or personal information. You are sharing what you reviewed.", s.Manifest.MaskedCount, secrets)
	if tty != nil && !yes {
		page(tty, file.String())
		fmt.Fprintln(stderr, warning)
		switch confirmed(tty, "Share this? [y/N]") {
		case 130:
			exit(130)
		case 1:
			fmt.Fprintln(stderr, "superfind share: nothing was written.")
			return
		}
	} else {
		fmt.Fprintln(stderr, warning)
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		fail(taken)
	}
	if err == nil {
		_, err = f.Write(file.Bytes())
		err = cmp.Or(err, f.Close())
	}
	if err != nil {
		fail("superfind share: couldn't write " + out + ": " + err.Error())
	}
	telemetry.Record(telemetry.ShareWritten{})
	fmt.Fprintf(stdout, "Wrote %s, %d bytes. Send it to your colleague. They run:\nsuperfind receive %s\n",
		out, file.Len(), agent.ShellQuote(filepath.Base(out)))
}

func shareOf(r session.Row, t agent.Transcript, maxBytes int, now time.Time) share.Share {
	home := agent.Home()
	for i := range t.Turns {
		t.Turns[i].Text = share.ScrubHome(t.Turns[i].Text, home)
	}
	p := briefing.Build(t, briefing.Options{MaxBytes: maxBytes})
	masked := 0
	for _, turn := range p.Turns {
		masked += mask.Count(agent.Printable(t.Turns[turn.Number-1].Text))
	}
	sender, set := config.Lookup("SUPERFIND_NAME")
	if !set {
		sender = os.Getenv("USER")
	}
	m := share.Manifest{Version: share.Version, BundleID: share.NewID(), CreatedAt: now.UTC().Truncate(time.Second),
		SenderName: sender, Agent: r.Agent.ID, AgentLabel: r.Agent.Label, Title: show.Clean(share.ScrubHome(r.Session.Title, home)),
		SourceSessionID: r.Session.ID, Branch: agent.Printable(r.Session.Branch), TurnsTotal: p.Total, TurnsShown: len(p.Turns),
		MaskedCount: masked, Omissions: share.Omissions}
	if c, err := share.Inspect(r.Session.Cwd); err == nil {
		m.RepoSubdir, m.HeadCommit, m.Branch = c.Subdir, c.Head, cmp.Or(c.Branch, m.Branch)
		if len(c.Remotes) > 0 {
			m.RepoRemote = c.Remotes[0]
		}
	}
	trail, _ := agent.ReadTrail(r.Agent, r.Path, r.Threads...)
	for _, pr := range trail.PRs {
		if ref := "https://github.com/" + pr.Repo + "/pull/" + strconv.Itoa(pr.Number); !slices.Contains(m.PRRefs, ref) {
			m.PRRefs = append(m.PRRefs, ref)
		}
	}
	return share.Share{Manifest: m, Briefing: share.Briefing(r.Agent, m, p)}
}

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(title string) string {
	s := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-")
	}
	return cmp.Or(s, "session")
}
