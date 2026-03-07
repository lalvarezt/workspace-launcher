package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	benchMarker  = ".workspace-launcher-bench-root"
	benchState   = ".workspace-launcher-bench-state"
	defaultRoot  = "/tmp/workspace-launcher-bench"
	defaultCount = 1500
)

type config struct {
	root  string
	count int
	force bool
}

type state struct {
	fixtureCount int
	startEpoch   int64
}

func main() {
	if err := run(); err != nil {
		var exitErr exitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}
		fmt.Fprintf(os.Stderr, "%s: %s\n", filepath.Base(os.Args[0]), err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseArgs(os.Args[1:])
	if err != nil {
		return err
	}

	created, skipped, err := buildFixture(cfg)
	if err != nil {
		return err
	}

	fmt.Printf("Bench fixture ready under %s (created=%d skipped=%d total=%d)\n", cfg.root, created, skipped, cfg.count)
	return nil
}

func parseArgs(args []string) (config, error) {
	cfg := config{
		root:  envOrDefault("WORKSPACE_LAUNCHER_BENCH_ROOT", defaultRoot),
		count: envOrDefaultInt("WORKSPACE_LAUNCHER_BENCH_COUNT", defaultCount),
	}

	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--root":
			i++
			if i >= len(args) {
				return config{}, errors.New("missing value for --root")
			}
			cfg.root = args[i]
		case "--count":
			i++
			if i >= len(args) {
				return config{}, errors.New("missing value for --count")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return config{}, errors.New("count must be a positive integer")
			}
			cfg.count = n
		case "--force":
			cfg.force = true
		case "-h", "--help":
			printUsage()
			return config{}, exitCodeError{code: 0}
		default:
			return config{}, fmt.Errorf("unknown option: %s", arg)
		}
	}

	if cfg.count < 100 {
		return config{}, errors.New("count must be at least 100")
	}
	return cfg, nil
}

func printUsage() {
	fmt.Fprintf(os.Stdout, `Usage: %s [--root DIR] [--count N] [--force]

Create a synthetic workspace tree for performance testing.

Options:
  --root DIR   Target root directory (default: %s)
  --count N    Number of direct child directories to generate (default: %d)
  --force      Recreate the benchmark root from scratch
  -h, --help   Show this help text

Environment:
  WORKSPACE_LAUNCHER_BENCH_ROOT   Default target root
  WORKSPACE_LAUNCHER_BENCH_COUNT  Default directory count
`, filepath.Base(os.Args[0]), defaultRoot, defaultCount)
}

func buildFixture(cfg config) (created int, skipped int, err error) {
	st, err := prepareRoot(cfg)
	if err != nil {
		return 0, 0, err
	}

	if !cfg.force {
		complete, err := fixtureComplete(cfg)
		if err != nil {
			return 0, 0, err
		}
		if complete {
			fmt.Printf("Fixture already exists under %s with %d directories; use --force to recreate it\n", cfg.root, cfg.count)
			return 0, 0, nil
		}
	}

	for i := 0; i < cfg.count; i++ {
		archetype, gitMode := selectArchetype(i)
		dir := expectedDir(cfg.root, i, archetype, gitMode)
		epoch := st.startEpoch + int64(i*73)

		if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
			skipped++
			continue
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return created, skipped, statErr
		}

		if err := os.MkdirAll(dir, 0o755); err != nil {
			return created, skipped, err
		}
		if err := createFiles(dir, archetype, i); err != nil {
			return created, skipped, err
		}

		switch gitMode {
		case "clean":
			if err := initGitRepo(dir, i, epoch, false); err != nil {
				return created, skipped, err
			}
		case "dirty":
			if err := initGitRepo(dir, i, epoch, true); err != nil {
				return created, skipped, err
			}
		case "none":
		default:
			return created, skipped, fmt.Errorf("unknown git mode: %s", gitMode)
		}

		if err := setMTime(dir, epoch); err != nil {
			return created, skipped, err
		}
		created++
	}

	if err := writeBenchReadme(cfg.root, cfg.count); err != nil {
		return created, skipped, err
	}
	if err := persistState(cfg.root, state{fixtureCount: cfg.count, startEpoch: st.startEpoch}); err != nil {
		return created, skipped, err
	}
	return created, skipped, nil
}

func prepareRoot(cfg config) (state, error) {
	info, err := os.Stat(cfg.root)
	if err == nil && !info.IsDir() {
		return state{}, fmt.Errorf("root exists and is not a directory: %s", cfg.root)
	}
	if err == nil {
		if _, markerErr := os.Stat(filepath.Join(cfg.root, benchMarker)); markerErr != nil && !cfg.force {
			return state{}, fmt.Errorf("refusing to use non-benchmark directory: %s", cfg.root)
		}
		if cfg.force {
			if err := os.RemoveAll(cfg.root); err != nil {
				return state{}, err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return state{}, err
	}

	if err := os.MkdirAll(cfg.root, 0o755); err != nil {
		return state{}, err
	}
	if err := os.WriteFile(filepath.Join(cfg.root, benchMarker), nil, 0o644); err != nil {
		return state{}, err
	}

	st, err := loadState(cfg.root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return state{}, err
	}
	if st.startEpoch == 0 {
		st.startEpoch, err = inferStartEpoch(cfg)
		if err != nil {
			return state{}, err
		}
	}
	st.fixtureCount = cfg.count
	if err := persistState(cfg.root, st); err != nil {
		return state{}, err
	}
	return st, nil
}

func loadState(root string) (state, error) {
	data, err := os.ReadFile(filepath.Join(root, benchState))
	if err != nil {
		return state{}, err
	}
	st := state{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "fixture_count":
			st.fixtureCount, _ = strconv.Atoi(value)
		case "start_epoch":
			st.startEpoch, _ = strconv.ParseInt(value, 10, 64)
		}
	}
	return st, nil
}

func persistState(root string, st state) error {
	body := fmt.Sprintf("fixture_count=%d\nstart_epoch=%d\n", st.fixtureCount, st.startEpoch)
	return os.WriteFile(filepath.Join(root, benchState), []byte(body), 0o644)
}

func fixtureComplete(cfg config) (bool, error) {
	for i := 0; i < cfg.count; i++ {
		archetype, gitMode := selectArchetype(i)
		info, err := os.Stat(expectedDir(cfg.root, i, archetype, gitMode))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		if !info.IsDir() {
			return false, nil
		}
	}
	return true, nil
}

func inferStartEpoch(cfg config) (int64, error) {
	for i := 0; i < cfg.count; i++ {
		archetype, gitMode := selectArchetype(i)
		info, err := os.Stat(expectedDir(cfg.root, i, archetype, gitMode))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return 0, err
		}
		return info.ModTime().Unix() - int64(i*73), nil
	}
	return time.Now().Unix(), nil
}

func expectedDir(root string, idx int, archetype, gitMode string) string {
	return filepath.Join(root, fmt.Sprintf("%04d-%s-%s", idx, archetype, gitMode))
}

func selectArchetype(idx int) (string, string) {
	switch idx % 10 {
	case 0:
		return "rust", "clean"
	case 1:
		return "python", "none"
	case 2:
		return "node", "dirty"
	case 3:
		return "go", "clean"
	case 4:
		return "lua", "none"
	case 5:
		return "ruby", "dirty"
	case 6:
		return "nix", "clean"
	case 7:
		return "plain", "none"
	case 8:
		return "gitonly", "clean"
	default:
		return "mixed", "dirty"
	}
}

func createFiles(dir, archetype string, idx int) error {
	switch archetype {
	case "rust":
		if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(fmt.Sprintf("[package]\nname = \"bench-%d\"\nversion = \"0.1.0\"\nedition = \"2021\"\n", idx)), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "src", "main.rs"), []byte(fmt.Sprintf("fn main() {\n    println!(\"bench-%d\");\n}\n", idx)), 0o644); err != nil {
			return err
		}
	case "python":
		pkgDir := filepath.Join(dir, "src", fmt.Sprintf("bench_%d", idx))
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(fmt.Sprintf("[project]\nname = \"bench-%d\"\nversion = \"0.1.0\"\n", idx)), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "__init__.py"), []byte(fmt.Sprintf("VALUE = %d\n", idx)), 0o644); err != nil {
			return err
		}
	case "node":
		if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(fmt.Sprintf("{\"name\":\"bench-%d\",\"version\":\"1.0.0\"}\n", idx)), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "src", "index.js"), []byte(fmt.Sprintf("export const value = %d;\n", idx)), 0o644); err != nil {
			return err
		}
	case "go":
		if err := os.MkdirAll(filepath.Join(dir, "cmd", "app"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(fmt.Sprintf("module example.com/bench-%d\n\ngo 1.22\n", idx)), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "cmd", "app", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
			return err
		}
	case "lua":
		if err := os.MkdirAll(filepath.Join(dir, "lua"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "init.lua"), []byte(fmt.Sprintf("return { value = %d }\n", idx)), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "lua", "module.lua"), []byte("return {}\n"), 0o644); err != nil {
			return err
		}
	case "ruby":
		if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "Gemfile"), []byte("source \"https://rubygems.org\"\ngem \"rake\"\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "lib", "bench.rb"), []byte(fmt.Sprintf("module Bench\n  VALUE = %d\nend\n", idx)), 0o644); err != nil {
			return err
		}
	case "nix":
		if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(fmt.Sprintf("{\n  description = \"bench-%d\";\n}\n", idx)), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "default.nix"), []byte(fmt.Sprintf("{ }: { value = %d; }\n", idx)), 0o644); err != nil {
			return err
		}
	case "mixed":
		if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(fmt.Sprintf("[package]\nname = \"bench-mixed-%d\"\nversion = \"0.1.0\"\nedition = \"2021\"\n", idx)), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(fmt.Sprintf("{\"name\":\"bench-mixed-%d\",\"private\":true}\n", idx)), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "src", "main.rs"), []byte("fn main() {}\n"), 0o644); err != nil {
			return err
		}
	case "gitonly", "plain":
		if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(fmt.Sprintf("# bench-%d\n", idx)), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "docs", "notes.txt"), []byte(fmt.Sprintf("fixture %d\n", idx)), 0o644); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown archetype: %s", archetype)
	}

	return os.WriteFile(filepath.Join(dir, ".bench-meta"), []byte(fmt.Sprintf("index=%d\narchetype=%s\n", idx, archetype)), 0o644)
}

func initGitRepo(dir string, idx int, epoch int64, dirty bool) error {
	if err := runGit(dir, nil, "init", "-q"); err != nil {
		return err
	}
	if err := runGit(dir, nil, "config", "user.name", "workspace-launcher bench"); err != nil {
		return err
	}
	if err := runGit(dir, nil, "config", "user.email", "bench@example.com"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("target/\n"), 0o644); err != nil {
		return err
	}
	if err := runGit(dir, nil, "add", "."); err != nil {
		return err
	}
	env := []string{
		"GIT_AUTHOR_DATE=" + strconv.FormatInt(epoch, 10) + " +0000",
		"GIT_COMMITTER_DATE=" + strconv.FormatInt(epoch, 10) + " +0000",
	}
	if err := runGit(dir, env, "-c", "commit.gpgsign=false", "commit", "-q", "-m", fmt.Sprintf("bench commit %d", idx)); err != nil {
		return err
	}
	if dirty {
		return os.WriteFile(filepath.Join(dir, ".bench-dirty"), []byte(fmt.Sprintf("dirty=%d\n", idx)), 0o644)
	}
	return nil
}

func runGit(dir string, extraEnv []string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), extraEnv...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func setMTime(path string, epoch int64) error {
	ts := time.Unix(epoch, 0)
	return os.Chtimes(path, ts, ts)
}

func writeBenchReadme(root string, count int) error {
	body := fmt.Sprintf(`workspace-launcher benchmark fixture
root=%s
count=%d

Example:
  hyperfine --warmup 3 'WORKSPACE_LAUNCHER_BENCH_MODE=headless ./.build/workspace-launcher --print %s >/dev/null'
  hyperfine --warmup 3 'WORKSPACE_LAUNCHER_BENCH_MODE=headless WORKSPACE_LAUNCHER_RECENCY=git ./.build/workspace-launcher --print %s >/dev/null'
`, root, count, root, root)
	return os.WriteFile(filepath.Join(root, "README-bench.txt"), []byte(body), 0o644)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

type exitCodeError struct {
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("exit %d", e.code)
}
