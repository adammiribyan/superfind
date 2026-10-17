<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/superfind-lockup-white.svg">
    <img alt="superfind" src="assets/superfind-lockup-ink.svg" width="300">
  </picture>
</p>

<h3 align="center">Ctrl-R for your coding agents.</h3>

<p align="center">
  Find any Claude Code, Codex, or Grok session by what you remember.<br>
  Press Enter to resume it with the same agent, in the right folder.
</p>

![Find and resume sessions across Claude Code, Codex, and Grok](demo/superfind.gif)

## Install

On macOS or Linux, with zsh, bash, or fish:

```sh
curl -fsSL https://superfind.dev/install | sh
```

Open a new terminal tab and run `superfind`, or `sfd` for short. To resume a session, you need that agent's CLI installed.

Update with `superfind upgrade`. For Homebrew or Go installs, it prints the update command to run.

<details>
<summary>Install with Homebrew, Go, or from source</summary>

```sh
brew install --cask adammiribyan/tap/superfind
# or, with Go 1.26+
go install github.com/adammiribyan/superfind@latest
```

Then add this to `~/.zshrc`:

```sh
eval "$(superfind init zsh)"
```

For bash, add `eval "$(superfind init bash)"` to `~/.bashrc`, or `~/.bash_profile` on macOS. For fish, add `superfind init fish | source` to `~/.config/fish/config.fish`. Open a new terminal tab after setup.

To build from source, with Go 1.26+:

```sh
git clone https://github.com/adammiribyan/superfind.git
cd superfind
./install.sh
```

</details>

## Find a session

Run `sfd`, or press **Ctrl-X** then **c**. Type to search titles, projects, branches, and conversations.

| Key | Action |
| --- | --- |
| ↑ / ↓ | Select a session |
| Enter | Resume the selected session |
| Ctrl-O | Go to the session's folder instead |
| Ctrl-R | Search by description |
| Tab | Switch between the last exchange and recent conversation |
| Esc | Go back or close |

Can't remember the words? Inside the picker, type `where we fixed duplicate charges` and press **Ctrl-R**. Ctrl-R is a key in the picker, not the shortcut that opens it, so your shell's own Ctrl-R keeps working. Description search needs Claude Code installed and logged in, or a [TypeSafe](https://docs.typesafe.ai) API key (TypeSafe is a hosted API for small AI models).

Narrow your search with filters:

```sh
superfind search agent:codex after:yesterday
superfind search in:here file:main.go
```

Use `is:open` to find open sessions. With a TypeSafe key, `is:waiting` and `is:unfinished` find sessions that need attention. Run `superfind help search` for all filters.

A small bar on each row fills as the session uses its context window. It turns amber when less than 30% is left and red under 15%, so you can compact or start fresh before you resume. The preview shows the exact percentage.

If the session is already open, Enter switches to its tmux pane or Ghostty tab. When it can't, it asks before starting a second copy.

To pick up where you left off in the current folder, run `superfind last`.

<details>
<summary>Find sessions waiting on you</summary>

Get a key from TypeSafe and set:

```sh
export TYPESAFE_API_KEY=your-key
```

Open the picker to label recent sessions, then search for `is:waiting`.

</details>

<details>
<summary>Open with a keyboard shortcut</summary>

In Ghostty, **Cmd-Shift-O** opens the picker after installation. To add it manually, put this in your Ghostty config:

```text
keybind = super+shift+o=text:\x18c
```

On Omarchy, **Super-Shift-Ctrl-F** opens the picker in a new terminal. If the shortcut is already taken, the installer prints setup instructions.

</details>

## Which session wrote this?

```sh
superfind blame src/billing.ts:142
superfind blame HEAD
```

Find the sessions behind a line, file, or commit, best match first. It reads what your agents already recorded, so it works on old commits with no hooks or setup. Run `superfind command ID` to get a session's resume command.

## Share a session

```sh
superfind share ID
superfind receive checkout-double-charge.share.md
```

`share` writes a session's briefing to a Markdown file you can send to a colleague. You read it before it's written. It holds excerpts with common secrets masked, the repository, branch, and commit. It leaves out the raw transcript, tool output, and your folder paths. Masking can't find everything, so read what you share.

Your colleague runs `receive` in their clone of the repository, or in any folder if the session wasn't in a repository. It checks the file, shows the briefing, and starts their agent with it when they say yes.

## Scripts and agents

```sh
superfind search --json retry webhook | jq -r .title
superfind show ID --tail 6
superfind handoff ID
```

Use `show` to read a conversation and `handoff` to get a briefing for another agent. `search --json` returns one JSON object per line.

Install [the agent skill](skills/README.md) to let your agent find past sessions when you ask. Run `superfind help <command>` for command help, or see the [full reference](https://superfind.dev/llms-full.txt).

## Privacy

Keyword search and conversation reading stay on your machine. superfind never changes your session files.

- Description search sends your query and session excerpts to TypeSafe if you set a key, or to Anthropic through Claude Code otherwise. TypeSafe session labels also send recent session endings.
- Opening the picker checks for updates and sends an anonymous check-in at most once a day. It never includes searches, paths, or session text. Run `superfind telemetry` to inspect it or `superfind telemetry off` to disable it.

See [Security](SECURITY.md#what-leaves-your-machine) for details.

---

[Contributing](CONTRIBUTING.md) · [Changelog](CHANGELOG.md) · [Security](SECURITY.md)

Built by [Adam Miribyan](https://github.com/adammiribyan). [Apache 2.0 licensed](LICENSE).

Independent of Anthropic, OpenAI, and xAI.
