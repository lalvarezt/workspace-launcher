set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

alias default := help

help:
    @just --list

test:
    go test ./...

build:
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf .build
    mkdir -p .build
    go build -ldflags "-X main.version=$(cat VERSION)" -o ./.build/workspace-launcher ./cmd/workspace-launcher
    go build -o ./.build/bench-setup ./cmd/bench-setup

run *args:
    just build
    ./.build/workspace-launcher {{args}}

install bin_dir="":
    #!/usr/bin/env bash
    set -euo pipefail
    just build
    target="{{bin_dir}}"
    if [[ -z "$target" ]]; then
      target="${XDG_BIN_HOME:-$HOME/.local/bin}"
    fi
    mkdir -p "$target"
    install -m 755 ./.build/workspace-launcher "$target/workspace-launcher"
    install -m 755 ./.build/bench-setup "$target/bench-setup"
    ln -sfn workspace-launcher "$target/wl"
    printf 'Installed workspace-launcher, bench-setup, and wl to %s\n' "$target"

bump-version version:
    #!/usr/bin/env bash
    set -euo pipefail
    case "{{version}}" in
      v[0-9]*.[0-9]*.[0-9]*) ;;
      *)
        printf 'version must look like v1.2.3\n' >&2
        exit 1
        ;;
    esac
    printf '%s\n' "{{version}}" > VERSION
    perl -0pi -e 's/version-v[0-9]+\.[0-9]+\.[0-9]+-cb8d43\.svg/version-{{version}}-cb8d43.svg/' README.md
