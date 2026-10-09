# superfind

Find any past AI coding session and resume it. superfind reads Claude Code, Codex, and Grok sessions, opens the session's folder, and resumes it with the agent that made it, right in the terminal pane you're in. It runs on macOS and Linux, in zsh or bash.

![superfind finding a session from a description and resuming it](demo/superfind.gif)

## Install

With Homebrew:

```sh
brew install --cask adammiribyan/tap/superfind
```

With Go 1.26 or newer:

```sh
go install github.com/adammiribyan/superfind@latest
```

Then add one line to your shell's startup file, so choosing a session can change your shell's folder:

```sh
eval "$(superfind init zsh)"     # in ~/.zshrc
eval "$(superfind init bash)"    # in ~/.bashrc, or ~/.bash_profile on macOS
```

Open a new terminal tab and type `superfind`, or `sfd` for short. To open it with Cmd-Shift-O in Ghostty, add this line to your Ghostty config:

```
keybind = super+shift+o=text:\x18c
```

To build from source instead, clone this repo and run `./install.sh`. It builds superfind with Go, copies it to `~/.local/bin`, adds the setup line for your shell, and adds the Ghostty keybind if Ghostty is installed. Running it again changes nothing.

superfind resumes a session with the agent that made it, so it needs [Claude Code](https://code.claude.com/docs/en/overview), [Codex](https://developers.openai.com/codex/cli), or [Grok](https://docs.x.ai/build/overview) on your PATH for that session. It's a single binary with no other dependencies.

## Use

Type `superfind` or `sfd`, press **Ctrl-X c** in zsh or bash, or press **Cmd-Shift-O** in Ghostty. The picker opens full screen and leaves your terminal exactly as it was when you choose or cancel.

| Key | Does |
|---|---|
| type | Search titles, projects, branches, and the conversations themselves for the words you type. Every word must match. Titles that match come first. |
| Ctrl-R | Ask: find the session you describe, even when it used other words. "where did we stop charging people twice" finds "Checkout button double-charges on slow networks". |
| Enter | Resume the selected session: `cd` into its folder and run its agent's resume command (`claude --resume`, `codex resume`, or `grok --resume`). The command lands in your shell history. |
| Tab | Switch the preview between where the session left off and the recent conversation. |
| ↑ ↓, Ctrl-P Ctrl-N, PgUp PgDn | Move through the list. |
| Ctrl-/ | Hide or show the preview. Alt-Up and Alt-Down page through it. |
| Ctrl-U | Clear the search. |
| `is:waiting` | Show only sessions waiting on you. `is:unfinished` works the same way. |
| `agent:codex` | Show only Codex sessions. `agent:claude` and `agent:grok` work the same way. |

Each row shows a session's title, how long ago it was active, and its project. When your sessions come from more than one agent, each row also names its agent. Sessions from the folder you're in come first, then everything else, newest first. One-line "hi" sessions that did no work sink to the bottom.

The preview opens on where the session left off: your last real request and the end of the reply. When you search, its fourth line shows the passage that matched.

A session written in the last two minutes shows **active now**, because it's probably open in another window. Resuming it anyway prints a reminder.

## Waiting on you

With a TypeSafe key, superfind marks sessions that need you. **waiting on you** means the agent stopped on a question, an approval, or a choice it can't make alone. **unfinished** means work stopped partway: the agent was interrupted, hit an error, or never answered. Sessions that simply finished, including ones that end with an optional "want me to also…?", stay unmarked. Type `is:waiting` or `is:unfinished` to list only those.

[Jev](https://docs.typesafe.ai) reads how each session ended: the last six turns, clipped, and whether the agent was mid-reply, mid-command, or interrupted. A label appears only when Jev is at least 85% sure. It disappears as soon as the session changes, and after two weeks away, since a session you haven't returned to in that long isn't really waiting. On 21 hand-labeled Claude Code sessions, 13 of the 15 labels shown were right, and 1 of the 14 sessions that needed a label got none. On 42 hand-labeled Codex and Grok sessions, all 3 labels shown were right, and all 3 sessions that needed a label got one. Treat labels as a hint. Labeling runs in the background when the picker opens and only reads recent sessions that changed, so opening never waits on it.

## Ask

Ask (Ctrl-R in the picker, or `superfind ask`) ranks sessions with [Jev](https://docs.typesafe.ai) when a TypeSafe API key is configured, and with Claude Haiku otherwise.

Measured on 21 test searches, each phrased differently from the session it should find, over two or three runs:

| | Jev | Claude Haiku |
|---|---|---|
| Right session ranked first | 16–17 | 18–19 |
| Right session in the top 3 | 19–20 | 19–20 |
| Time to results, median | 1.4 s | 3.3–3.8 s |
| Time to results, slowest | 1.7 s | 8.6 s |
| Cost per search | $0.0036 | $0.0073 |

Claude puts the right session first slightly more often. Jev is about three times faster and half the price.

With Jev, the preview also shows the passage that best matches your search for the top five results. It appears about a second after the results.

Without a TypeSafe key, ask uses Claude Haiku through your Claude Code login, whichever agent made the sessions, so it needs Claude Code installed and counts toward your Claude plan. To use Jev instead, get a key from [TypeSafe](https://docs.typesafe.ai) and set `TYPESAFE_API_KEY` in your environment, or put this line in `~/.config/superfind/env` (in a source checkout, a git-ignored `.env` at the repo root also works):

```sh
TYPESAFE_API_KEY=your-key
```

Setting `TYPESAFE_API_KEY=` to an empty value turns Jev off even when a file has a key. `TYPESAFE_URL` sends requests to another endpoint, such as a proxy; it defaults to `https://api.typesafe.ai/v1/systemone`. If asking fails, the line under the search box says why, and typing goes back to search.

**What leaves your machine.** Asking sends each session's title, project, last request, and one short passage to the ranker: to TypeSafe with a key, to Anthropic without one. With a key, explanations also send up to 120 short passages from each of the top five sessions to TypeSafe, and labeling sends the ending of every session (its last six turns, clipped) to TypeSafe once, and again whenever it changes. Without a key nothing is labeled. Typing never sends anything; search runs locally.

## Sessions it finds

- Claude Code: `~/.claude/projects`, every `~/.claude-*` folder, `$CLAUDE_CONFIG_DIR`, and any folders listed in colon-separated `$SUPERFIND_CLAUDE_DIRS`.
- Codex: `~/.codex/sessions`, every `~/.codex-*` folder, `$CODEX_HOME`, and `$SUPERFIND_CODEX_DIRS`. Sessions you archived and threads Codex started on its own, such as subagents and reviews, are left out.
- Grok: `~/.grok/sessions`, every `~/.grok-*` folder, `$GROK_HOME`, and `$SUPERFIND_GROK_DIRS`. Subagent runs are left out.
- When one agent's sessions come from more than one folder, each row says which, and resuming sets that agent's home variable (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, or `GROK_HOME`) to match.
- Accounts you switch between with `/login` share one folder, so all their sessions appear. Resuming uses whichever account is logged in now.
- A session whose folder was deleted, which is common with worktrees, is marked **folder removed** and resumes from the nearest parent folder that still exists.

## Commands

```sh
superfind                    # open the picker; sfd is short for it
superfind init zsh|bash      # print the shell setup
superfind search <words>     # print sessions that contain every word
superfind ask <words>        # print the sessions that best fit your description
superfind preview <session>  # print where a session left off (--recent, --width N, --query Q)
superfind command <session>  # print the command that resumes a session
superfind index              # build or refresh the index
superfind label              # mark sessions waiting on you (needs a TypeSafe key)
superfind --version
```

A session is the path `superfind search` prints first: a Claude Code or Codex transcript, or a Grok session's `chat_history.jsonl`.

The index lives in `~/.cache/superfind/`, or `$XDG_CACHE_HOME/superfind/` when that is set. Each run re-reads only the sessions that changed, so opening the picker stays instant. The first run reads every session in parallel: about four seconds for 540 sessions and 1.6 GB of transcripts here, with a short "Indexing…" line while it works.

superfind takes its colors from your terminal. Until your terminal reports them, superfind shows the selected row in reverse video, and it switches to the tinted row as soon as they arrive. Set [`NO_COLOR`](https://no-color.org) to any value to turn color off: superfind then uses only bold, dim, and reverse video, and always shows the selected row in reverse video.

## Develop

```sh
go build -o bin/superfind .              # build
python3 -m unittest discover -s tests    # behavior tests against bin/superfind, using fake transcripts and homes
vhs demo/superfind.tape                  # re-record demo/superfind.gif from fake sessions
```

The picker and shell tests drive real terminals through tmux, so they need tmux, zsh, and bash. CI runs everything on macOS and Linux.

To release, set `version` in `main.go`, commit, and push a tag like `v0.9.0`. The release workflow checks that the tag matches `superfind --version`, then builds the binaries and updates the Homebrew tap.

## License

MIT. superfind is an independent project, not made or endorsed by Anthropic, OpenAI, or xAI.
