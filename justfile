set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

alias default := help

help:
    @just --list

test:
    go test ./...

lint:
    #!/usr/bin/env bash
    set -euo pipefail
    golangci_bin="$(go env GOPATH)/bin/golangci-lint"
    staticcheck_bin="$(go env GOPATH)/bin/staticcheck"
    if [[ ! -x "$golangci_bin" ]]; then
      go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
    fi
    if [[ ! -x "$staticcheck_bin" ]]; then
      go install honnef.co/go/tools/cmd/staticcheck@latest
    fi
    "$golangci_bin" run ./...
    "$staticcheck_bin" ./...

build:
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf .build
    mkdir -p .build
    go build -ldflags "-X main.version=$(cat VERSION)" -o ./.build/workspace-launcher ./cmd/workspace-launcher
    go build -o ./.build/bench-setup ./cmd/bench-setup

bench root="/tmp/workspace-launcher-bench" count="1500" warmup="3":
    #!/usr/bin/env bash
    set -euo pipefail
    just build
    if ! command -v hyperfine >/dev/null 2>&1; then
      printf 'hyperfine is required for just bench\n' >&2
      exit 1
    fi
    ./.build/bench-setup --root "{{root}}" --count "{{count}}"
    hyperfine --warmup "{{warmup}}" \
      "WORKSPACE_LAUNCHER_BENCH_MODE=headless ./.build/workspace-launcher {{root}} >/dev/null" \
      "WORKSPACE_LAUNCHER_BENCH_MODE=headless WORKSPACE_LAUNCHER_RECENCY=git ./.build/workspace-launcher {{root}} >/dev/null"

bench-go filter=".":
    go test ./cmd/workspace-launcher -run '^$' -bench "{{filter}}" -benchmem

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

bump-version target="patch":
    #!/usr/bin/env bash
    set -euo pipefail
    target="{{target}}"
    if [[ "$target" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      just set-version "$target"
      exit 0
    fi

    current_version="$(tr -d '\n' < VERSION)"
    if [[ ! "$current_version" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
      printf 'VERSION must look like v1.2.3, found %s\n' "$current_version" >&2
      exit 1
    fi

    major="${BASH_REMATCH[1]}"
    minor="${BASH_REMATCH[2]}"
    patch="${BASH_REMATCH[3]}"

    case "$target" in
      major)
        major=$((major + 1))
        minor=0
        patch=0
        ;;
      minor)
        minor=$((minor + 1))
        patch=0
        ;;
      patch)
        patch=$((patch + 1))
        ;;
      *)
        printf 'bump target must be major, minor, patch, or an explicit version like v1.2.3\n' >&2
        exit 1
        ;;
    esac

    just set-version "v${major}.${minor}.${patch}"

set-version version:
    #!/usr/bin/env bash
    set -euo pipefail
    version="{{version}}"
    if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      printf 'version must look like v1.2.3\n' >&2
      exit 1
    fi

    tmp_dir="$(mktemp -d)"
    cleanup() {
      rm -rf "$tmp_dir"
    }
    trap cleanup EXIT

    printf '%s\n' "$version" > "$tmp_dir/VERSION"
    if ! VERSION="$version" perl -0pe '$count = s/version-v[^)]+-cb8d43\.svg/version-$ENV{VERSION}-cb8d43.svg/; END { exit($count == 1 ? 0 : 1) }' README.md > "$tmp_dir/README.md"; then
      printf 'failed to update version badge in README.md\n' >&2
      exit 1
    fi

    mv "$tmp_dir/VERSION" VERSION
    mv "$tmp_dir/README.md" README.md
