# Security

## Reporting a vulnerability

Report vulnerabilities through [a private security report](https://github.com/adammiribyan/superfind/security/advisories/new). If the form is unavailable, email adam@adam.am.

Include your version from `superfind --version`, your OS, and steps or a file that reproduces the problem.

## Supported versions

Fixes go into the latest release only.

## Verify a release

For releases 0.11.1 and later, download `checksums.txt`, `checksums.txt.minisig`, and the release files you want to verify into one folder. With [minisign](https://jedisct1.github.io/minisign/) installed, run:

```sh
minisign -Vm checksums.txt -P RWSm5i2Pqh5T3LbiX5vIMSTtLNTkW+Y3Bpvhb+fFEABqAX4YxUHq64sK
shasum -a 256 -c --ignore-missing checksums.txt
```

Check that minisign's trusted comment names the version you downloaded, such as `superfind v0.12.0 checksums.txt`. Both commands must pass. On Linux, you can use `sha256sum -c --ignore-missing checksums.txt` for the second command.

## What leaves your machine

Keyword search, `show`, `handoff`, `share`, `receive`, `command`, `last`, and `blame` run locally. Description search sends your query and session excerpts to TypeSafe if you set a key, or to Anthropic through Claude Code otherwise. With a TypeSafe key, session labeling also sends recent session endings to TypeSafe.

Opening the picker checks for updates and sends an anonymous check-in to superfind.dev at most once a day. It includes a random install id, version, OS, CPU type, install method, upgrade results, and three counts: `share_written`, `share_received`, and `share_started`. A count has no title, repository, agent, or share id, so it never links two installs. It never includes searches, paths, project names, or session text. superfind.dev forwards the check-in to PostHog without your IP address. Scripts and `--json` never check in, but the counts they record wait for the next check-in.

Run `superfind telemetry` to inspect the next check-in. Run `superfind telemetry off` to disable check-ins and automatic update checks. You can also set `SUPERFIND_TELEMETRY=off` or `DO_NOT_TRACK=1`. Telemetry is always off in CI. Manual checks with `superfind upgrade --check` still work.

## Share files

`superfind share` writes a Markdown file that you send yourself. At a terminal, you read it before it's written. It holds the session's title, the agent, the repository remote without credentials, the branch, the commit, pull request links, how the session ended, and excerpts of up to 12,000 bytes by default. Paths in your home folder start with `~`, and common secrets are masked. It leaves out the raw transcript, tool output, file contents, uncommitted changes, your folder path, and the resume command. Masking can't find customer data, unlabelled credentials, or personal information. The sender name comes from `SUPERFIND_NAME`, or else `$USER`, and the receiver can't verify it.

The file carries a SHA-256 of its briefing. It catches a file that was damaged or cut short. It is not a signature, so trust a share as much as the channel it came through.

`superfind receive` writes a record of the share in superfind's cache and the briefing in the repository's `.git` folder. It writes nothing else and never changes agent session files. It starts an agent only after you say yes at a terminal, and the prompt tells the agent the briefing is a record, not instructions.

## What to report

Report transcripts or share files that make superfind run unintended commands, write outside its cache and state folders, leak data, or control your terminal. `share` writes only the file you name, and `receive` writes only its cache and one file in the `.git` folder. Also report check-ins that send data beyond what `superfind telemetry` shows.
