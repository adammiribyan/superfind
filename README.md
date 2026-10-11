<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/superfind-lockup-white.svg">
    <img alt="superfind" src="assets/superfind-lockup-ink.svg" width="300">
  </picture>
</p>

<p align="center">The session picker for your coding agents.</p>

Search by keyword or describe what you remember. Preview where you left off. Press Enter to resume with the original agent in the session's project folder, right in your current terminal.

![Find and resume sessions across Claude Code, Codex, and Grok](demo/superfind.gif)

## Install

Works on macOS and Linux with zsh, bash, or fish.

```sh
curl -fsSL https://superfind.dev/install | sh
```

Open a new terminal tab, then run:

```sh
superfind
```

You need the corresponding agent CLI installed to resume a session.

<details>
<summary>Install with Homebrew, Go, or from source</summary>

```sh
brew install --cask adammiribyan/tap/superfind
# or, with Go 1.21+
go install github.com/adammiribyan/superfind@latest
```

For either, add this to `~/.zshrc`:

```sh
eval "$(superfind init zsh)"
```

For bash, add `eval "$(superfind init bash)"` to `~/.bashrc`, or `~/.bash_profile` on macOS. For fish, add `superfind init fish | source` to `~/.config/fish/config.fish`. Open a new terminal tab after setup.

To build from source, with Go 1.21 or newer:

```sh
git clone https://github.com/adammiribyan/superfind.git
cd superfind
./install.sh
```

</details>

## Use

Open the picker with `sfd`, `superfind`, or **Ctrl-X**, then **c**. Type to search titles, projects, branches, and conversations.

| Key | Action |
| --- | --- |
| ↑ / ↓ | Select a session |
| Enter | Resume the selected session |
| Ctrl-R | Search by description |
| Tab | Switch between the last exchange and recent conversation |
| Esc | Go back or close |

Try a description like `where we fixed duplicate charges`, then press **Ctrl-R**. Search by description requires Claude Code installed and logged in, or a TypeSafe API key.

Filter by agent with `agent:claude`, `agent:codex`, or `agent:grok`. Use `is:unfinished` for sessions that stopped partway. Use `is:open` for sessions an agent has open right now; their rows say **open**.

<details>
<summary>Faster search and session labels with TypeSafe</summary>

Get a key from [TypeSafe](https://docs.typesafe.ai) and set:

```sh
export TYPESAFE_API_KEY=your-key
```

This also labels sessions **waiting on you**. Use `is:waiting` to filter them.

</details>

<details>
<summary>Open with Cmd-Shift-O in Ghostty</summary>

Add this to your Ghostty config and reload it:

```text
keybind = super+shift+o=text:\x18c
```

</details>

<details>
<summary>Open with Super-Shift-Ctrl-F on Omarchy</summary>

The install script adds the key to your Hyprland bindings. Run `hyprctl reload` to use it right away.

</details>

## Scripts and agents

Give your agent context from an earlier conversation, or build a journal across all your agents. These examples use `jq`.

```sh
# Find a migration discussion and read its last six turns.
id=$(superfind search --json migration |
  jq -sr 'map(select(.kind == "interactive")) | .[0].id // empty')
superfind show "$id" --json --tail 6

# Export a session journal: date, agent, project, and title.
superfind search --json |
  jq -r 'select(.kind == "interactive") |
    [.updated[:10], .agent, .project, .title] | @tsv'
```

To brief another agent on a session, `superfind handoff ID` prints how it ended, how to resume it, and quoted excerpts.

See `superfind --help` for all commands and `superfind help <command>` for details. Agents can read the whole reference in one file, [llms-full.txt](https://superfind.dev/llms-full.txt).

## Privacy

Keyword search runs locally. superfind never changes your original session files and has no telemetry, analytics, or update checks.

Search by description sends your query and session excerpts to TypeSafe when configured, or Anthropic through Claude Code otherwise. With a TypeSafe key, background labeling also sends recent session endings to TypeSafe.

---

[Contributing](CONTRIBUTING.md) · [Changelog](CHANGELOG.md) · [Security](SECURITY.md)

Built by [Adam Miribyan](https://github.com/adammiribyan). [MIT licensed](LICENSE).

Independent of Anthropic, OpenAI, and xAI.
