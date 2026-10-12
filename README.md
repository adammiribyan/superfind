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

Open a new terminal tab and run `superfind`, or `sfd` for short. You need the CLI of the agent you want to resume. To update later, run `superfind upgrade`; with Homebrew or Go it prints the command to use instead.

<details>
<summary>Homebrew, Go, or from source</summary>

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

Open the picker with `sfd`, or press **Ctrl-X** then **c**. Type to search titles, projects, branches, and conversations.

| Key | Action |
| --- | --- |
| ↑ / ↓ | Select a session |
| Enter | Resume the selected session |
| Ctrl-R | Search by description |
| Tab | Switch between the last exchange and recent conversation |
| Esc | Go back or close |

Can't remember the words? Type `where we fixed duplicate charges` and press **Ctrl-R**. Description search needs Claude Code installed and logged in, or a TypeSafe API key.

Add filters to any search: `agent:codex`, `in:here`, `after:yesterday`, `is:unfinished`, `is:open`. `superfind help search` lists them all.

If the session is already open, Enter switches to its tmux pane or Ghostty tab. When it can't, it asks before starting a second copy.

To pick up where you left off in the current folder, run `superfind last`.

<details>
<summary>Sessions waiting on you, with TypeSafe</summary>

Get a key from [TypeSafe](https://docs.typesafe.ai) and set:

```sh
export TYPESAFE_API_KEY=your-key
```

Sessions that stopped waiting on you are labeled, and `is:waiting` lists them.

</details>

<details>
<summary>Open with a keyboard shortcut</summary>

In Ghostty, add this to your config and reload it to open the picker with Cmd-Shift-O:

```text
keybind = super+shift+o=text:\x18c
```

On Omarchy, the install script adds Super-Shift-Ctrl-F to your Hyprland bindings, or says what to add when the key is taken.

</details>

## Which session wrote this?

```sh
superfind blame src/billing.ts:142
superfind blame HEAD
```

This lists the sessions behind a line, file, or commit, best match first. It reads what your agents already recorded, so it works on past commits with nothing installed in advance. Run `superfind command ID` to get the command that resumes one.

## Scripts and agents

```sh
superfind search --json retry webhook | jq -r .title
superfind show ID --tail 6
superfind handoff ID
```

`handoff` prints where a session ended and the turns that matter, to brief another agent. Install [the skill](skills/README.md) so Claude Code, Codex, or Grok can search your sessions when you ask. Agents can read every command in [llms-full.txt](https://superfind.dev/llms-full.txt), and `superfind help <command>` explains one.

## Privacy

Everything runs on your machine except two things. superfind never changes your session files.

- **Description search** sends your query and session excerpts to TypeSafe if you set a key, or to Anthropic through Claude Code otherwise. With a TypeSafe key, session labels also send recent session endings.
- **Update check:** opening the picker checks for a new release and sends an anonymous check-in, never your searches, paths, or session text. `superfind telemetry` shows exactly what it sends, and `superfind telemetry off` stops it.

---

[Contributing](CONTRIBUTING.md) · [Changelog](CHANGELOG.md) · [Security](SECURITY.md)

Built by [Adam Miribyan](https://github.com/adammiribyan). [Apache 2.0 licensed](LICENSE).

Independent of Anthropic, OpenAI, and xAI.
