<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/superfind-lockup-white.svg">
    <img alt="superfind" src="assets/superfind-lockup-ink.svg" width="300">
  </picture>
</p>

<p align="center">The session picker for your coding agents.</p>
<p align="center">Claude Code · Codex · Grok</p>

Find a past session by keyword or by what you remember of it, see where you left off, and press Enter to pick it up again in the right folder.

![Find and resume sessions across Claude Code, Codex, and Grok](demo/superfind.gif)

## Install

```sh
curl -fsSL https://superfind.dev/install | sh
```

Open a new terminal tab and run `sfd`. Works on macOS and Linux with zsh, bash, or fish.

<details>
<summary>Homebrew, Go, or from source</summary>

```sh
brew install --cask adammiribyan/tap/superfind
go install github.com/adammiribyan/superfind@latest
```

Then add the shell setup to your startup file and open a new tab:

```sh
eval "$(superfind init zsh)"      # ~/.zshrc
eval "$(superfind init bash)"     # ~/.bashrc, or ~/.bash_profile on macOS
superfind init fish | source      # ~/.config/fish/config.fish
```

From source: clone the repo and run `./install.sh`.

</details>

## Use

Run `sfd`, or press **Ctrl-X** then **c**, and start typing.

| Key | Action |
| --- | --- |
| ↑ / ↓ | Select a session |
| Enter | Resume it |
| Ctrl-R | Search by description, like `where we fixed duplicate charges` |
| Tab | Switch the preview between the last exchange and the conversation |
| Esc | Go back or close |

Narrow the list with `agent:codex`, `in:here`, `is:open`, `file:main.go`, or `after:3d`. `sfd last` resumes the newest session in the current folder.

Resuming needs the agent that made the session. Search by description uses Claude Code, or a [TypeSafe](https://docs.typesafe.ai) key if you set `TYPESAFE_API_KEY`.

<details>
<summary>Open with a key in Ghostty or Omarchy</summary>

The install script sets these up. In Ghostty, Cmd-Shift-O:

```text
keybind = super+shift+o=text:\x18c
```

On Omarchy, Super-Shift-Ctrl-F opens superfind in a new terminal.

</details>

## Scripts and agents

`search`, `show`, `last`, and `handoff` print JSON with `--json`. For example, read the end of the latest session about a migration:

```sh
superfind show "$(superfind search --json migration | jq -r '.id' | head -1)" --tail 6
```

`superfind handoff ID` briefs another agent on a session. `superfind help` lists every command, and agents can read [llms-full.txt](https://superfind.dev/llms-full.txt) or install [the skill](skills/README.md).

## Update

```sh
superfind upgrade
```

If you installed with Homebrew or Go, it tells you the command to run instead.

## Privacy

Keyword search runs on your machine, and superfind never changes your session files. Search by description sends your query and parts of your sessions to Claude Code's model, or to TypeSafe if you set a key. With a TypeSafe key, superfind also sends the end of recent sessions to TypeSafe to label the ones waiting on you.

Once a day, when you open the picker, superfind checks for a new version and sends a random install ID, its version, your OS and CPU type, how it was installed, and whether an upgrade you ran worked. Nothing from your sessions is sent. `superfind telemetry off` turns it off, and `superfind help telemetry` shows exactly what is sent.

---

[Contributing](CONTRIBUTING.md) · [Changelog](CHANGELOG.md) · [Security](SECURITY.md)

Built by [Adam Miribyan](https://github.com/adammiribyan). [Apache 2.0 licensed](LICENSE).

Independent of Anthropic, OpenAI, and xAI.
