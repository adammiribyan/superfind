_hop_handoff() {
  print -r -- $'\e[38;2;215;119;87m✻\e[0m Resuming \e[1m'"$1"$'\e[0m'
}

hop() {
  if (( $# )); then
    command hop "$@"
    return
  fi
  local out
  out=$(command hop pick) || return
  _hop_handoff "${out%%$'\n'*}"
  print -s -- "${out#*$'\n'}"
  eval "${out#*$'\n'}"
}

_hop_widget() {
  local out
  if out=$(command hop pick); then
    zle -I
    _hop_handoff "${out%%$'\n'*}"
    BUFFER=${out#*$'\n'}
    zle accept-line
  else
    zle reset-prompt
  fi
}
zle -N _hop_widget
bindkey '^Xc' _hop_widget
