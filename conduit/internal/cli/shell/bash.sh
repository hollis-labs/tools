# conduit shell integration for bash.
#   eval "$(conduit init bash)"            # in ~/.bashrc
# Binds @@KEY@@ to the history picker (conduit runs first, then this shell's history)
# and defines cx: run a command through conduit's policy and record.
__conduit_pick() {
  local sel
  history -a 2>/dev/null
  sel="$(conduit pick --query "$READLINE_LINE" --history "${HISTFILE:-$HOME/.bash_history}")" || return 0
  READLINE_LINE="$sel"
  READLINE_POINT=${#READLINE_LINE}
}
bind -x '"@@KEY@@": __conduit_pick'

# cx git status        -> conduit run -- git status
# cx 'ls | wc -l'      -> a single argument is a command string
cx() { conduit run -- "$@"; }

_conduit_complete() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=($(compgen -W "run check parse ls show recall pick init paths help" -- "$cur"))
  fi
}
complete -F _conduit_complete conduit
