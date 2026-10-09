#!/bin/sh
set -eu

repo=$(cd "$(dirname "$0")" && pwd)
bin="$HOME/.local/bin"

command -v go >/dev/null || { echo "go missing: run brew install go" >&2; exit 1; }
(cd "$repo" && go build -o bin/hop .)
mkdir -p "$bin"
ln -sf "$repo/bin/hop" "$bin/hop"
echo "built and linked $bin/hop ($("$bin/hop" --version))"

setup='eval "$("$HOME/.local/bin/hop" init zsh)"'
if ! grep -qF "$setup" "$HOME/.zshrc" 2>/dev/null; then
  printf '\n%s\n' "$setup" >> "$HOME/.zshrc"
  echo "added hop to ~/.zshrc"
fi

ghostty_mac="$HOME/Library/Application Support/com.mitchellh.ghostty/config"
ghostty_xdg="${XDG_CONFIG_HOME:-$HOME/.config}/ghostty/config"
if [ -f "$ghostty_xdg" ] || [ ! -f "$ghostty_mac" ]; then ghostty="$ghostty_xdg"; else ghostty="$ghostty_mac"; fi
keybind='keybind = super+shift+o=text:\x18c'
if ! grep -qF "$keybind" "$ghostty" 2>/dev/null; then
  mkdir -p "$(dirname "$ghostty")"
  printf '\n%s\n' "$keybind" >> "$ghostty"
  echo "added Cmd-Shift-O to $ghostty (reload Ghostty config with Cmd-Shift-,)"
fi

echo "done: open a new shell, then press Cmd-Shift-O in Ghostty, Ctrl-X c anywhere, or run hop"
