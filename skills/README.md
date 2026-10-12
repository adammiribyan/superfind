# Let your agent find past sessions

Install the superfind skill, then ask your agent "where did we fix the webhook retry last week?" or "continue my Codex session here". It finds the session, reads a briefing, and gives you the resume command.

You need `superfind` and `jq` installed.

## Install

For most agents, use [skills.sh](https://skills.sh):

```sh
npx skills add adammiribyan/superfind
```

Add `-g` to install for your user instead of the current project.

To install as a plugin, choose your agent:

| Agent | Install |
| --- | --- |
| Claude Code | Run `/plugin marketplace add adammiribyan/superfind`, then `/plugin install superfind@superfind` |
| Codex | Run `codex plugin marketplace add adammiribyan/superfind`, then `codex plugin add superfind@superfind` |
| Grok | Run `grok plugin install adammiribyan/superfind#plugins/superfind --trust` |
| Cursor | Import `https://github.com/adammiribyan/superfind` under Dashboard > Plugins & MCPs |

For a manual install, copy the [superfind folder](superfind) into your agent's skills folder, such as `~/.agents/skills/`.

## Update a plugin

- Claude Code: run `claude plugin marketplace update superfind`, then `claude plugin update superfind@superfind`. You can enable auto-update under `/plugin` > Marketplaces.
- Codex: run `codex plugin marketplace upgrade`.
- Grok: run `grok plugin update superfind`.
