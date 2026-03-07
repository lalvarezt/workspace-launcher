# workspace-launcher

[![License](https://img.shields.io/badge/license-MIT-3d3d3d.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-v1.0.4-cb8d43.svg)](VERSION)
[![Last Commit](https://img.shields.io/github/last-commit/lalvarezt/workspace-launcher)](https://github.com/lalvarezt/workspace-launcher/commits/main)
[![Language](https://img.shields.io/badge/language-go-00ADD8.svg)](go.mod)

`workspace-launcher` is a native Go `fzf`-powered workspace picker for the terminal. It
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

- Go 1.26 or newer to build or install from source
- `fzf` in `PATH`, unless you provide a vendored binary at `bin/fzf`
- `git` if you want git-state display or git-based recency sorting

## Install

Install the launcher with Go:

```sh
go install github.com/lalvarezt/workspace-launcher/cmd/workspace-launcher@latest
```

This installs the `workspace-launcher` binary into `GOBIN` or `$(go env GOPATH)/bin`.

Install the benchmark fixture generator with:

```sh
go install github.com/lalvarezt/workspace-launcher/cmd/bench-setup@latest
```

## Local Build

Build the launcher binary from a checkout:

```sh
mkdir -p .build
go build -ldflags "-X main.version=$(cat VERSION)" -o ./.build/workspace-launcher ./cmd/workspace-launcher
```

Build the benchmark fixture generator:

```sh
go build -o ./.build/bench-setup ./cmd/bench-setup
```

Run the launcher directly after building:

```sh
./.build/workspace-launcher
```

Install from the local checkout into a custom bin dir:

```sh
GOBIN="${XDG_BIN_HOME:-$HOME/.local/bin}" go install ./cmd/workspace-launcher
```

Release archives are built and published by the GitHub release workflow when a
`v*` tag is pushed.

To generate a large synthetic workspace tree for performance testing:

```sh
go run ./cmd/bench-setup
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
