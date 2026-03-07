# Benchmarking

Create the synthetic fixture tree:

```sh
make build
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

Run the launcher in built-in headless benchmark mode:

```sh
hyperfine --warmup 3 'WORKSPACE_LAUNCHER_BENCH_MODE=headless ./.build/workspace-launcher --print /tmp/workspace-launcher-bench >/dev/null'
hyperfine --warmup 3 'WORKSPACE_LAUNCHER_BENCH_MODE=headless WORKSPACE_LAUNCHER_RECENCY=git ./.build/workspace-launcher --print /tmp/workspace-launcher-bench >/dev/null'
```

The stored pre-optimization reference for March 7, 2026 lives in
[`docs/performance-baseline.md`](performance-baseline.md).
