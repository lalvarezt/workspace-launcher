# workspace-launcher zsh integration

if [[ -o interactive ]]; then
  : "${__workspace_launcher_bin:=workspace-launcher}"

  workspace-launcher-cd() {
    emulate -L zsh -o no_aliases
    local dir
    dir="$("$__workspace_launcher_bin" "$@")" || return
    [[ -n "$dir" ]] || return 0
    builtin cd -- "$dir"
  }

  workspace-launcher-widget() {
    emulate -L zsh -o no_aliases
    local dir
    dir="$("$__workspace_launcher_bin" --query "$LBUFFER")" || {
      zle reset-prompt
      return 1
    }
    if [[ -z "$dir" ]]; then
      zle reset-prompt
      return 0
    fi

    builtin cd -- "$dir" || {
      zle reset-prompt
      return 1
    }
    LBUFFER=''
    RBUFFER=''
    zle reset-prompt
  }

  zle -N workspace-launcher-widget
  bindkey -M emacs '^G' workspace-launcher-widget
  bindkey -M vicmd '^G' workspace-launcher-widget
  bindkey -M viins '^G' workspace-launcher-widget
fi
