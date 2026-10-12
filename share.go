package main

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/adammiribyan/superfind/internal/agent"
	"github.com/adammiribyan/superfind/internal/config"
	briefing "github.com/adammiribyan/superfind/internal/handoff"
	"github.com/adammiribyan/superfind/internal/mask"
	"github.com/adammiribyan/superfind/internal/session"
	"github.com/adammiribyan/superfind/internal/share"
	"github.com/adammiribyan/superfind/internal/show"
	"github.com/adammiribyan/superfind/internal/store"
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

func runReceive(args []string) {
	folder, file, printOnly, force := "", "", false, false
	var chosen *agent.Agent
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--print-only":
			printOnly = true
		case arg == "--force-folder":
			force = true
		case arg == "--folder" || arg == "--agent":
			if i+1 == len(args) {
				usageError("superfind receive: " + arg + " needs a value. See superfind help receive.")
			}
			i++
			if arg == "--folder" {
				folder = args[i]
			} else if chosen = agent.Named(args[i]); chosen == nil {
				usageError("superfind receive: --agent takes claude, codex, or grok, not " + args[i] + ".")
			}
		case strings.HasPrefix(arg, "-"):
			usageError("superfind receive: there is no " + arg + " option. See superfind help receive.")
		case file != "":
			usageError("superfind receive: one file at a time, not " + arg + " too.")
		default:
			file = arg
		}
	}
	if file == "" {
		usageError("superfind receive: which file? Pass the share file a colleague sent you.")
	}
	f, err := os.Open(file)
	if err != nil {
		fail("superfind receive: couldn't read " + file + ": " + err.Error())
	}
	raw, err := io.ReadAll(io.LimitReader(f, share.MaxBytes+1))
	f.Close()
	if err != nil {
		fail("superfind receive: couldn't read " + file + ": " + err.Error())
	}
	s, err := share.Parse(raw)
	if err != nil {
		advice := "Ask the sender to share it again."
		if errors.Is(err, share.ErrVersion) {
			advice = "Run superfind upgrade, or ask the sender to share it again."
		}
		fail("superfind receive: can't use " + file + ": " + err.Error() + ". " + advice)
	}
	m := s.Manifest
	if fresh, err := share.Remember(m, time.Now()); err != nil {
		fmt.Fprintln(stderr, "superfind receive: couldn't record this share in "+store.CacheDir()+": "+err.Error())
	} else if fresh {
		telemetry.Record(telemetry.ShareReceived{})
	}
	for _, line := range preview(m) {
		fmt.Fprintln(stdout, line)
	}
	tty := terminal()
	briefingText := strings.NewReplacer("\r", "", "\v", "", "\f", "").Replace(agent.Printable(s.Briefing))
	if tty != nil && term.IsTerminal(os.Stdout.Fd()) {
		output.Flush()
		page(tty, briefingText)
	} else {
		fmt.Fprint(stdout, briefingText)
		output.Flush()
	}
	if printOnly {
		return
	}
	a := cmp.Or(chosen, agent.Named(m.Agent))
	if a == nil {
		fail("superfind receive: this superfind can't start " + field(m.Agent) + ". Pass --agent claude, codex, or grok.")
	}
	c, err := share.Inspect(cmp.Or(folder, "."))
	switch {
	case err != nil && folder != "":
		fail("superfind receive: " + folder + " is not in a git repository. Pass --folder with a clone of " + cmp.Or(m.RepoRemote, "its repository") + ".")
	case err != nil:
		var found bool
		if c, found = cloneOf(m.RepoRemote); !found {
			fail("superfind receive: cd into a clone of " + cmp.Or(m.RepoRemote, "the shared repository") + ", or pass --folder DIR.")
		}
	}
	report := share.Check(c, m)
	lines := report.Lines(m)
	if report.Stops() && !force {
		fail("superfind receive: " + lines[0] + " Pass --folder with a clone of " + m.RepoRemote + ", or --force-folder to continue here anyway.")
	}
	dir := c.Root
	if sub := filepath.Join(c.Root, m.RepoSubdir); m.RepoSubdir != "" && dirExists(sub) {
		dir = sub
	}
	fmt.Fprintln(stdout, "\nContinuing in "+dir+".")
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	path := filepath.Join(c.GitDir, "superfind-share-"+m.BundleID+".md")
	if err := os.WriteFile(path, []byte(s.Briefing), 0o600); err != nil {
		fail("superfind receive: couldn't save the briefing in " + c.GitDir + ": " + err.Error())
	}
	argv := share.Start(a, share.Prompt(path, a))
	fmt.Fprintln(stdout, "\n"+share.Command(dir, argv))
	if tty == nil {
		return
	}
	output.Flush()
	switch confirmed(tty, "Start "+a.Label+" here? [y/N]") {
	case 130:
		exit(130)
	case 1:
		return
	}
	program, err := exec.LookPath(argv[0])
	if err != nil {
		fail("superfind receive: " + argv[0] + " isn't on your PATH. Install it, or pass --agent.")
	}
	telemetry.Record(telemetry.ShareStarted{})
	telemetry.Flush()
	if err := output.Flush(); err != nil {
		fail("superfind receive: couldn't write the output: " + err.Error())
	}
	if err := os.Chdir(dir); err != nil {
		fail("superfind receive: couldn't go to " + dir + ": " + err.Error())
	}
	err = syscall.Exec(program, argv, os.Environ())
	fail("superfind receive: couldn't run " + argv[0] + ": " + err.Error())
}

func preview(m share.Manifest) []string {
	secrets := "secrets"
	if m.MaskedCount == 1 {
		secrets = "secret"
	}
	lines := []string{
		"Shared by " + cmp.Or(field(m.SenderName), "someone") + " (self-asserted) on " + m.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"),
		"Agent: " + field(cmp.Or(m.AgentLabel, m.Agent)),
		"Title: " + field(m.Title),
		fmt.Sprintf("Turns: %d of %d shown", m.TurnsShown, m.TurnsTotal),
	}
	for _, f := range [][2]string{{"Repository", m.RepoRemote}, {"Branch", m.Branch}, {"Commit", m.HeadCommit}} {
		if f[1] != "" {
			lines = append(lines, f[0]+": "+field(f[1]))
		}
	}
	omitted := make([]string, len(m.Omissions))
	for i, o := range m.Omissions {
		omitted[i] = field(o)
	}
	return append(lines, "Left out: "+strings.Join(omitted, ", "), fmt.Sprintf("Masked: %d %s", m.MaskedCount, secrets), "")
}

func cloneOf(remote string) (share.Checkout, bool) {
	if remote == "" {
		return share.Checkout{}, false
	}
	rows := session.CachedRows()
	slices.SortFunc(rows, func(a, b session.Row) int { return cmp.Compare(b.Session.Mtime, a.Session.Mtime) })
	var checked []string
	for _, r := range rows {
		dir := r.Session.Cwd
		if slices.ContainsFunc(checked, func(root string) bool { return dir == root || strings.HasPrefix(dir, root+"/") }) {
			continue
		}
		c, err := share.Inspect(dir)
		if err != nil {
			continue
		}
		if slices.Contains(c.Remotes, remote) {
			return c, true
		}
		checked = append(checked, c.Root)
	}
	return share.Checkout{}, false
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
