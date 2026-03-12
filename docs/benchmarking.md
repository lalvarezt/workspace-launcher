# Benchmarking

## Native Go Benchmarks

Use Go's native benchmark runner for targeted hot paths:

```sh
just bench-go
```

That runs:

```sh
go test ./cmd/workspace-launcher -run '^$' -bench . -benchmem
```

The native suite currently covers these core paths in `cmd/workspace-launcher`:

- `buildCandidates` with `mtime` and `git` recency
- `inspectGitMeta` for regular repos, packed refs, and linked worktrees
- `pickRepoHeadless` for empty-query, early-match, and late-match scans

Run a subset directly:

```sh
go test ./cmd/workspace-launcher -run '^$' -bench 'BuildCandidates|InspectGitMeta' -benchmem
```

Use the native suite when you want per-function allocation data and tighter feedback
while working on a specific hot path.

## End-to-End Headless Benchmarks

Run the full app-level benchmark flow:

```sh
just bench
```

That builds the binaries, prepares the synthetic fixture under
`/tmp/workspace-launcher-bench`, and runs the `mtime` and `git` recency
benchmarks with `hyperfine`.

Create the synthetic fixture tree manually:

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

Run the launcher in built-in headless benchmark mode directly:

```sh
hyperfine --warmup 3 'WORKSPACE_LAUNCHER_BENCH_MODE=headless ./.build/workspace-launcher /tmp/workspace-launcher-bench >/dev/null'
hyperfine --warmup 3 'WORKSPACE_LAUNCHER_BENCH_MODE=headless WORKSPACE_LAUNCHER_RECENCY=git ./.build/workspace-launcher /tmp/workspace-launcher-bench >/dev/null'
```

Use the headless flow when you want end-to-end timings for the actual binary across a
large synthetic workspace tree.
