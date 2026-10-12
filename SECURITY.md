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

Keyword search, `show`, `handoff`, `command`, `last`, and `blame` run locally. Description search sends your query and session excerpts to TypeSafe if you set a key, or to Anthropic through Claude Code otherwise. `SUPERFIND_ASK=sonnet`, or another Claude model, sends them to Anthropic even with a key. With a TypeSafe key, session labeling also sends recent session endings to TypeSafe.

Opening the picker checks for updates and sends an anonymous check-in to superfind.dev at most once a day. It includes a random install id, version, OS, CPU type, install method, and upgrade results. It never includes searches, paths, project names, or session text. superfind.dev forwards the check-in to PostHog without your IP address. Scripts and `--json` never check in.

Run `superfind telemetry` to inspect the next check-in. Run `superfind telemetry off` to disable check-ins and automatic update checks. You can also set `SUPERFIND_TELEMETRY=off` or `DO_NOT_TRACK=1`. Telemetry is always off in CI. Manual checks with `superfind upgrade --check` still work.

## What to report

Report transcripts that make superfind run unintended commands, write outside its cache and state folders, leak data, or control your terminal. Also report check-ins that send data beyond what `superfind telemetry` shows.
