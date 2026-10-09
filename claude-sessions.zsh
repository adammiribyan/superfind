_claude_sessions_handoff() {
  print -r -- $'\e[38;2;215;119;87m✻\e[0m Resuming \e[1m'"$1"$'\e[0m'
}

ccs() {
  local out
  out=$(claude-sessions pick) || return
  _claude_sessions_handoff "${out%%$'\n'*}"
  print -s -- "${out#*$'\n'}"
  eval "${out#*$'\n'}"
}

_claude_sessions_widget() {
  local out
  if out=$(claude-sessions pick); then
    zle -I
    _claude_sessions_handoff "${out%%$'\n'*}"
    BUFFER=${out#*$'\n'}
    zle accept-line
  else
    zle reset-prompt
  fi
}
zle -N _claude_sessions_widget
bindkey '^Xc' _claude_sessions_widget
