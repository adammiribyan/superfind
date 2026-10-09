# hop

Find any past AI coding session on your Mac and hop back into it. hop opens the session's folder and resumes it, right in the terminal pane you're in. Today it reads Claude Code sessions; other agents come later.

![hop finding a session by meaning and resuming it](demo/hop.gif)

## Install

```sh
brew install fzf
git clone <this repo> ~/src/hop
~/src/hop/install.sh
```

Then open a new terminal tab. The installer links `hop` into `~/.local/bin`, loads the zsh widget from `~/.zshrc`, and adds a Cmd-Shift-O keybind to Ghostty. It needs python3, which the Xcode Command Line Tools provide, and a recent fzf; the installer checks for the features it needs. Running it again changes nothing.

## Use

Press **Cmd-Shift-O** in Ghostty, **Ctrl-X c** in any zsh, or type `hop`.

| Key | Does |
|---|---|
| type | Search titles, projects, branches, and the conversations themselves. Every word must match. Titles that match come first. |
| ⌥ Enter | Ask: rank sessions by meaning, so "where did we stop charging people twice" finds "Checkout button double-charges on slow networks". |
| Enter | Resume the selected session: `cd` into its folder and run `claude --resume`. The command lands in your shell history. |
| Tab | Switch the preview between where the session left off and the recent conversation. |
| Ctrl-/ | Hide or show the preview. Alt-Up and Alt-Down page through it. |
| Ctrl-U | Clear the search. |
| `is:waiting` | Show only sessions waiting on you. `is:unfinished` works the same way. |

Each row shows a session's title, how long ago it was active, and its project. Sessions from the folder you're in come first, then everything else, newest first. One-line "hi" sessions that did no work sink to the bottom.

The preview opens on where the session left off: your last real request and the end of the reply. When you search, its fourth line shows the passage that matched.

A session written in the last two minutes shows **active now**, because it's probably open in another window. Resuming it anyway prints a reminder.

## Waiting on you

With a TypeSafe key, hop marks sessions that need you. **waiting on you** means Claude stopped on a question, an approval, or a choice it can't make alone. **unfinished** means work stopped partway: Claude was interrupted, hit an error, or never answered. Sessions that simply finished, including ones that end with an optional "want me to also…?", stay unmarked. Type `is:waiting` or `is:unfinished` to list only those.

[Jev](https://docs.typesafe.ai) reads how each session ended: the last six turns, clipped, and whether Claude was mid-reply, mid-command, or interrupted. A label appears only when Jev is at least 85% sure. It disappears as soon as the session changes, and after two weeks away, since a session you haven't returned to in that long isn't really waiting. On 21 hand-labeled sessions, 13 of the 15 labels shown were right, and 1 of the 14 sessions that needed a label got none; treat labels as a hint. Labeling runs in the background when the picker opens and only reads recent sessions that changed, so opening never waits on it.

## Ask

⌥ Enter ranks sessions with [Jev](https://docs.typesafe.ai) when a TypeSafe API key is configured, and with Claude Haiku otherwise.

Measured on 21 test searches, each phrased differently from the session it should find, over two or three runs:

| | Jev | Claude Haiku |
|---|---|---|
| Right session ranked first | 16–17 | 18–19 |
| Right session in the top 3 | 19–20 | 19–20 |
| Time to results, median | 1.3 s | 3.3–3.8 s |
| Time to results, slowest | 2.3 s | 8.6 s |
| Cost per question | $0.0036 | $0.0073 |

Claude puts the right session first slightly more often. Jev is about three times faster and half the price.

With Jev, the preview also shows the passage that best answers your question for the top five results. It appears about a second after the results.

To use Jev, put the key in your environment or in a `.env` file next to the `hop` script (it is git-ignored):

```sh
TYPESAFE_API_KEY=your-key
```

Setting `TYPESAFE_API_KEY=` to an empty value turns Jev off even when `.env` has a key. If asking fails, the line under the search box says why, and typing goes back to keyword search.

**What leaves your machine.** Asking sends each session's title, project, last request, and one short passage to the ranker: to TypeSafe with a key, to Anthropic without one. With a key, explanations also send up to 120 short passages from each of the top five sessions to TypeSafe, and labeling sends the ending of every session (its last six turns, clipped) to TypeSafe once, and again whenever it changes. Without a key nothing is labeled. Typing never sends anything; keyword search runs locally.

## Sessions it finds

- `~/.claude/projects`, every `~/.claude-*` folder, `$CLAUDE_CONFIG_DIR`, and any folders listed in colon-separated `$HOP_CLAUDE_DIRS`. When sessions come from more than one folder, each row says which, and resuming sets `CLAUDE_CONFIG_DIR` to match.
- Accounts you switch between with `/login` share one folder, so all their sessions appear. Resuming uses whichever account is logged in now.
- A session whose folder was deleted, which is common with worktrees, is marked **folder removed** and resumes from the nearest parent folder that still exists.

## Commands

```sh
hop                  # open the picker
hop search <words>   # print matching sessions
hop ask <question>   # print sessions ranked by meaning
hop index            # build or refresh the index
hop label            # mark sessions waiting on you (needs a TypeSafe key)
hop --version
```

The index lives in `~/.cache/hop/`. Each run re-reads only the transcripts that changed, so opening the picker stays instant; the first run shows a short "Indexing…" line while it reads everything.

hop takes its colors from your terminal. If your terminal doesn't report its colors, hop falls back to reverse video for the selected row. Set `HOP_PLAIN_COLORS=1` to always use that fallback, for example if the tinted row looks wrong.

## Develop

```sh
python3 -m unittest discover -s tests   # behavior tests, using fake transcripts
vhs demo/hop.tape                       # re-record demo/hop.gif from fake sessions
```
