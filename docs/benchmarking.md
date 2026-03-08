# Benchmarking

Run the default benchmark flow end-to-end:

```sh
just bench
```

That builds the binaries, prepares the synthetic fixture under
`/tmp/workspace-launcher-bench`, and runs the `mtime` and `git` recency
benchmarks with `hyperfine`.

Create the synthetic fixture tree:

```sh
just build
./.build/bench-setup
```

Or install the fixture generator directly with Go:

```sh
go install github.com/lalvarezt/workspace-launcher/cmd/bench-setup@latest
bench-setup
```

Re-running the command only creates missing directories. Rebuild from scratch:

```sh
./.build/bench-setup --force
```

Override the size or location:

```sh
./.build/bench-setup --count 2500 --root /tmp/wl-bench
just bench /tmp/wl-bench 2500
```

Run the launcher in built-in headless benchmark mode:

```sh
hyperfine --warmup 3 'WORKSPACE_LAUNCHER_BENCH_MODE=headless ./.build/workspace-launcher /tmp/workspace-launcher-bench >/dev/null'
hyperfine --warmup 3 'WORKSPACE_LAUNCHER_BENCH_MODE=headless WORKSPACE_LAUNCHER_RECENCY=git ./.build/workspace-launcher /tmp/workspace-launcher-bench >/dev/null'
```

The stored pre-optimization reference for March 7, 2026 lives in
[`docs/performance-baseline.md`](performance-baseline.md).
