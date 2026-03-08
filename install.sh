#!/usr/bin/env bash
set -euo pipefail

repo_url="https://github.com/lalvarezt/workspace-launcher"
bin_dir="${XDG_BIN_HOME:-$HOME/.local/bin}"
ref="latest"
stage_dir=""

cleanup() {
  if [[ -n "$stage_dir" ]]; then
    rm -rf "$stage_dir"
  fi
}

trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: install.sh [REF] [--bin-dir DIR]

Install workspace-launcher and the wl alias.

Options:
  --bin-dir DIR   Install into DIR instead of ${XDG_BIN_HOME:-$HOME/.local/bin}
  -h, --help      Show this help text

Arguments:
  REF             Release tag to install, defaults to latest
EOF
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bin-dir)
      shift
      if [[ $# -eq 0 ]]; then
        printf 'missing value for --bin-dir\n' >&2
        exit 1
      fi
      bin_dir="$1"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      printf 'unknown option: %s\n' "$1" >&2
      exit 1
      ;;
    *)
      if [[ "$ref" != "latest" ]]; then
        printf 'unexpected argument: %s\n' "$1" >&2
        exit 1
      fi
      ref="$1"
      ;;
  esac
  shift
done

mkdir -p "$bin_dir"

install_alias() {
  ln -sfn workspace-launcher "$bin_dir/wl"
}

resolve_tag() {
  if [[ "$ref" != "latest" ]]; then
    printf '%s\n' "$ref"
    return 0
  fi

  local resolved
  resolved="$(curl -fsSIL -o /dev/null -w '%{url_effective}' "$repo_url/releases/latest")"
  basename "$resolved"
}

detect_target() {
  local os
  local arch

  case "$(uname -s)" in
    Linux)
      os="linux"
      ;;
    Darwin)
      os="darwin"
      ;;
    *)
      printf 'unsupported operating system for release archives: %s\n' "$(uname -s)" >&2
      return 1
      ;;
  esac

  case "$(uname -m)" in
    x86_64|amd64)
      arch="amd64"
      ;;
    arm64|aarch64)
      arch="arm64"
      ;;
    *)
      printf 'unsupported architecture for release archives: %s\n' "$(uname -m)" >&2
      return 1
      ;;
  esac

  printf '%s_%s\n' "$os" "$arch"
}

install_from_release() {
  local tag="$1"
  local target
  local version_no_v
  local archive
  local url

  target="$(detect_target)" || return 1
  version_no_v="${tag#v}"
  archive="workspace-launcher_${version_no_v}_${target}.tar.gz"
  url="$repo_url/releases/download/$tag/$archive"

  if ! curl -fsSL "$url" -o "$stage_dir/$archive"; then
    return 1
  fi

  if ! tar -xzf "$stage_dir/$archive" -C "$stage_dir" workspace-launcher; then
    return 1
  fi

  if ! install -m 755 "$stage_dir/workspace-launcher" "$bin_dir/workspace-launcher"; then
    return 1
  fi

  install_alias

  printf 'Installed workspace-launcher and wl to %s from release %s\n' "$bin_dir" "$tag"
}

install_from_go() {
  local tag="$1"
  local module_ref="latest"

  if ! command_exists go; then
    return 1
  fi

  if [[ "$ref" != "latest" ]]; then
    module_ref="$tag"
  fi

  GOBIN="$bin_dir" go install "github.com/lalvarezt/workspace-launcher/cmd/workspace-launcher@$module_ref"
  install_alias

  printf 'Installed workspace-launcher and wl to %s via go install (%s)\n' "$bin_dir" "$module_ref"
}

stage_dir="$(mktemp -d)"
tag="$(resolve_tag)"

if ! install_from_release "$tag"; then
  printf 'Falling back to go install for %s\n' "$tag" >&2
  if ! install_from_go "$tag"; then
    printf 'failed to install workspace-launcher from release archives and go is not available\n' >&2
    exit 1
  fi
fi
