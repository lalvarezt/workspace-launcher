if [[ -o interactive ]]; then
  bindkey -M emacs '^G' workspace-launcher-widget
  bindkey -M vicmd '^G' workspace-launcher-widget
  bindkey -M viins '^G' workspace-launcher-widget
fi
