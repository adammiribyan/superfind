# Skills

`superfind/SKILL.md` teaches a coding agent to use superfind: when you ask "where did we fix the webhook retry last week?" or "continue my Codex session here", the agent searches your sessions, reads a handoff, and gives you the command that resumes one. It needs superfind installed and `jq`.

Install it in your agent:

- Claude Code: `/plugin marketplace add adammiribyan/superfind`, then `/plugin install superfind@superfind`. Update with `claude plugin marketplace update superfind`, then `claude plugin update superfind@superfind`, or turn on auto-update for it under `/plugin` > Marketplaces.
- Codex: `codex plugin marketplace add adammiribyan/superfind`, then `codex plugin add superfind@superfind`. Update with `codex plugin marketplace upgrade`.
- Grok: `grok plugin install adammiribyan/superfind --trust`. Update with `grok plugin update superfind`.
- Any other agent that reads [Agent Skills](https://agentskills.io): copy the `superfind` folder into its skills folder, such as `~/.agents/skills/`.

Each recipe in the skill runs in `tests/test_skill.py` against fake sessions.
