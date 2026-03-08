if [[ $- =~ i ]]; then
  __workspace_launcher_clear_accept_line
  bind -m emacs-standard "\"\C-g\": \"$__workspace_launcher_widget_key$__workspace_launcher_accept_key\""
  bind -m vi-command "\"\C-g\": \"$__workspace_launcher_widget_key$__workspace_launcher_accept_key\""
  bind -m vi-insert "\"\C-g\": \"$__workspace_launcher_widget_key$__workspace_launcher_accept_key\""
  bind -m emacs-standard -x "\"$__workspace_launcher_widget_key\": workspace-launcher-widget"
  bind -m vi-command -x "\"$__workspace_launcher_widget_key\": workspace-launcher-widget"
  bind -m vi-insert -x "\"$__workspace_launcher_widget_key\": workspace-launcher-widget"
fi
