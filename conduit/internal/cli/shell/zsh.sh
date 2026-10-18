# conduit shell integration for zsh.
#   eval "$(conduit init zsh)"             # in ~/.zshrc
# Binds @@KEY@@ to the history picker (conduit runs first, then this shell's history)
# and defines cx: run a command through conduit's policy and record.
__conduit_pick() {
  local sel
  builtin fc -W 2>/dev/null
  sel="$(conduit pick --query "$BUFFER" --history "${HISTFILE:-$HOME/.zsh_history}")" || { zle redisplay; return 0 }
  BUFFER="$sel"
  CURSOR=${#BUFFER}
  zle redisplay
}
zle -N __conduit_pick
bindkey '@@KEY@@' __conduit_pick

# cx git status        -> conduit run -- git status
# cx 'ls | wc -l'      -> a single argument is a command string
cx() { conduit run -- "$@"; }

_conduit() {
  local -a subs
  subs=(run check parse ls show recall pick init paths help)
  (( CURRENT == 2 )) && _describe 'conduit command' subs
}
(( $+functions[compdef] )) && compdef _conduit conduit
