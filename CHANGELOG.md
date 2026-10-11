# Changelog

All notable changes to superfind are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 0.9.0, superfind was called hop, and before 0.3.0, claude-sessions.

## [Unreleased]

### Added

- `curl -fsSL https://superfind.dev/install | sh` installs superfind without Go. It downloads the latest release for macOS or Linux on arm64 or amd64, checks it against the release's `checksums.txt`, installs it to `~/.local/bin`, and does the same setup as `install.sh`. A file that doesn't match its checksum installs nothing. `SUPERFIND_VERSION=0.10.0` installs that release instead of the latest.
- Shift-Up and Shift-Down page through the preview, as Alt-Up and Alt-Down do.

### Changed

- `install.sh` builds from source only when it runs from a superfind clone. Anywhere else, including piped to `sh`, it downloads a release.
- In a pane 16 rows tall or shorter, such as a tmux or zellij split, the search box takes one line instead of three. A 40×10 pane now shows 6 sessions instead of 3.
- When the preview is too short for your last request and the end of the reply, it keeps the start of your request and cuts the reply from its start. Before, a short preview showed only the end of the reply.
- Under the list, the preview now appears only when it has room for at least 6 lines below its heading. At 80×24 the list shows 5 sessions and the preview 7 lines, instead of 8 sessions and 3 lines. Beside the list, the preview is left out when the window is too short for 6 lines.
- When nothing in the preview matched your search, it no longer leaves two blank lines above "you", so the conversation gets one more line.
- When the footer is too narrow for every key, it leaves keys out in this order: "tab view", "ctrl-u clear search", "ctrl-r ask", "enter resume". "esc" goes last. A message too wide for the list leaves out its later sentences before anything else is cut.

### Fixed

- The picker waits for the terminal to report its colors before it first draws, instead of drawing after 300 ms whether or not they have arrived. A slow machine or a remote terminal no longer shows a frame without colors first. A terminal that doesn't report its colors gets the picker as soon as it answers, and one that answers nothing gets it after a second.
- A transcript line just over the 16 MiB limit is now skipped everywhere. Before, search and titles read it, while `show` and the preview left it out. A session already in the index picks this up the next time it changes.
- `show` masks a key whole even when part of it looks like another kind of key. Before, a bearer token, Slack token, or Google API key with an AWS-key-shaped piece inside kept the rest of its characters visible. A key used as the user name in a URL with a password is masked along with the password.

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

[Unreleased]: https://github.com/adammiribyan/superfind/compare/v0.10.0...HEAD
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
