# Changelog

All notable changes to superfind are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 0.9.0, superfind was called hop, and before 0.3.0, claude-sessions.

## [0.9.0] - Unreleased

### Changed

- hop is now **superfind**. The command is `superfind`, and `superfind init` also defines `sfd` as a shortcut. The module path is `github.com/adammiribyan/superfind`, and the Homebrew cask is `adammiribyan/tap/superfind`.
- "Deep search" is now **ask**. Ctrl-R in the picker still asks; on the command line, `superfind ask <words>` replaces `hop search --deep <words>`. "Search" now always means keyword search.
- The cache moves to `~/.cache/superfind/`, the settings file to `~/.config/superfind/env`, and the environment variables to `SUPERFIND_CLAUDE_DIRS`, `SUPERFIND_CODEX_DIRS`, and `SUPERFIND_GROK_DIRS`. Nothing is migrated: to upgrade from hop, replace `eval "$(hop init zsh)"` with `eval "$(superfind init zsh)"` (or the bash line), move `~/.config/hop/env` to `~/.config/superfind/env`, and delete `~/.cache/hop/`. The first run rebuilds the index.
- The code is split into internal packages behind a thin `main.go`. `go install` works as before.
- The picker header and the "Resuming" line no longer show Claude's asterisk.

### Added

- Codex and Grok sessions appear beside Claude Code sessions, and each resumes with the agent that made it (`codex resume` or `grok --resume`). Codex archives, Codex subagent and review threads, and Grok subagent runs are left out.
- `agent:claude`, `agent:codex`, and `agent:grok` filter the list. Rows name their agent when sessions come from more than one.
- `NO_COLOR` turns off every color in the picker and the command output, keeping bold, dim, and reverse video.
- The README reports label accuracy on hand-labeled Codex and Grok sessions.

### Removed

- `HOP_PLAIN_COLORS`. Use `NO_COLOR` instead.

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

- Throwaway sessions sort last and are left out of ask.
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

[0.9.0]: https://github.com/adammiribyan/superfind/compare/v0.8.1...HEAD
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
