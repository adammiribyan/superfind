# Contributing to superfind

Thanks for helping. Bug reports, fixes, and ideas are all welcome. For a larger change, please open an issue first so we can agree on the approach before you write it.

## Build

You need Go 1.26 or newer.

```sh
go build -o bin/superfind .
```

`./install.sh` builds the same binary and installs it to `~/.local/bin`, if you want to try your build as your everyday superfind.

## Test

The tests are behavior tests written in Python. They run the real `bin/superfind` against fake transcripts in throwaway home folders, so they never read your own sessions. They need Python 3 (standard library only), and the picker and shell tests drive real terminals through tmux, so they also need tmux, zsh, and bash.

```sh
brew install tmux                                          # macOS; on Debian or Ubuntu: sudo apt-get install tmux zsh
go build -o bin/superfind .
python3 -m unittest discover -s tests                      # all tests, about a minute
python3 -m unittest discover -s tests -p test_picker.py    # one file
```

Without tmux, the picker and shell tests are skipped rather than failed. To test another build, point `SUPERFIND_BIN` at it.

CI runs these checks on macOS and Linux for every pull request. To run them yourself before you push:

```sh
gofmt -l .                                                 # prints nothing when formatting is clean
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@2025.1.1 ./...
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

## Demo

`demo/superfind.gif` is recorded with [vhs](https://github.com/charmbracelet/vhs) from fake sessions that `demo/make-home.py` writes to `/tmp/superfind-demo-home`:

```sh
vhs demo/superfind.tape
```

The recording runs ask for real. With a TypeSafe key in `.env` at the repo root, it uses Jev and also shows labels. Without one, it sends ask to Claude Haiku through your Claude Code login, so Claude Code must be installed and logged in.

## Releases

Maintainers release by setting `version` in `main.go`, adding the version's date to CHANGELOG.md, committing, and pushing a tag such as `v0.9.0`. The release workflow checks that the tag matches `superfind --version`, then GoReleaser builds the macOS and Linux binaries, publishes the GitHub release, and updates the Homebrew cask in `adammiribyan/homebrew-tap`.

## Conduct

Everyone taking part is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
