# Benchmarking

Create the synthetic fixture tree:

```sh
mkdir -p .build
go build -ldflags "-X main.version=$(cat VERSION)" -o ./.build/workspace-launcher ./cmd/workspace-launcher
go build -o ./.build/bench-setup ./cmd/bench-setup
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
```

Run the launcher in built-in headless benchmark mode:

```sh
hyperfine --warmup 3 'WORKSPACE_LAUNCHER_BENCH_MODE=headless ./.build/workspace-launcher /tmp/workspace-launcher-bench >/dev/null'
hyperfine --warmup 3 'WORKSPACE_LAUNCHER_BENCH_MODE=headless WORKSPACE_LAUNCHER_RECENCY=git ./.build/workspace-launcher /tmp/workspace-launcher-bench >/dev/null'
```

The stored pre-optimization reference for March 7, 2026 lives in
[`docs/performance-baseline.md`](performance-baseline.md).
