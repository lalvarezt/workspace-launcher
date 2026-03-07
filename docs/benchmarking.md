# Benchmarking

Create the synthetic fixture tree:

```sh
make bench-setup
```

Re-running the command only creates missing directories. Rebuild from scratch:

```sh
make bench-setup BENCH_ARGS=--force
```

Override the size or location:

```sh
make bench-setup BENCH_COUNT=2500 BENCH_ROOT=/tmp/wl-bench
```

Run the launcher through the headless `fzf` stub:

```sh
make build
hyperfine --warmup 3 'FZF_BIN=./scripts/fzf-bench-stub ./.build/workspace-launcher --print /tmp/workspace-launcher-bench >/dev/null'
hyperfine --warmup 3 'FZF_BIN=./scripts/fzf-bench-stub WORKSPACE_LAUNCHER_RECENCY=git ./.build/workspace-launcher --print /tmp/workspace-launcher-bench >/dev/null'
```
