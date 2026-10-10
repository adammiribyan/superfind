<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/superfind-lockup-white.svg">
    <img alt="superfind" src="assets/superfind-lockup-ink.svg" width="300">
  </picture>
</p>

superfind reads your Claude Code, Codex, and Grok sessions and finds the one you want, by the words in it or by a description. Then it opens the session's folder and resumes it with the agent that made it, right in the terminal pane you're in. It runs on macOS and Linux, in zsh or bash.

![superfind searching sessions from three agents, asking for one by description, and resuming it](demo/superfind.gif)

## Install

superfind is a single binary. To resume a session it runs the agent that made it, so you need [Claude Code](https://code.claude.com/docs/en/overview), [Codex](https://learn.chatgpt.com/docs/codex/cli), or [Grok](https://docs.x.ai/build/overview) on your PATH for that session. Installing with Go, or from source, needs Go 1.21 or newer; if yours is older than 1.26, Go downloads 1.26 the first time.

There are two ways to install. Install with Homebrew or Go and do the setup yourself, or build [from source](#from-source) with `install.sh`, which does the setup for you.

### With Homebrew or Go

```sh
brew install --cask adammiribyan/tap/superfind
# or
go install github.com/adammiribyan/superfind@latest
```

Then add one line to your shell's startup file. It defines the `superfind` and `sfd` commands, which open the picker and move your shell to the folder of the session you choose:

```sh
eval "$(superfind init zsh)"     # in ~/.zshrc
eval "$(superfind init bash)"    # in ~/.bashrc, or ~/.bash_profile on macOS
```

To open superfind with Cmd-Shift-O in Ghostty, also add this line to your Ghostty config, then reload the config with Cmd-Shift-, (comma):

```
keybind = super+shift+o=text:\x18c
```

### From source

Use this instead of the steps above; it does all of them.

```sh
git clone https://github.com/adammiribyan/superfind.git
cd superfind
./install.sh
```

`install.sh` builds superfind, copies it to `~/.local/bin`, and adds the setup line to the startup file named above for your shell. If Ghostty is installed, it also adds the keybind above to your Ghostty config (`$XDG_CONFIG_HOME/ghostty/config`, usually `~/.config/ghostty/config`, or the macOS path when only that one exists), creating the file if needed. Running it again rebuilds superfind but never adds a line twice.

`~/.local/bin` doesn't need to be on your PATH: the setup line defines `superfind` as a shell function that runs the binary by its full path, and passes along every command, such as `superfind ask`. Add it to your PATH only to run superfind from scripts or other shells.

### Check it works

The setup line takes effect in shells started after you add it, so open a new terminal tab, or `source` the startup file in the shell you have. Then type `superfind`, or `sfd` for short. If the picker doesn't open, run `type superfind`: it should say superfind is a function. If it doesn't, the setup line didn't run.

## Use

Open the picker in any of these ways:

- Run `superfind`, or `sfd` for short.
- In zsh or bash, press **Ctrl-X**, then **c**.
- In Ghostty, press **Cmd-Shift-O**.

The picker opens full screen. When you choose or cancel, it closes and leaves your scrollback as it was.

| Key | Does |
|---|---|
| Type words | Search titles, projects, branches, and the conversations themselves. Every word must match. Titles that match come first. |
| Ctrl-R | Ask: find the session you describe, even when it used other words. See [Ask for the session you remember](#ask-for-the-session-you-remember). |
| Enter | Resume the selected session: `cd` into its folder and run its agent's resume command (`claude --resume`, `codex resume`, or `grok --resume`). |
| Tab | Switch the preview between where the session left off and the recent conversation. |
| ↑ ↓, Ctrl-P Ctrl-N, PgUp PgDn | Move through the list. |
| Ctrl-/ | Hide or show the preview. Alt-Up and Alt-Down page through it. |
| Ctrl-U | Clear the search. |
| Esc, Ctrl-C | Close the picker without resuming anything. |

Add `agent:claude`, `agent:codex`, or `agent:grok` to show one agent's sessions, and, with a TypeSafe key, `is:waiting` or `is:unfinished` to show only sessions with that mark (see [Waiting on you](#waiting-on-you)). Filters work with or without other words.

Each row shows a session's title, how long ago it was active, and its project. When your sessions come from more than one agent, each row also names its agent. Sessions from the folder you're in come first, then everything else, newest first. Very short sessions sink to the bottom: ones with two prompts or fewer, under 150 words in all, and no tool calls, such as a lone "hi".

The preview opens on where the session left off: your last real request and the end of the reply. When you search, its fourth line shows the passage that matched.

When you resume, superfind prints `Resuming` and the session's title, and the agent takes over the terminal. The command, such as `cd /Users/you/code/storefront && claude --resume <id>`, lands in your shell history, and when you leave the agent your shell stays in that folder.

A session written in the last two minutes shows **active now**, because it's probably open in another window. Resuming it anyway prints a reminder.

superfind takes its colors from your terminal. If your terminal doesn't report them, the selected row is shown in reverse video instead of tinted. Set [`NO_COLOR`](https://no-color.org) to any non-empty value to turn color off: superfind then uses only bold, dim, and reverse video.

## Ask for the session you remember

Often you remember what a session was about but not the words it used. Type a description in the search box and press **Ctrl-R**; ask uses whatever the box holds, including any `agent:` or `is:` filter. While you type, the list may show no keyword matches; that doesn't matter. While it asks, the search box is titled "Asking…". Then the list holds only the sessions that fit, best match first, so it may hold just one. Typing again goes back to search.

For example, "where did we stop charging people twice" finds "Checkout button double-charges on slow networks", while typing the same words finds nothing. From the command line, `superfind ask where did we stop charging people twice` prints the same sessions as tab-separated lines (see [Commands](#commands)).

Ask ranks sessions with [Jev](https://docs.typesafe.ai) when a TypeSafe API key is configured, and with Claude Haiku otherwise. Either way, asking sends some session text off your machine; [What leaves your machine](#what-leaves-your-machine) lists exactly what. With a key, if TypeSafe fails, asking says so instead of trying Claude.

Measured on 2026-10-10 on 21 test searches over the author's own sessions, each phrased differently from the session it should find, over three runs:

| | Jev | Claude Haiku |
|---|---|---|
| Right session ranked first | 13–14 | 13–14 |
| Right session in the top 3 | 18 | 16–17 |
| Time to results, median | 1.6 s | 5.4–5.7 s |
| Time to results, slowest | 1.9 s | 10.3 s |
| Cost per search (measured 2026-10-09) | $0.0036 | $0.0073 |

The two put the right session first equally often. Jev puts it in the top 3 slightly more often, is about three and a half times faster at the median, and costs about half as much per search.

With Jev, the preview also shows the passage that best matches your search for the top five results. It appears shortly after the results.

Without a TypeSafe key, ask uses Claude Haiku through your Claude Code login, whichever agent made the sessions, so it needs Claude Code installed and counts toward your Claude plan. Claude Code runs from an empty folder, without your settings, memory, MCP servers, or CLAUDE.md files. To use Jev instead, get a key from [TypeSafe](https://docs.typesafe.ai) and set `TYPESAFE_API_KEY` in your environment, or put this line in `~/.config/superfind/env`:

```sh
TYPESAFE_API_KEY=your-key
```

Setting `TYPESAFE_API_KEY=` to an empty value turns Jev off even when a file has a key. `TYPESAFE_URL` sends requests to another endpoint, such as a proxy; it defaults to `https://api.typesafe.ai/v1/systemone`.

If asking fails, the line under the search box says why, and typing goes back to search. For example, "Claude Code didn't answer" usually means Claude Code isn't installed or isn't logged in; run `claude` once to log in.

## Waiting on you

With a TypeSafe key, superfind marks sessions that need you. **waiting on you** means the agent stopped on a question, an approval, or a choice it can't make alone. **unfinished** means work stopped partway: the agent was interrupted, hit an error, or never answered. Sessions that simply finished, including ones that end with an optional "want me to also…?", stay unmarked. Type `is:waiting` or `is:unfinished` to list only those.

[Jev](https://docs.typesafe.ai) reads how each session ended: the last six turns, clipped, and whether the agent was mid-reply, mid-command, or interrupted. A label appears only when Jev is at least 85% sure. It disappears as soon as the session changes, and after two weeks away, since a session you haven't returned to in that long isn't really waiting. On 21 hand-labeled Claude Code sessions, 13 of the 15 labels shown were right, and 1 of the 14 sessions that needed a label got none. On 42 hand-labeled Codex and Grok sessions, all 3 labels shown were right, and all 3 sessions that needed a label got one. Treat labels as a hint. Labeling runs in the background when the picker opens and only reads recent sessions that changed, so opening never waits on it.

## What leaves your machine

Asking sends the ranker your description and, for each session except very short ones (two prompts or fewer, under 150 words, and no tool calls), its title, project, age, last request, and one short passage: to TypeSafe with a key, or to Anthropic through Claude Code without one. If TypeSafe fails, asking says so instead of trying Anthropic. With a key, explanations also send your description and up to 120 short passages from each of the top five results to TypeSafe. Labeling sends the ending of each session that changed in the last two weeks (its last six turns, clipped, and how it stopped) to TypeSafe once, and again whenever it changes; older sessions and very short ones are never sent, and a session active now waits until it goes quiet. Without a key nothing is labeled. Typing never sends anything; search runs locally.

superfind has no telemetry, analytics, or update checks. The requests above are the only ones it makes, so without a TypeSafe key, nothing leaves your machine until you ask.

## Sessions it finds

- Claude Code: `~/.claude/projects`, every `~/.claude-*` folder, `$CLAUDE_CONFIG_DIR`, and any folders listed in colon-separated `$SUPERFIND_CLAUDE_DIRS`.
- Codex: `~/.codex/sessions`, every `~/.codex-*` folder, `$CODEX_HOME`, and `$SUPERFIND_CODEX_DIRS`. Sessions you archived are left out, and so are threads that another Codex thread started, such as subagents and reviews. Runs of `codex exec` are listed like any other session.
- Grok: `~/.grok/sessions`, every `~/.grok-*` folder, `$GROK_HOME`, and `$SUPERFIND_GROK_DIRS`. Subagent runs are left out.
- When one agent's sessions come from more than one folder, each row says which, and resuming sets that agent's home variable (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, or `GROK_HOME`) to match.
- Claude Code accounts you switch between with `/login` share one folder, so all their sessions appear. Resuming uses whichever account is logged in now.
- A session whose folder was deleted, which is common with worktrees, is marked **folder removed** and resumes from the nearest parent folder that still exists.

## Commands

```sh
superfind                    # open the picker; sfd is short for it
superfind init zsh|bash      # print the shell setup
superfind search <words>     # print sessions that contain every word
superfind ask <words>        # print the sessions that best fit your description
superfind preview <session>  # print where a session left off (--recent, --width N, --query Q)
superfind command <session>  # print the command that resumes a session
superfind pick               # open the picker, then print the chosen session's title and resume command
superfind index              # build or refresh the index
superfind label              # mark sessions waiting on you (needs a TypeSafe key)
superfind help <command>     # explain one command; --help works too
superfind --version
```

`search` and `ask` print one tab-separated line per session, meant for scripts: the session, its folder, its id, the agent's home folder, its title, and the row as the picker shows it. A session is that first field: a Claude Code or Codex transcript, or a Grok session's `chat_history.jsonl`. Pass it to `preview` or `command`.

The index lives in `~/.cache/superfind/`, or `$XDG_CACHE_HOME/superfind/` when that is set, and only your account can read it. Each run re-reads only the sessions that changed, and the picker opens from the index while it refreshes in the background. The first run reads every session in parallel: about four seconds for 540 sessions and 1.6 GB of transcripts here, with a short "Indexing…" line while it works.

## Settings

These environment variables change what superfind does:

| Variable | Does | See |
|---|---|---|
| `TYPESAFE_API_KEY` | Ask with Jev and turn on labels. Can also go in `~/.config/superfind/env`. | [Ask](#ask-for-the-session-you-remember) |
| `TYPESAFE_URL` | Send TypeSafe requests to another endpoint. | [Ask](#ask-for-the-session-you-remember) |
| `SUPERFIND_CLAUDE_DIRS`, `SUPERFIND_CODEX_DIRS`, `SUPERFIND_GROK_DIRS` | Read sessions from more folders, separated by colons. | [Sessions it finds](#sessions-it-finds) |
| `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `GROK_HOME` | The agents' own home variables. superfind reads sessions there as well as in the default folders, and sets the variable when it resumes a session from another folder. | [Sessions it finds](#sessions-it-finds) |
| `XDG_CACHE_HOME` | Keep the index in `$XDG_CACHE_HOME/superfind/`. | [Commands](#commands) |
| `NO_COLOR` | Turn color off. | [Use](#use) |

## Uninstall

Remove the binary the way you installed it:

```sh
brew uninstall --cask adammiribyan/tap/superfind   # Homebrew
rm "$(go env GOPATH)/bin/superfind"                # go install, or $GOBIN/superfind if you set GOBIN
rm ~/.local/bin/superfind                          # install.sh
```

Delete the line containing `superfind init` from `~/.zshrc`, `~/.bashrc`, or `~/.bash_profile`, and the `keybind = super+shift+o=text:\x18c` line from your Ghostty config if you added it. Then delete the index and your settings:

```sh
rm -rf ~/.cache/superfind ~/.config/superfind      # and $XDG_CACHE_HOME/superfind if you set XDG_CACHE_HOME
```

superfind only reads your sessions and never changes them, so uninstalling leaves every Claude Code, Codex, and Grok session as it was.

## Contributing

Bug reports and pull requests are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) explains how to build and test superfind, and [CHANGELOG.md](CHANGELOG.md) lists what changed in each version. Please report security problems privately, as [SECURITY.md](SECURITY.md) describes.

## License

MIT. superfind is an independent project, not made or endorsed by Anthropic, OpenAI, or xAI.
