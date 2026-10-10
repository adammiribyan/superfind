# Security

## Reporting a vulnerability

Please report security problems privately, not in a public issue. Open a private report from this repository's [Security tab](https://github.com/adammiribyan/superfind/security/advisories/new) ("Report a vulnerability"). If that form isn't available to you, email adam@adam.am.

Include the superfind version (`superfind --version`), your OS, and the steps or the file that shows the problem.

## Supported versions

Fixes go into the latest release only.

## What counts

superfind reads session transcripts written by other programs and prints parts of them to your terminal, builds shell commands that resume sessions, and sends some session text over the network when you ask (see [What leaves your machine](README.md#what-leaves-your-machine)). Problems in those paths are what we most want to hear about: for example, a transcript that makes superfind run a command, write outside its cache folder, send data it shouldn't, or control your terminal.
