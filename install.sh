#!/bin/sh
set -eu

repo=$(cd "$(dirname "$0")" && pwd)
bin="$HOME/.local/bin"

command -v python3 >/dev/null || { echo "python3 missing: run xcode-select --install" >&2; exit 1; }
command -v fzf >/dev/null || { echo "fzf missing: run brew install fzf" >&2; exit 1; }
fzf --filter=x --ghost=x --footer=x --gap-line= --input-label-pos=3 --highlight-line </dev/null >/dev/null 2>&1 && : || [ $? -ne 2 ] ||
  { echo "fzf $(fzf --version | cut -d' ' -f1) is too old for the picker's layout: run brew upgrade fzf" >&2; exit 1; }

mkdir -p "$bin"
ln -sf "$repo/claude-sessions" "$bin/claude-sessions"
echo "linked $bin/claude-sessions"

source_line="source \"$repo/claude-sessions.zsh\""
if ! grep -qF "$source_line" "$HOME/.zshrc" 2>/dev/null; then
  printf '\n%s\n' "$source_line" >> "$HOME/.zshrc"
  echo "added widget to ~/.zshrc"
fi
case ":$PATH:" in *":$bin:"*) ;; *) echo "note: add $bin to PATH in ~/.zshrc" ;; esac

ghostty_mac="$HOME/Library/Application Support/com.mitchellh.ghostty/config"
ghostty_xdg="${XDG_CONFIG_HOME:-$HOME/.config}/ghostty/config"
if [ -f "$ghostty_xdg" ] || [ ! -f "$ghostty_mac" ]; then ghostty="$ghostty_xdg"; else ghostty="$ghostty_mac"; fi
keybind='keybind = super+shift+o=text:\x18c'
if ! grep -qF "$keybind" "$ghostty" 2>/dev/null; then
  mkdir -p "$(dirname "$ghostty")"
  printf '\n%s\n' "$keybind" >> "$ghostty"
  echo "added Cmd-Shift-O to $ghostty (reload Ghostty config with Cmd-Shift-,)"
fi

echo "done: open a new shell, then press Cmd-Shift-O in Ghostty or Ctrl-X c anywhere"
