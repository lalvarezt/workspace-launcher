# workspace-launcher bash integration

if [[ $- =~ i ]]; then
  : "${__workspace_launcher_bin:=workspace-launcher}"

  workspace-launcher-cd() {
    local dir
    dir="$("$__workspace_launcher_bin" "$@")" || return
    [[ -n "$dir" ]] || return 0
    builtin cd -- "$dir"
  }

  workspace-launcher-widget() {
    local dir query
    query="${READLINE_LINE-}"
    dir="$("$__workspace_launcher_bin" --query "$query")" || return
    [[ -n "$dir" ]] || return 0
    builtin cd -- "$dir" || return
    READLINE_LINE=''
    READLINE_POINT=0
  }

  if (( BASH_VERSINFO[0] >= 4 )); then
    bind -m emacs-standard -x '"\C-g": workspace-launcher-widget'
    bind -m vi-command -x '"\C-g": workspace-launcher-widget'
    bind -m vi-insert -x '"\C-g": workspace-launcher-widget'
  fi
fi
