# claude-sessions

Fuzzy-search every Claude Code session on your Mac, then jump into its directory and resume it.

Press Cmd-Shift-O in Ghostty, or Ctrl-X c in any zsh. Type to filter by title, project, or branch; space-separated words must all match. Enter runs `cd <dir> && claude --resume <id>` in the current pane and saves it to your shell history.

Each session is one line: its title, then how long ago it was active and its project. Sessions from the directory you're in come first; everything else follows, newest first.

The preview opens on where each session left off: your last request and the end of Claude's reply. Tab switches to the recent conversation. The preview sits beside the list in wide windows and below it in narrow ones; Ctrl-/ hides it, and Alt-Up and Alt-Down page through it. Colors come from your terminal theme, so the picker matches light and dark modes.

## Install

```sh
brew install fzf   # or brew upgrade fzf; the layout needs a 2025 or newer release
git clone <this repo> ~/src/claude-sessions
~/src/claude-sessions/install.sh
```

The installer links `claude-sessions` into `~/.local/bin`, sources the zsh widget from `~/.zshrc`, and adds the Ghostty keybind. Re-running it changes nothing. It needs python3, which the Xcode Command Line Tools provide.

## Behavior

- Sessions come from `~/.claude/projects`, plus every `~/.claude-*` directory, `$CLAUDE_CONFIG_DIR`, and any directories in colon-separated `$CLAUDE_SESSIONS_DIRS`. When more than one directory has sessions, each row shows which one it came from, and resume sets `CLAUDE_CONFIG_DIR` to match.
- Accounts you switch between with `/login` share one directory, so their sessions all appear. Resume uses whichever account is logged in now.
- Sessions whose directory was deleted (common with worktrees) are marked `folder removed`. Resume runs from the nearest parent that still exists.
- The index is cached in `~/.cache/claude-sessions/index.json`. Only transcripts that changed since the last run get re-read.
- Run `ccs` to use the picker without the keybind.
