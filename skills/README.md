# Skills

`superfind/SKILL.md` teaches a coding agent to use superfind: when you ask "where did we fix the webhook retry last week?" or "continue my Codex session here", the agent searches your sessions, reads a handoff, and gives you the command that resumes one. It needs superfind installed and `jq`.

Install it in your agent:

- Most agents, through [skills.sh](https://skills.sh): `npx skills add adammiribyan/superfind`. Add `-g` to install it for your user instead of this project.
- Claude Code: `/plugin marketplace add adammiribyan/superfind`, then `/plugin install superfind@superfind`. Update with `claude plugin marketplace update superfind`, then `claude plugin update superfind@superfind`, or turn on auto-update for it under `/plugin` > Marketplaces.
- Codex: `codex plugin marketplace add adammiribyan/superfind`, then `codex plugin add superfind@superfind`. Update with `codex plugin marketplace upgrade`.
- Grok: `grok plugin install adammiribyan/superfind#plugins/superfind --trust`. Update with `grok plugin update superfind`.
- Cursor: import `https://github.com/adammiribyan/superfind` as a team marketplace under Dashboard > Plugins & MCPs.
- By hand: copy the `superfind` folder into your agent's skills folder, such as `~/.agents/skills/`.

The plugins install `plugins/superfind`, which holds a copy of `superfind/SKILL.md`, so Claude Code and Codex install three small files instead of the whole repository. `tests/test_skill.py` checks that the two copies match, and runs each recipe in the skill against fake sessions.
