#!/bin/sh
set -eu

repo=$(cd "$(dirname "$0")" && pwd)
bin="$HOME/.local/bin"

command -v go >/dev/null || { echo "go missing: install Go 1.26 or newer from https://go.dev/dl" >&2; exit 1; }
(cd "$repo" && go build -o bin/superfind .)
mkdir -p "$bin"
trap 'rm -f "$bin/.superfind.$$"' EXIT
cp "$repo/bin/superfind" "$bin/.superfind.$$"
mv -f "$bin/.superfind.$$" "$bin/superfind"
echo "installed $bin/superfind ($("$bin/superfind" --version))"

case "$(basename "${SHELL:-}")" in
  zsh) shell=zsh rc="$HOME/.zshrc" ;;
  bash)
    shell=bash rc="$HOME/.bashrc"
    [ "$(uname)" = Darwin ] && rc="$HOME/.bash_profile"
    ;;
  *) shell="" rc="" ;;
esac
if [ -n "$shell" ]; then
  setup="eval \"\$(\"\$HOME/.local/bin/superfind\" init $shell)\""
  if ! grep -qF "$setup" "$rc" 2>/dev/null; then
    printf '\n%s\n' "$setup" >> "$rc"
    echo "added superfind to $rc"
  fi
else
  echo "superfind supports zsh and bash; add  eval \"\$(superfind init zsh)\"  or  eval \"\$(superfind init bash)\"  to your shell's startup file"
fi

ghostty_mac="$HOME/Library/Application Support/com.mitchellh.ghostty/config"
ghostty_xdg="${XDG_CONFIG_HOME:-$HOME/.config}/ghostty/config"
if [ -f "$ghostty_xdg" ] || [ -f "$ghostty_mac" ] || [ -d /Applications/Ghostty.app ] || command -v ghostty >/dev/null; then
  if [ -f "$ghostty_xdg" ] || [ ! -f "$ghostty_mac" ]; then ghostty="$ghostty_xdg"; else ghostty="$ghostty_mac"; fi
  keybind='keybind = super+shift+o=text:\x18c'
  if ! grep -qF "$keybind" "$ghostty" 2>/dev/null; then
    mkdir -p "$(dirname "$ghostty")"
    printf '\n%s\n' "$keybind" >> "$ghostty"
    echo "added Cmd-Shift-O to $ghostty (reload Ghostty config with Cmd-Shift-,)"
  fi
fi

echo "done: open a new shell, then press Ctrl-X c or run superfind"
