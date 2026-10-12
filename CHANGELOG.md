# Changelog

All notable changes to superfind are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 0.9.0, superfind was called hop, and before 0.3.0, claude-sessions.


## [0.13.0] - 2026-10-12

> file: finds sessions by file, and blame is instant

### Added

- `superfind show --json` gives each commit in `outcomes` a `body`: its message after the subject, masked like the turns and cut to 2,000 bytes. The bodies share 12,000 bytes or half of `--max-bytes`, whichever is less, newest first; `--max-bytes 0` lifts that. A body is empty for a pull request, and for a commit that a squash merge carried after its branch was deleted.
- `file:PATH` filters the picker, `search`, `ask`, and `last` to sessions that edited a file, or a file in a folder, with an edit tool, in any worktree of its repository, including a Claude Code worktree since removed. `has:commit` keeps sessions that ran `git commit`, leaving out a commit that failed on its own.

### Changed

- `superfind blame FILE` now also reads the file's commits that only other branches, remotes, and tags hold: up to 30 more, after the last 30 on this branch. A session that committed the file on a branch that was never merged, or that was squash-merged under a message that doesn't list its commits, now shows up with the commit it made. A merge on another branch counts only when it changed the file beyond what its parents had.
- `superfind show` says "committed, on no branch" for a commit that no branch, tag, stash, or checkout holds any more, such as one whose branch was deleted before it merged, instead of "committed, not pushed". `show --json` gives it the stage `dangling`.
- `superfind blame FILE` answers from the index instead of reading every transcript again. On about 950 sessions it went from about 9.4 to 0.18 seconds of CPU time.
- `superfind blame` finds edits a session made in a Claude Code worktree (`.claude/worktrees/…`) after that worktree was removed.
- Titles and last requests are masked before the index cuts them to length, so a secret that the cut would split, such as a quoted password, is masked whole. The picker and `ask` see the masked text too.
- The index also records the files each session edited or read and the commits it ran. The first run after upgrading rebuilds the index, which took about 4.5 seconds for 950 sessions.

### Fixed

- `show` and `handoff` mask Slack, Discord, and Microsoft Teams webhook URLs, Tailscale keys (`tskey-auth-…`, `tskey-client-…`, `tskey-api-…`), and tokens, keys, and signatures in a URL's query, such as `&token=…`, `&sig=…`, or `X-Amz-Signature=…`. Before, they printed them whole. A query value with no digit, such as a placeholder, is left as it is.

## [0.12.0] - 2026-10-11

> superfind upgrade installs new versions in place

### Added

- `superfind upgrade` installs the newest release in place of a copy that the install script put in `~/.local/bin`, after checking that the release's `checksums.txt` is signed with the superfind release key and that the archive matches it. It keeps the copy it replaced, and `superfind upgrade --rollback` swaps back to it; a second `--rollback` swaps forward again. `--check` says whether a newer release is out and changes nothing, and `--version X` installs that release, also to go back to an older one. A copy from Homebrew, `go install`, or a clone is left alone: upgrade prints the command that updates it. The install script now writes a receipt, `~/.local/state/superfind/install.json`, so upgrade knows which copy it may replace.
- Once a day, when you open the picker, superfind asks superfind.dev for the newest release and sends one anonymous check-in: a random install id, its version, your OS and CPU type, how it was installed, and the outcome of any `superfind upgrade` you ran. It never sends a search, a path, a project name, or session text, and superfind.dev passes the check-in to PostHog without your IP address. The first picker tells you, in the line under the search box, and later pickers say when a newer release is out. `superfind telemetry` prints exactly what the next check-in sends, and `superfind telemetry off` turns it off, as do `SUPERFIND_TELEMETRY=off`, `DO_NOT_TRACK=1`, and CI. Scripted commands, agents, and `--json` never check in. [SECURITY.md](SECURITY.md#what-leaves-your-machine) lists exactly what is sent.
- The first time you open the picker after superfind updates, the line under the search box says what's new in that version, in one line. It stays while the picker is open, gives way to search messages while they apply, and doesn't show again. A new install doesn't show it.
- Releases include `checksums.txt.minisig`, a minisign signature of `checksums.txt` made with the superfind release key, so you can check that a download is the release the maintainer signed. [SECURITY.md](SECURITY.md#verify-a-release) has the commands.


## [0.11.0] - 2026-10-11

> Handoff, last, filters, fish, and a skill for agents

### Added

- On Omarchy, the install script binds Super-Shift-Ctrl-F to open superfind in a new terminal, and leaves out the Ghostty key there. It works for the curl install too. `superfind pick --run` resumes the chosen session itself, for a terminal with no shell to hand the command to. On a session an agent already has open, it does what Enter chose in the picker.
- A `superfind` skill for coding agents, so that asking Claude Code, Codex, or Grok "where did we fix X last week?" or "continue my Codex session here" makes it search your sessions and read a handoff instead of guessing. Install it in most agents with `npx skills add adammiribyan/superfind`, or as a plugin from this repository in Claude Code, Codex, Grok, or Cursor; [skills/README.md](skills/README.md) has the commands.
- `superfind show` lists the commits a session made and the pull requests it opened, and how far each got: committed, pushed, merged into the default branch (directly or in a squash), or merged and then reverted. It reads only local git, as of your last fetch. `show --json` has them as `outcomes`. Merged means the change was accepted, not that it was right.
- In a terminal that supports the Program Status Protocol (OSC 7501), the picker's tab or pane shows that superfind is working while the first index runs and while ask works. The status is cleared before superfind exits or the agent starts. Other terminals ignore it.
- `search` and `ask` take `--limit N`, or `--limit=N`, to print only the first N sessions, with or without `--json`.
- `superfind help search` ends with examples for `jq`: the best match's id, every match as one array, and how many match.
- `llms.txt` at the repo root, and `llms-full.txt` attached to each release, describe superfind's install, commands, JSON fields, and exit codes for AI assistants and agents. Each release also attaches `install.sh`.
- `curl -fsSL https://superfind.dev/install | sh` installs superfind without Go. It downloads the latest release for macOS or Linux on arm64 or amd64, checks it against the release's `checksums.txt`, installs it to `~/.local/bin`, and does the same setup as `install.sh`. A file that doesn't match its checksum installs nothing. `SUPERFIND_VERSION=0.10.0` installs that release instead of the latest.
- `superfind handoff <session> [words]` prints a briefing for another agent to continue a session's work, for example a Codex session in Claude Code. It has the session's folder, branch, and resume command, how the session ended, and excerpts of the conversation with their turn numbers. The excerpts are the first and last requests, what followed the last one, and the turns that contain your words. They are quoted under a note that they are a record and not instructions, masked like `show`, and kept to 12,000 bytes by default (`--max-bytes N`). `--json` prints the same as one object.
- fish support: `superfind init fish | source` in `~/.config/fish/config.fish` sets up the same `superfind` and `sfd` commands, `superfind last`, and Ctrl-X c binding as zsh and bash, and `install.sh` adds that line for fish users. It needs fish 3.1 or later. Before fish 4.0, the resume command doesn't land in your history.
- Shift-Up and Shift-Down page through the preview, as Alt-Up and Alt-Down do.
- `in:here`, `branch:NAME`, `after:WHEN`, and `before:WHEN` filter the picker, `search`, and `ask`. `in:here` keeps sessions started in the folder you're in, or in a folder that contains it, as the picker's "here" does; sessions from folders inside it don't count. `branch:` matches the branch a session ended on. `after:` and `before:` compare the time a session was last active with the start of a day in your time zone, such as `after:2026-10-09`, `after:yesterday`, or `before:today`, or with a time ago, such as `after:3d`, `12h`, or `2w`.
- `superfind last` resumes the newest session started in the folder you're in, or in a folder that contains it, with no picker. It takes the same filters, such as `sfd last agent:codex`. Through the shell setup it `cd`s and resumes as Enter does; in a script or an agent's shell it prints the title and the resume command instead, and `--json` prints the session as JSON.
- `is:open` lists only the sessions that a running Claude Code, Codex, or Grok has open right now. Their rows say "open" instead of "active now" or the time.
- `search --json`, `ask --json`, and `show --json` have a new field, `open`: the process id of the agent that has the session open, whether Claude Code is busy or idle, and the tmux pane it runs in. It is `null` when no agent has the session open.
- Codex counts as open only when it was started with `codex resume <id>`, because Codex keeps no list of running sessions.
- Enter in the picker, and `superfind last`, no longer start a second copy of a session that an agent already has open. If the agent runs in a pane of the tmux server you're in, they switch to that pane. If it runs in a Ghostty tab and your Ghostty can say which terminal each tab uses (newer builds than 1.3.1), they switch to that tab. Otherwise the picker asks first: Enter again resumes a copy, and Esc goes back. `superfind last` asks the same at a terminal, and in a script it says where the session is open and exits 1.

### Changed

- superfind is now licensed under the Apache License 2.0 instead of MIT. Releases up to 0.10.0 stay under MIT.
- `search` and `ask` reject an option that starts with one dash, such as `-n 5`, and exit 2. They used to search for it as a word, so `-n 5` usually found nothing. To search for such a word, put `--` first.
- `superfind --help` and `superfind help search` say that `--json` prints JSON Lines, one object per line rather than one array, and how to get the first result or one array with `jq`.
- `superfind help search` says how sessions are ordered with no words: the ones from this folder or a folder that holds it first, then newest first, and very short ones last. It used to say newest first.
- `install.sh` builds from source only when it runs from a superfind clone. Anywhere else, including piped to `sh`, it downloads a release.
- The index is rebuilt once after upgrading, to record how each session ended.
- That rebuild starts the search index afresh instead of deleting every row from it, so it ends up the size of a new index (about 17% smaller on a 10,000-session test corpus) and stays searchable from other windows while it runs.
- A Claude Code session that ends on a command answered on your machine, such as `/compact`, now counts as ending where it was before that command, for both labels.
- Building the index from scratch takes time in line with the number of sessions, where it grew with their square. Refreshing it after sessions change reads the search index once, not once per changed session.
- `search --json` takes about as long as plain `search`, even for a word in nearly every session. On 600 generated sessions, `search --json the` took 13 seconds and now takes under a quarter of a second. `ask` also prepares its question faster, and the preview finds the passage that matched sooner in long sessions.
- `ask --json` fills `match` with a passage that holds some of your words when the conversation doesn't hold them all, and `superfind help ask` says so. Common words such as `the` don't count. A session rarely holds every word of a description, so most `ask` results used to have an empty `match`.
- In a conversation longer than about 2,000 characters, `match` can show a different passage than before. It comes from where your less common words appear together most, or from the start of the conversation when every word you typed is very common. Shorter conversations show the same passage as before.
- A filter that superfind can't read, such as `after:2026-1`, makes `search`, `ask`, and `last` exit 2 and say what the filter takes, before `ask` sends anything. In the picker, the filter is left out and the line under the search box says what it takes.
- `superfind show` holds only the turns it prints, instead of the whole session, and masks secrets only in the text it measures. The output is the same as before. On test sessions, `show --tail 10 --json` peaked at 0.30 GB instead of 1.1 GB on a 561 MB session, most of it the search index, and took 2 s instead of 19 to 27 s on a 126 MB session that ends in one long agent run.
- A session that ends in one long agent run no longer slows the index and the preview down with the run's length. On a 126 MB test session, the preview took 4 to 6 s instead of 18 to 23 s and peaked at 0.39 to 0.43 GB instead of 0.89 to 1.0 GB.
- The preview reads the end of a session line by line instead of loading it, so a long run of tool output no longer has to fit in memory. On the same 126 MB session it peaked at 0.29 to 0.35 GB instead of 0.39 to 0.43 GB.
- A line between 16 MiB and 17 MiB long is now skipped by the index as well as the preview, so search no longer finds a prompt the preview can't show.
- The preview renders only the end of a long reply, the part it shows, and keeps code blocks that began earlier formatted as code. It looks the same as before. On a 126 MB test session that ends in a 40 MB reply, the preview took 2.4 to 2.7 s instead of 5.0 to 6.2 s.
- In a pane 23 rows tall or shorter, such as a tmux or zellij split, the search box takes one line instead of three. A 40×10 pane now shows 6 sessions instead of 3, and an 80×20 pane has room for 16 instead of 13.
- When the preview is too short for your last request and the end of the reply, it keeps the start of your request and cuts the reply from its start. Before, a short preview showed only the end of the reply.
- Under the list, the preview now appears only when it has room for at least 6 lines below its heading. At 80×24 the list shows 5 sessions and the preview 7 lines, instead of 8 sessions and 3 lines. Beside the list, the preview is left out when the window is too short for 6 lines.
- When nothing in the preview matched your search, it no longer leaves two blank lines above "you", so the conversation gets one more line.
- When the footer is too narrow for every key, it leaves keys out in this order: "tab view", "ctrl-u clear search", "ctrl-r ask", "enter resume". "esc" goes last. A message too wide for the list leaves out its later sentences before anything else is cut.

### Fixed

- `superfind` and `sfd` work in Claude Code's Bash tool when your shell setup comes from `superfind init`. Claude Code copies your shell functions into its own shell but leaves out functions whose names start with one underscore, so every `superfind` command there failed with "command not found: _superfind_run". The `superfind` function no longer calls a helper, in zsh, bash, and fish.
- When two folders share one sessions folder through a link, as Omarchy's agent accounts do, each session is listed once. If `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, or `GROK_HOME` points at one of them, sessions resume as that account. Before, each session showed twice, and one copy resumed as the other account.
- A resume command whose folder contains a backslash now runs correctly in fish as well as in zsh and bash. Session ids and agent homes that start with `=` or are exactly `%self` are now quoted, so zsh and fish no longer expand them.
- superfind works inside a sandbox that can't write to its cache, such as Codex's `read-only` mode. Every command used to fail there with "locking the index: operation not permitted". Now it reads the index as it was last written and changes nothing in the cache. `search` and `ask` say on stderr when sessions changed since then, because they match the words those sessions held at the last index. `index` and `label` exit 1 there. With no usable index, superfind says to run `superfind index` once outside the sandbox or to set `XDG_CACHE_HOME`.
- `superfind blame <commit>` prints the session that made a commit, from any agent: the one that ran its `git commit`, then any that opened its pull request. A squash merge leads to the sessions that made the commits it squashed. `blame <file>` lists the sessions that edited a file, or made or opened the pull request of one of its last 30 commits, and `blame <file>:<line>` blames the commit that last changed that line. Each line's summary starts with what the session did, such as `made this commit` or `opened #19`, and `--json` puts it in `match`.
- Without a TypeSafe key, sessions that ended on an interruption, mid-command, or on a request the agent never answered are marked **unfinished**, and `is:unfinished` finds them. superfind reads this from the session file and sends nothing. **waiting on you** still needs a key, and with a key Jev still decides both labels.
- The picker waits for the terminal to report its colors before it first draws, instead of drawing after 300 ms whether or not they have arrived. A slow machine or a remote terminal no longer shows a frame without colors first. A terminal that doesn't report its colors gets the picker as soon as it answers, and one that answers nothing gets it after a second.
- A transcript line just over the 16 MiB limit is now skipped everywhere. Before, search and titles read it, while `show` and the preview left it out. A session already in the index picks this up the next time it changes.
- `show` masks a key whole even when part of it looks like another kind of key. Before, a bearer token, Slack token, or Google API key with an AWS-key-shaped piece inside kept the rest of its characters visible. A key used as the user name in a URL with a password is masked along with the password.
- Indexing a session that holds an enormous line uses less memory, and about the same amount every time.

## [0.10.0] - 2026-10-10

### Added

- `superfind show <session>` prints a session's conversation for scripts and agents: what you asked and what the agent answered, without color or wrapping. `--json` prints it as one JSON object, `--tail N` keeps the last N turns, and `--max-bytes N` sets the size limit (60,000 bytes by default). Keys, tokens, and passwords in common formats are replaced with `[masked …]`.
- `search --json` and `ask --json` print one JSON object per session, with the session's id, folder, title, kind, the passage that matched, and the command that resumes it.
- `show`, `preview`, and `command` take a session's id, or a unique start of one at least 8 characters long, as well as its path. An id that matches more than one session exits 2 and lists them.
- `is:agent` lists sessions that another program started: runs of `claude -p` (even ones you ran yourself) and the Claude Agent SDK, runs of `codex exec`, headless Grok runs, and Claude Code sessions driven only by another Claude Code session.
- The README has a new section, "Use from scripts and agents", and `superfind --help` lists the exit codes.

### Changed

- Sessions that another program started no longer appear in the picker, `search`, or `ask` unless you add `is:agent`. `--json` output always includes them, marked `"kind": "agent"`.
- Claude Code sessions whose every request came from another Claude Code session used to be hidden entirely. They now appear under `is:agent`.
- Labeling skips sessions another program started, so their endings are no longer sent to TypeSafe.
- The index is rebuilt once after upgrading, to record which sessions another program started.

## [0.9.1] - 2026-10-10

### Changed

- Picker rows put the project, the agent, and the age in columns that line up from row to row. Long titles are cut instead of the age, and a label such as "waiting on you" follows the title. A row with a label gives up its agent cell, then its project cell, before its title gets too short. In a narrow window the agent column goes first, then the project column.
- Sessions from a folder that contains the one you're in count as here. They come first, and their project is left blank. Your home folder and the root folder count only for sessions started right there.
- Among search results that match about as well, newer sessions come first. Tested on the maintainer's 576 sessions, weighted by the age of the sessions they actually resumed, this ranked the right session first in 36% of title-word searches instead of 32%.
- A title word that starts with a word you typed is bold, and blue when the terminal reports its colors.
- Beside the preview, the list takes about half the width (54% instead of 45%).
- The selected row has a stronger tint, a bold title, and full-strength text.
- After an ask, the search box is titled "Sessions by description", and Esc goes back to search instead of closing the picker. Esc during an ask stops it.
- When no session contains the words you typed, the picker says that Ctrl-R asks by description.
- The search box no longer has a "›" mark.

## [0.9.0] - 2026-10-10

### Changed

- hop is now **superfind**. The command is `superfind`, and `superfind init` also defines `sfd` as a shortcut. The module path is `github.com/adammiribyan/superfind`, and the Homebrew cask is `adammiribyan/tap/superfind`.
- "Deep search" is now **ask**. Ctrl-R in the picker still asks; on the command line, `superfind ask <words>` replaces `hop search --deep <words>`. "Search" now always means keyword search.
- The cache moves to `~/.cache/superfind/`, the settings file to `~/.config/superfind/env`, and the environment variables to `SUPERFIND_CLAUDE_DIRS`, `SUPERFIND_CODEX_DIRS`, and `SUPERFIND_GROK_DIRS`. Nothing is migrated: to upgrade from hop, replace `eval "$(hop init zsh)"` with `eval "$(superfind init zsh)"` (or the bash line), move `~/.config/hop/env` to `~/.config/superfind/env`, and delete `~/.cache/hop/`. The first run rebuilds the index.
- The code is split into internal packages behind a thin `main.go`. `go install` works as before.
- The picker header and the "Resuming" line no longer show Claude's asterisk.
- With a TypeSafe key, a TypeSafe failure is reported instead of quietly asking Anthropic, and when Jev finds nothing the ask ends with no results.
- Ask runs Claude Code from an empty folder without your settings, memory, MCP servers, or CLAUDE.md files. On the maintainer's machine this cut a request from 97,770 input tokens to 523.
- Keyword search uses every word you type: one-letter words match whole words, and common words such as "the" are no longer dropped.
- Usage mistakes exit 2, and `search` and `ask` reject flags they don't know instead of searching for them.

### Added

- Codex and Grok sessions appear beside Claude Code sessions, and each resumes with the agent that made it (`codex resume` or `grok --resume`). Codex archives, Codex threads started by another Codex thread (such as subagents and reviews), and Grok subagent runs are left out; `codex exec` runs are listed.
- `agent:claude`, `agent:codex`, and `agent:grok` filter the list. Rows name their agent when sessions come from more than one.
- `NO_COLOR` turns off every color in the picker and the command output, keeping bold, dim, and reverse video.
- The README reports label accuracy on hand-labeled Codex and Grok sessions.
- `--help`, `-h`, `superfind help <command>`, and `<command> --help`.
- On a machine with no sessions, superfind says where it looked.
- The macOS binaries are signed with a Developer ID certificate and notarized by Apple, so the Homebrew cask no longer removes macOS's quarantine flag.

### Removed

- `HOP_PLAIN_COLORS`. Use `NO_COLOR` instead.

### Fixed

- A damaged or empty search index is rebuilt instead of failing or finding nothing.
- A pasted log no longer becomes a 20,000-character title: titles are clipped to 200 characters and last requests to 400.
- Words wrapped in escape codes are searchable, and a stray escape sequence no longer hides the rest of a conversation from search.
- A session folder that can't be read keeps its sessions and is reported, instead of looking empty.
- A FIFO or a symlink to `/dev/zero` among the sessions no longer hangs every command, and a line over 16 MB is skipped.
- A prompt that starts with HTML or JSX is still read as a prompt.
- A Grok session whose `summary.json` is half-written is listed, and read again once it is complete.
- Previewing a huge pasted word no longer takes time quadratic in its length.
- Pressing Ctrl-C during `superfind ask`, or quitting the picker while it asks, stops Claude Code.
- A line that cut a word starting with a wide character came out one column too wide.
- A failed copy in `install.sh` keeps the installed superfind.
- With `HOME` unset, the cache goes to your home folder instead of the current folder.
- The picker leaves the terminal's cursor color alone, and redraws after the terminal shrinks to one cell and back.
- Labels that arrive while `is:waiting` is typed show up, and filters keep the matched passage in the preview.
- `command` and `preview` accept the path `search` printed, even under a symlinked folder.
- A write that fails, such as to a full disk, exits 1.

### Security

- The cache folder is readable only by your account, and an existing install is tightened on the next run. Before, any local account could read indexed conversations.
- Session text can no longer send escape sequences, control characters, or bidi overrides to your terminal.
- A session whose id starts with `-` is never listed, so it can't become an option to the agent's command.

## [0.8.1] - 2026-10-09

### Changed

- The picker opens in about 70 ms with the list and preview drawn together, measured end to end in tmux. On a 50 MB session it used to take about 0.87 s to show the preview.
- The preview reads only the end of a transcript, growing the read until it holds the turns on screen.
- The picker opens from the cached index and refreshes it in the background, keeping the selected session selected when the list updates.
- Nothing is drawn until the terminal's colors and the first preview are ready, so the picker never restyles itself after it appears.

## [0.8.0] - 2026-10-09

### Added

- bash support: `hop init bash` sets up the same `hop` function and Ctrl-X c binding as zsh.
- Release builds for macOS and Linux on arm64 and amd64, and a Homebrew cask.
- `go install github.com/adammiribyan/hop@latest`.
- MIT license.

### Changed

- `install.sh` copies the binary instead of linking into the clone, sets up whichever of zsh or bash you use, and edits the Ghostty config only when Ghostty is installed.

## [0.7.3] - 2026-10-09

### Fixed

- A transcript that failed to read was labeled as if it were empty, and was never labeled again. It is now retried on the next run.
- A preview of a transcript that failed to read said "No reply yet" instead of saying it couldn't read the session. `hop preview` now exits 1 in that case.

## [0.7.2] - 2026-10-09

### Changed

- The index, the preview, and labeling share one transcript reader that decodes each line's JSON.
- `search`, `preview`, and `command` refresh the index first, so a session written since the picker last opened is found.
- `hop label` exits non-zero when it has no key, when the index fails, when a judgment fails, or when it cannot save.
- Sessions report the branch they ended on.

### Fixed

- A transcript written with spaces after colons indexed to nothing.
- A tool call whose input contained a user message was read as one of your prompts.
- A transcript that failed to read was cached as empty instead of retried.
- A broken search index showed "No sessions match" instead of its error.

## [0.7.1] - 2026-10-09

### Changed

- Ask becomes **deep search**, on Ctrl-R. Default macOS terminals send Option-Enter as a plain Enter, so the old key resumed the selected session instead. Alt-Enter still works where the terminal sends it. `hop ask` becomes `hop search --deep`.

### Fixed

- Small terminals crashed the picker.
- A slow ask or explanation could show results for an earlier search.
- The resume command did not quote the session id, so a crafted transcript name could run shell commands.
- The search index failed when another process was writing it, and when the cache path was relative or contained URI characters.
- Wide characters and tabs wrapped at the wrong width in the preview.
- Pasting into the search box did not work.
- Malformed TypeSafe answers could be cached as labels.
- `hop init zsh` needed `~/.local/bin` on PATH.

## [0.7.0] - 2026-10-09

### Changed

- hop is a single Go binary with its own full-screen picker. fzf and Python are no longer needed.
- Keyword search takes 1–2 ms per keystroke, down from about 30 ms. The first index of 360 sessions takes about 1 s, down from 3 s.
- `~/.zshrc` uses `eval "$(hop init zsh)"` instead of sourcing a file from the clone.

### Added

- `hop command <session>` prints the command that resumes a session.
- `hop preview` takes `--width`, `--recent`, and `--query`.
- The TypeSafe key can live in `~/.config/hop/env`.

### Fixed

- One-letter words, such as the "s" of a possessive, matched nearly every conversation and slowed asking to about 7 s. They are now ignored.
- Sessions with the same modification time kept no stable order.

## [0.6.0] - 2026-10-09

### Added

- The first run prints "Indexing N sessions…" while it reads every transcript.
- When asking fails, the line under the search box says why.
- `HOP_PLAIN_COLORS=1` always uses reverse video for the selected row.

### Changed

- Labels disappear after two weeks away, since a session you haven't returned to in that long isn't waiting on you.
- An empty `TYPESAFE_API_KEY` turns Jev off even when a file has a key.

## [0.5.0] - 2026-10-09

### Added

- With a TypeSafe key, rows say **waiting on you** or **unfinished**, and `is:waiting` and `is:unfinished` filter the list. A label appears only when Jev is at least 85% sure, and disappears as soon as the session changes.
- `hop label` labels sessions in the background when the picker opens.

### Fixed

- Bold text that wrapped across lines showed its `**` markers.

## [0.4.0] - 2026-10-09

### Added

- Ask ranks with Jev when a TypeSafe key is configured, and with Claude Haiku otherwise.
- With Jev, the preview shows the passage that best matches your question for the top five results.
- A session written in the last two minutes shows **active now**.
- `hop index` and `hop --version`.

### Changed

- Very short sessions (two prompts or fewer, under 150 words, and no tool calls) sort last and are left out of ask.
- The preview opens on your last request longer than three words, so "done" no longer stands in for the work.

### Fixed

- A search with no results showed one blank result.

## [0.3.0] - 2026-10-09

### Changed

- claude-sessions is now **hop**, since it will read more agents than Claude Code. Typing `hop` opens the picker. The cache moves to `~/.cache/hop`.

## [0.2.0] - 2026-10-09

### Added

- Search covers the conversations themselves as well as titles, projects, and branches. Every word must match, and title matches come first.
- The preview shows the passage that matched.
- Option-Enter asks Claude Haiku to rank sessions by how well they fit a description.

## [0.1.0] - 2026-10-09

### Added

- claude-sessions: a picker for resuming Claude Code sessions, opened with Ctrl-X c in zsh or Cmd-Shift-O in Ghostty. Enter changes to the session's folder and runs `claude --resume` in the same pane.
- The preview opens on where the session left off, and Tab switches to the recent conversation.
- Sessions in every `~/.claude*` folder are found. A deleted worktree resumes from its nearest parent folder that still exists.

[0.13.0]: https://github.com/adammiribyan/superfind/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/adammiribyan/superfind/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/adammiribyan/superfind/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/adammiribyan/superfind/compare/v0.9.1...v0.10.0
[0.9.1]: https://github.com/adammiribyan/superfind/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/adammiribyan/superfind/compare/v0.8.1...v0.9.0
[0.8.1]: https://github.com/adammiribyan/superfind/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/adammiribyan/superfind/compare/v0.7.3...v0.8.0
[0.7.3]: https://github.com/adammiribyan/superfind/compare/v0.7.2...v0.7.3
[0.7.2]: https://github.com/adammiribyan/superfind/compare/v0.7.1...v0.7.2
[0.7.1]: https://github.com/adammiribyan/superfind/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/adammiribyan/superfind/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/adammiribyan/superfind/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/adammiribyan/superfind/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/adammiribyan/superfind/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/adammiribyan/superfind/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/adammiribyan/superfind/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/adammiribyan/superfind/releases/tag/v0.1.0
