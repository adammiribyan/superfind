#!/bin/sh
set -eu

releases=https://github.com/adammiribyan/superfind/releases

fail() {
  echo "$*" >&2
  exit 1
}

fetch() {
  curl --proto '=https' --tlsv1.2 -fsSL --retry 3 "$@"
}

build() {
  command -v go >/dev/null || { echo "go missing: install Go 1.26 or newer from https://go.dev/dl" >&2; exit 1; }
  (cd "$1" && go build -o bin/superfind .)
  binary="$1/bin/superfind"
}

download() {
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) os="" ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) arch="" ;;
  esac
  if [ -z "$os" ] || [ -z "$arch" ]; then
    echo "no superfind release for $(uname -s) $(uname -m); with Go installed, run: go install github.com/adammiribyan/superfind@latest" >&2
    exit 1
  fi

  version=${SUPERFIND_VERSION:-$(fetch -I -o /dev/null -w '%{url_effective}' "$releases/latest")}
  version=${version##*/}
  version=${version#v}
  case "$version" in
    [0-9]*) ;;
    *) echo "could not find the latest superfind release at $releases" >&2; exit 1 ;;
  esac

  archive="superfind_${version}_${os}_${arch}.tar.gz"
  echo "downloading superfind $version for $os $arch"
  fetch -o "$tmp/$archive" "$releases/download/v$version/$archive"
  fetch -o "$tmp/checksums.txt" "$releases/download/v$version/checksums.txt"
  want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
  if command -v sha256sum >/dev/null; then
    got=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
  else
    got=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
  fi
  if [ -z "$want" ] || [ "$want" != "$got" ]; then
    echo "$archive does not match its checksum in checksums.txt; nothing was installed" >&2
    exit 1
  fi
  tar -xzf "$tmp/$archive" -C "$tmp" superfind
  binary="$tmp/superfind"
}

main() {
  here=$(dirname "$0")
  if [ -f "$0" ] && grep -qx 'module github.com/adammiribyan/superfind' "$here/go.mod" 2>/dev/null; then
    from=checkout
  else
    from=release
  fi

  bin="$HOME/.local/bin"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp" "$bin/.superfind.$$"' EXIT
  case "$from" in
    checkout) build "$(cd "$here" && pwd)" ;;
    release) download ;;
  esac
  installed=$("$binary" --version) || fail "the new superfind does not run on this machine; nothing was installed"
  other=$(command -v superfind || true)
  mkdir -p "$bin"
  cp "$binary" "$bin/.superfind.$$"
  mv -f "$bin/.superfind.$$" "$bin/superfind"
  echo "installed $bin/superfind ($installed)"
  if [ -n "$other" ] && [ "$other" != "$bin/superfind" ]; then
    echo "note: another superfind is installed at $other"
  fi

  omarchy="${OMARCHY_PATH:-$HOME/.local/share/omarchy}"
  if [ -d "$omarchy" ]; then
    hypr="${XDG_CONFIG_HOME:-$HOME/.config}/hypr"
    launch="omarchy-launch-tui $bin/superfind pick --run"
    if [ -f "$hypr/bindings.conf" ] && [ ! -f "$hypr/bindings.lua" ]; then
      bindings="$hypr/bindings.conf" chord='SUPER SHIFT CTRL, F,' binding="bindd = SUPER SHIFT CTRL, F, Superfind, exec, $launch"
    else
      bindings="$hypr/bindings.lua" chord='"SUPER + SHIFT + CTRL + F"' binding="o.bind(\"SUPER + SHIFT + CTRL + F\", \"Superfind\", \"$launch\")"
    fi
    case "$bin" in *[!A-Za-z0-9/._-]*) unsafe=1 ;; *) unsafe="" ;; esac
    if grep -qF "$binding" "$bindings" 2>/dev/null; then
      :
    elif [ -n "$unsafe" ] || [ ! -f "$bindings" ] || grep -qF "$chord" "$bindings"; then
      echo "to open superfind with Super-Shift-Ctrl-F, add this line to $bindings: $binding"
    else
      printf '\n%s\n' "$binding" >> "$bindings"
      echo "added Super-Shift-Ctrl-F to $bindings (run hyprctl reload to use it now)"
    fi
  fi

  case "$(basename "${SHELL:-}")" in
    zsh) shell=zsh rc="$HOME/.zshrc" ;;
    bash)
      shell=bash rc="$HOME/.bashrc"
      [ "$(uname)" = Darwin ] && rc="$HOME/.bash_profile"
      ;;
    fish) shell=fish rc="${XDG_CONFIG_HOME:-$HOME/.config}/fish/config.fish" ;;
    *) shell="" rc="" ;;
  esac
  if [ -z "$shell" ]; then
    echo "superfind sets up zsh, bash, and fish. To use it from one of them, add a line to that shell's startup file:"
    echo "  eval \"\$(\"\$HOME/.local/bin/superfind\" init zsh)\"     # in ~/.zshrc"
    echo "  eval \"\$(\"\$HOME/.local/bin/superfind\" init bash)\"    # in ~/.bashrc, or ~/.bash_profile on macOS"
    echo "  \"\$HOME/.local/bin/superfind\" init fish | source      # in ~/.config/fish/config.fish"
    echo "done: run $bin/superfind to see what it does"
    return
  fi
  if grep -Eq '^[^#]*superfind"? init' "$rc" 2>/dev/null; then
    echo "$rc already sets up superfind; left it as it is"
  else
    setup="eval \"\$(\"\$HOME/.local/bin/superfind\" init $shell)\""
    [ "$shell" = fish ] && setup="\"\$HOME/.local/bin/superfind\" init fish | source"
    mkdir -p "$(dirname "$rc")"
    printf '\n%s\n' "$setup" >> "$rc"
    echo "added superfind to $rc"
  fi

  ghostty_mac="$HOME/Library/Application Support/com.mitchellh.ghostty/config"
  ghostty_xdg="${XDG_CONFIG_HOME:-$HOME/.config}/ghostty/config"
  if [ -f "$ghostty_xdg" ] || [ -f "$ghostty_mac" ] || [ -d /Applications/Ghostty.app ] || command -v ghostty >/dev/null; then
    if [ -f "$ghostty_xdg" ] || [ ! -f "$ghostty_mac" ]; then ghostty="$ghostty_xdg"; else ghostty="$ghostty_mac"; fi
    keybind='keybind = super+shift+o=text:\x18c'
    if [ -d "$omarchy" ]; then
      echo "skipped the Ghostty keybind: on Omarchy, Hyprland takes Super keys before Ghostty sees them"
    elif ! grep -qF "$keybind" "$ghostty" 2>/dev/null; then
      mkdir -p "$(dirname "$ghostty")"
      printf '\n%s\n' "$keybind" >> "$ghostty"
      echo "added Cmd-Shift-O to $ghostty (reload Ghostty config with Cmd-Shift-,)"
    fi
  fi

  echo "done: open a new shell, then press Ctrl-X c or run superfind"
}

main "$@"
