# workspace-launcher

[![License](https://img.shields.io/badge/license-MIT-3d3d3d.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-v1.0.3-cb8d43.svg)](VERSION)
[![Last Commit](https://img.shields.io/github/last-commit/lalvarezt/workspace-launcher)](https://github.com/lalvarezt/workspace-launcher/commits/main)
[![Shell](https://img.shields.io/badge/shell-bash-2f7d32.svg)](bin/workspace-launcher)

`workspace-launcher` is an `fzf`-powered workspace picker for the terminal. It
scans the direct children of a root directory, sorts them by recent activity,
shows lightweight metadata, and lets you either select an existing workspace or
create a new one from the current query.

By default it scans `~/git-repos`, but it also works well for generic directory
trees such as `~/.config` or `~/src`.

## Screenshots

### Browse git repositories

![workspace-launcher browsing git repos](docs/images/workspace-launcher-git-repos.png)

### Browse config directories

![workspace-launcher browsing config directories](docs/images/workspace-launcher-config.png)

## Why Use It

- Recent-first workspace switching without typing full paths.
- Inline metadata for age, detected language, and git state.
- `Ctrl-N` creates a new directory from the active query.
- `Ctrl-E` opens the selected directory in `$VISUAL` or `$EDITOR`.
- Works as a path picker or a shell launcher.

## Requirements

- `bash`
- `fzf` in `PATH`, unless you provide a vendored binary at `bin/fzf`
- Standard Unix tools such as `find`, `sort`, `stat`, and `xargs`
- `git` if you want git-state display or git-based recency sorting

## Install

For end users, install from GitHub releases with `eget`:

```sh
eget lalvarezt/workspace-launcher
```

This is the recommended install path when you want a packaged release asset.

## Local Install

For local development or source checkouts, install the launcher and the short alias with:

```sh
make install
```

This installs:

- `workspace-launcher`
- `wl`

By default both commands go to `${XDG_BIN_HOME:-$HOME/.local/bin}`.

You can override the target directory with either `XDG_BIN_HOME` or `BIN_DIR`

To build release archives locally:

```sh
make release-assets
```

To generate a large synthetic workspace tree for performance testing:

```sh
make bench-setup
```

## Usage

By default, `workspace-launcher` prints the selected path. That makes it easy to
use in shell functions, scripts, and commands such as `cd "$(workspace-launcher)"`.
Use `--shell` when you want the launcher to open an interactive shell in the
selected directory instead of returning the path.

Print the selected path:

```sh
workspace-launcher
```

Start a shell in the selected directory:

```sh
workspace-launcher --shell
```

Seed the query and sort by latest git commit:

```sh
WORKSPACE_LAUNCHER_RECENCY=git workspace-launcher --query fzf ~/src
```

Use it as a generic picker for config directories:

```sh
workspace-launcher --no-language --no-git ~/.config
```

## Key Bindings

- `Enter`: select the current match
- `Ctrl-N`: create a new directory from the current query
- `Ctrl-E`: open the selected directory in `$VISUAL` or `$EDITOR`
- `Esc`: quit

## CLI Options

```text
Usage: workspace-launcher [--print|--shell] [--query TEXT] [--[no-]language] [--[no-]git] [-v|--version] [ROOT]
```

- `--print`: print the selected or created path
- `--shell`: start an interactive shell in the selected path
- `--query TEXT`: start with an initial query
- `--language` / `--no-language`: show or hide the language column
- `--git` / `--no-git`: show or hide the git-state column
- `ROOT`: override the default root directory for this run

## Configuration

Configuration is done with environment variables:

| Variable                             | Description                                                                |
|--------------------------------------|----------------------------------------------------------------------------|
| `WORKSPACE_LAUNCHER_ROOT`            | Default root directory. Defaults to `~/git-repos`.                         |
| `WORKSPACE_LAUNCHER_JOBS`            | Parallel metadata workers. Clamped between `1` and the detected CPU count. |
| `WORKSPACE_LAUNCHER_GIT_DIRTY=1`     | Marks dirty repositories as `git*`.                                        |
| `WORKSPACE_LAUNCHER_RECENCY=git`     | Sorts by the latest commit timestamp instead of directory mtime.           |
| `WORKSPACE_LAUNCHER_SHOW_LANGUAGE=0` | Hides the language column by default.                                      |
| `WORKSPACE_LAUNCHER_SHOW_GIT=0`      | Hides the git-state column by default.                                     |
| `FZF_BIN`                            | Overrides the `fzf` binary path.                                           |

## Notes

- The launcher only scans the direct children of the selected root.
- Language detection is heuristic-based and checks for common project files.
- Git metadata is only shown for directories that contain `.git`.

## Benchmarking

Benchmark fixture setup and `hyperfine` examples live in
[`docs/benchmarking.md`](docs/benchmarking.md).

## Acknowledgements

This launcher was influenced by prior work on terminal project and workspace pickers, especially:

- [`tobi/try`](https://github.com/tobi/try)
- [`tassiovirginio/try-rs`](https://github.com/tassiovirginio/try-rs)
