# Contributing to superfind

Thanks for helping. Bug reports, fixes, and ideas are all welcome. For a larger change, please open an issue first so we can agree on the approach before you write it.

## Build

You need Go 1.21 or newer; if yours is older than 1.26, Go downloads 1.26 on the first build.

```sh
go build -o bin/superfind .
```

`./install.sh` builds the same binary and installs it to `~/.local/bin`, if you want to try your build as your everyday superfind.

## Test

Unit and fuzz tests are written in Go, next to the code: `go test -race ./...` runs them. The behavior tests are written in Python. They run the real `bin/superfind` against fake transcripts in throwaway home folders, so they never read your own sessions. They need Python 3 (standard library only), and the picker and shell tests drive real terminals through tmux, so they also need tmux, zsh, and bash.

```sh
brew install tmux                                          # macOS; on Debian or Ubuntu: sudo apt-get install tmux zsh
go build -o bin/superfind .
python3 -m unittest discover -s tests                      # all tests, about a minute and a half
python3 -m unittest discover -s tests -p test_picker.py    # one file
```

Without tmux, the picker and shell tests are skipped rather than failed. To test another build, point `SUPERFIND_BIN` at it.

CI runs these checks on Linux for every pull request, and on macOS and Linux before every release. A push to `main` runs them again only when it didn't come from a merged pull request. To run the macOS checks on a branch, run `gh workflow run test.yml --ref BRANCH -f macos=true`. To run them yourself before you push:

```sh
gofmt -l .                                                 # prints nothing when formatting is clean
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@2025.1.1 ./...
go test -race ./...
go build -race -o bin/superfind-race . && SUPERFIND_BIN=$PWD/bin/superfind-race python3 -m unittest discover -s tests
```

## Code layout

`main.go` holds the command line. Everything else is under `internal/`:

| Package | Does |
|---|---|
| `agent` | Where each agent keeps its sessions, how to read them, and how to resume them |
| `session` | The index, the search database, and labels |
| `ask` | Ranking sessions for ask, and picking the passages that explain a result |
| `typesafe` | The TypeSafe client |
| `render` | Rows, previews, and text wrapping |
| `tui` | The full-screen picker |

## Pull requests

- Keep each pull request to one change, and add a test that fails without it.
- If users will notice the change, update the README and add a line under the top version in [CHANGELOG.md](CHANGELOG.md).
- Write commit messages that say what changed for the user, such as `fix: keep the selection when the list refreshes`.
- If you change the picker's look, re-record the demo (below) so the README stays true.
- `llms-full.txt` isn't committed. `python3 scripts/llms.py` writes it from `llms.txt`, every command's help, and real JSON output, and a behavior test fails if it can't. A new JSON field needs a line in the script's `FIELDS`.

## Demo

`demo/superfind.gif` is recorded with [vhs](https://github.com/charmbracelet/vhs) from fake sessions that `demo/make-home.py` writes to `/tmp/sfd-demo`:

```sh
vhs demo/superfind.tape
```

The recording runs ask and labeling for real. With a TypeSafe key in `.env` at the repo root, ask uses Jev and the `is:waiting` step shows the session waiting on you. Without one, ask goes to Claude Haiku through your Claude Code login, so Claude Code must be installed and logged in, and the `is:waiting` step shows an empty list; record the README's GIF with a key.

## Releasing

For maintainers:

1. Set `version` in `main.go`, add the date to that version's entry in CHANGELOG.md, and merge the change into `main`.
2. Tag the commit and push only that tag, never `--tags`:

   ```sh
   git tag v0.9.0
   git push origin v0.9.0
   ```

Pushing the tag starts the release workflow. It first runs every check on macOS and Linux, and stops if any fails. Then it checks that every release secret is set and that the tag matches `superfind --version`. Then it builds the macOS and Linux binaries, signs and notarizes the macOS ones, and uploads everything to a draft release, along with `install.sh` and an `llms-full.txt` that `scripts/llms.py` writes from that build. Next it checks the draft's macOS binaries on an Apple Silicon Mac and on an Intel Mac. Only when both pass does it publish the release and update the Homebrew cask in `adammiribyan/homebrew-tap`.

The workflow needs these repository secrets: `MACOS_SIGN_P12`, `MACOS_SIGN_PASSWORD`, `MACOS_NOTARY_KEY`, `MACOS_NOTARY_KEY_ID`, `MACOS_NOTARY_ISSUER_ID`, and `HOMEBREW_TAP_TOKEN`.
