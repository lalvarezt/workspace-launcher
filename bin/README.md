# Launcher Scripts

`workspace-launcher` is a small directory picker built on top of `fzf`. It
scans the direct children of `~/git-repos` by default, sorts them by recent
activity, and lets you either jump into an existing directory or create a new
one from the current query with `Ctrl-N`. `Ctrl-E` opens the selected
directory in `$VISUAL` or `$EDITOR`.

The checked-in launcher is `bin/workspace-launcher`. To create the short alias,
run `make -C bin install`.

This exposes two command names:

- `bin/workspace-launcher`
- `bin/wl`

Examples:

```sh
# Print the selected path
bin/workspace-launcher

# Start a shell in the selected directory
bin/workspace-launcher --shell

# Point it at a different root and seed the query
WORKSPACE_LAUNCHER_RECENCY=git bin/workspace-launcher --query fzf ~/src

# Hide language and git columns for generic directory trees
bin/wl --no-language --no-git ~/.config
```

Configuration is done with environment variables:

- `WORKSPACE_LAUNCHER_ROOT` sets the default root directory.
- `WORKSPACE_LAUNCHER_JOBS` controls parallel metadata collection.
- `WORKSPACE_LAUNCHER_GIT_DIRTY=1` marks dirty repos as `git*`.
- `WORKSPACE_LAUNCHER_RECENCY=git` sorts by the latest commit timestamp instead of
  directory mtime.
- `WORKSPACE_LAUNCHER_SHOW_LANGUAGE=0` hides the language column by default.
- `WORKSPACE_LAUNCHER_SHOW_GIT=0` hides the git-state column by default.
