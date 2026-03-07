# workspace-launcher bash integration

if [[ $- =~ i ]]; then
  __workspace_launcher_widget_key='\C-x\C-_W1\a'
  __workspace_launcher_accept_key='\C-x\C-_W0\a'

  workspace-launcher-cd() {
    local dir
    dir="$("$__workspace_launcher_bin" "$@")" || return
    [[ -n "$dir" ]] || return 0
    builtin cd -- "$dir"
  }

  __workspace_launcher_bind_accept_line() {
    local keymap
    for keymap in emacs-standard vi-command vi-insert; do
      bind -m "$keymap" "\"$__workspace_launcher_accept_key\": accept-line"
    done
  }

  __workspace_launcher_clear_accept_line() {
    local keymap
    for keymap in emacs-standard vi-command vi-insert; do
      bind -m "$keymap" "\"$__workspace_launcher_accept_key\": \"\""
    done
  }

  workspace-launcher-widget() {
    local dir query
    __workspace_launcher_clear_accept_line
    query="${READLINE_LINE-}"
    dir="$("$__workspace_launcher_bin" --query "$query")" || return
    [[ -n "$dir" ]] || return 0
    builtin cd -- "$dir" || return
    READLINE_LINE=''
    READLINE_POINT=0
    __workspace_launcher_bind_accept_line
  }

  __workspace_launcher_clear_accept_line
  bind -m emacs-standard "\"\C-g\": \"$__workspace_launcher_widget_key$__workspace_launcher_accept_key\""
  bind -m vi-command "\"\C-g\": \"$__workspace_launcher_widget_key$__workspace_launcher_accept_key\""
  bind -m vi-insert "\"\C-g\": \"$__workspace_launcher_widget_key$__workspace_launcher_accept_key\""
  bind -m emacs-standard -x "\"$__workspace_launcher_widget_key\": workspace-launcher-widget"
  bind -m vi-command -x "\"$__workspace_launcher_widget_key\": workspace-launcher-widget"
  bind -m vi-insert -x "\"$__workspace_launcher_widget_key\": workspace-launcher-widget"
fi
