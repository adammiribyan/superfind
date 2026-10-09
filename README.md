# hop

Find any past AI coding session on your Mac and hop back into it. hop opens the session's folder and resumes it, right in the terminal pane you're in. Today it reads Claude Code sessions; other agents come later.

![hop finding a session from a description and resuming it](demo/hop.gif)

## Install

```sh
brew install go
git clone <this repo> ~/src/hop
~/src/hop/install.sh
```

Then open a new terminal tab. hop is a single Go binary with no other dependencies. The installer builds it, links it into `~/.local/bin`, adds `eval "$(hop init zsh)"` to `~/.zshrc`, and adds a Cmd-Shift-O keybind to Ghostty. Running it again changes nothing.

The shell setup is a small zsh function: a program can't change its parent shell's folder, so `hop init zsh` defines the function that runs the `cd` and `claude --resume` for you.

## Use

Press **Cmd-Shift-O** in Ghostty, **Ctrl-X c** in any zsh, or type `hop`. The picker opens full screen and leaves your terminal exactly as it was when you choose or cancel.

| Key | Does |
|---|---|
| type | Search titles, projects, branches, and the conversations themselves. Every word must match. Titles that match come first. |
| Ctrl-R | Deep search: find the session you describe, even when it used other words. "where did we stop charging people twice" finds "Checkout button double-charges on slow networks". |
| Enter | Resume the selected session: `cd` into its folder and run `claude --resume`. The command lands in your shell history. |
| Tab | Switch the preview between where the session left off and the recent conversation. |
| ↑ ↓, Ctrl-P Ctrl-N, PgUp PgDn | Move through the list. |
| Ctrl-/ | Hide or show the preview. Alt-Up and Alt-Down page through it. |
| Ctrl-U | Clear the search. |
| `is:waiting` | Show only sessions waiting on you. `is:unfinished` works the same way. |

Each row shows a session's title, how long ago it was active, and its project. Sessions from the folder you're in come first, then everything else, newest first. One-line "hi" sessions that did no work sink to the bottom.

The preview opens on where the session left off: your last real request and the end of the reply. When you search, its fourth line shows the passage that matched.

A session written in the last two minutes shows **active now**, because it's probably open in another window. Resuming it anyway prints a reminder.

## Waiting on you

With a TypeSafe key, hop marks sessions that need you. **waiting on you** means Claude stopped on a question, an approval, or a choice it can't make alone. **unfinished** means work stopped partway: Claude was interrupted, hit an error, or never answered. Sessions that simply finished, including ones that end with an optional "want me to also…?", stay unmarked. Type `is:waiting` or `is:unfinished` to list only those.

[Jev](https://docs.typesafe.ai) reads how each session ended: the last six turns, clipped, and whether Claude was mid-reply, mid-command, or interrupted. A label appears only when Jev is at least 85% sure. It disappears as soon as the session changes, and after two weeks away, since a session you haven't returned to in that long isn't really waiting. On 21 hand-labeled sessions, 13 of the 15 labels shown were right, and 1 of the 14 sessions that needed a label got none; treat labels as a hint. Labeling runs in the background when the picker opens and only reads recent sessions that changed, so opening never waits on it.

## Deep search

Ctrl-R ranks sessions with [Jev](https://docs.typesafe.ai) when a TypeSafe API key is configured, and with Claude Haiku otherwise.

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

To use Jev, set `TYPESAFE_API_KEY` in your environment, or put this line in `~/.config/hop/env` (or in a git-ignored `.env` at the root of this repo):

```sh
TYPESAFE_API_KEY=your-key
```

Setting `TYPESAFE_API_KEY=` to an empty value turns Jev off even when a file has a key. `TYPESAFE_URL` sends requests to another endpoint, such as a proxy; it defaults to `https://api.typesafe.ai/v1/systemone`. If a deep search fails, the line under the search box says why, and typing goes back to keyword search.

**What leaves your machine.** A deep search sends each session's title, project, last request, and one short passage to the ranker: to TypeSafe with a key, to Anthropic without one. With a key, explanations also send up to 120 short passages from each of the top five sessions to TypeSafe, and labeling sends the ending of every session (its last six turns, clipped) to TypeSafe once, and again whenever it changes. Without a key nothing is labeled. Typing never sends anything; keyword search runs locally.

## Sessions it finds

- `~/.claude/projects`, every `~/.claude-*` folder, `$CLAUDE_CONFIG_DIR`, and any folders listed in colon-separated `$HOP_CLAUDE_DIRS`. When sessions come from more than one folder, each row says which, and resuming sets `CLAUDE_CONFIG_DIR` to match.
- Accounts you switch between with `/login` share one folder, so all their sessions appear. Resuming uses whichever account is logged in now.
- A session whose folder was deleted, which is common with worktrees, is marked **folder removed** and resumes from the nearest parent folder that still exists.

## Commands

```sh
hop                          # open the picker
hop init zsh                 # print the shell setup
hop search <words>           # print matching sessions
hop search --deep <words>    # print the sessions that best fit your description
hop preview <transcript>     # print where a session left off (--recent, --width N, --query Q)
hop command <transcript>     # print the command that resumes a session
hop index                    # build or refresh the index
hop label                    # mark sessions waiting on you (needs a TypeSafe key)
hop --version
```

The index lives in `~/.cache/hop/`, or `$XDG_CACHE_HOME/hop/` when that is set. Each run re-reads only the transcripts that changed, so opening the picker stays instant. The first run reads every transcript in parallel: about a second for 360 sessions here, with a short "Indexing…" line while it works.

hop takes its colors from your terminal. Until your terminal reports them, hop shows the selected row in reverse video, and it switches to the tinted row as soon as they arrive. Set `HOP_PLAIN_COLORS=1` to always use that fallback, for example if the tinted row looks wrong.

## Develop

```sh
go build -o bin/hop .                    # build
python3 -m unittest discover -s tests    # behavior tests against bin/hop, using fake transcripts
vhs demo/hop.tape                        # re-record demo/hop.gif from fake sessions
```
