# Security

## Reporting a vulnerability

Please report security problems privately, not in a public issue. Open a private report from this repository's [Security tab](https://github.com/adammiribyan/superfind/security/advisories/new) ("Report a vulnerability"). If that form isn't available to you, email adam@adam.am.

Include the superfind version (`superfind --version`), your OS, and the steps or the file that shows the problem.

## Supported versions

Fixes go into the latest release only.

## Verify a release

Releases after 0.11.0 include `checksums.txt.minisig`, a [minisign](https://jedisct1.github.io/minisign/) signature of the release's `checksums.txt`, made with the superfind release key. `checksums.txt` lists the SHA-256 of every other file in the release, so the signature covers them all. To check a release by hand, download `checksums.txt`, `checksums.txt.minisig`, and the files you want into one folder, and run:

```sh
minisign -Vm checksums.txt -P RWSm5i2Pqh5T3LbiX5vIMSTtLNTkW+Y3Bpvhb+fFEABqAX4YxUHq64sK
shasum -a 256 -c --ignore-missing checksums.txt
```

minisign prints `Trusted comment: superfind v0.12.0 checksums.txt` for 0.12.0. Check that the comment names the version you downloaded: the comment is signed too, so it can't be changed without breaking the signature. On Linux, `sha256sum -c --ignore-missing checksums.txt` does the same as the `shasum` line. The key is also in [internal/release/keys](internal/release/keys).

## What counts

superfind reads session transcripts written by other programs and prints parts of them to your terminal, builds shell commands that resume sessions, and sends some session text over the network when you ask (see [What leaves your machine](README.md#what-leaves-your-machine)). Problems in those paths are what we most want to hear about: for example, a transcript that makes superfind run a command, write outside its cache folder, send data it shouldn't, or control your terminal.
