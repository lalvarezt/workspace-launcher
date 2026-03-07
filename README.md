# workspace-launcher

[![License](https://img.shields.io/badge/license-MIT-3d3d3d.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-v1.0.0-cb8d43.svg)](VERSION)
[![Last Commit](https://img.shields.io/github/last-commit/lalvarezt/workspace-launcher)](https://github.com/lalvarezt/workspace-launcher/commits/main)
[![Shell](https://img.shields.io/badge/shell-bash-2f7d32.svg)](bin/workspace-launcher)

`workspace-launcher` is a small directory picker built on top of `fzf`. It
scans the direct children of `~/git-repos` by default, sorts them by recent
activity, and lets you either jump into an existing directory or create a new
one from the current query with `Ctrl-N`. `Ctrl-E` opens the selected
directory in `$VISUAL` or `$EDITOR`.

Current baseline release: `v1.0.0`.

## Install

Run:

```sh
make install
```

This installs:

- `workspace-launcher`
- `wl`

By default both commands go to `${XDG_BIN_HOME:-$HOME/.local/bin}`. Override the
target with `XDG_BIN_HOME=/some/bin make install` or `BIN_DIR=/some/bin make install`.

## Usage

Examples:

```sh
# Print the selected path
workspace-launcher

# Start a shell in the selected directory
workspace-launcher --shell

# Print the current version
workspace-launcher --version

# Point it at a different root and seed the query
WORKSPACE_LAUNCHER_RECENCY=git workspace-launcher --query fzf ~/src

# Hide language and git columns for generic directory trees
wl --no-language --no-git ~/.config
```

Configuration is done with environment variables:

- `WORKSPACE_LAUNCHER_ROOT` sets the default root directory.
- `WORKSPACE_LAUNCHER_JOBS` controls parallel metadata collection.
- `WORKSPACE_LAUNCHER_GIT_DIRTY=1` marks dirty repos as `git*`.
- `WORKSPACE_LAUNCHER_RECENCY=git` sorts by the latest commit timestamp instead of
  directory mtime.
- `WORKSPACE_LAUNCHER_SHOW_LANGUAGE=0` hides the language column by default.
- `WORKSPACE_LAUNCHER_SHOW_GIT=0` hides the git-state column by default.
