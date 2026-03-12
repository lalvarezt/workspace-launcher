package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

func parseConfig(args []string) (config, error) {
	maxJobs := max(runtime.NumCPU(), 1)

	roots := parseRootList(getenvDefault("WORKSPACE_LAUNCHER_ROOT", "~/git-repos"))
	jobs := clampJobs(parsePositiveEnvInt("WORKSPACE_LAUNCHER_JOBS", maxJobs), maxJobs)
	cfg := config{
		mode:          modePath,
		fzfStyle:      fzfStyleFull,
		roots:         roots,
		jobs:          jobs,
		gitDirty:      os.Getenv("WORKSPACE_LAUNCHER_GIT_DIRTY") == "1",
		recency:       recencyMtime,
		showLanguage:  os.Getenv("WORKSPACE_LAUNCHER_SHOW_LANGUAGE") != "0",
		showGit:       os.Getenv("WORKSPACE_LAUNCHER_SHOW_GIT") != "0",
		headlessBench: os.Getenv("WORKSPACE_LAUNCHER_BENCH_MODE") == "headless",
		now:           time.Now().Unix(),
	}
	if os.Getenv("WORKSPACE_LAUNCHER_RECENCY") == recencyGit {
		cfg.recency = recencyGit
	}
	rootSet := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--bash":
			cfg.mode = modeBash
		case arg == "--zsh":
			cfg.mode = modeZsh
		case arg == "--fish":
			cfg.mode = modeFish
		case arg == "--bindings":
			cfg.shellBindings = true
		case arg == "--query":
			i++
			if i >= len(args) {
				return config{}, errors.New("missing value for --query")
			}
			cfg.initialQuery = args[i]
		case strings.HasPrefix(arg, "--query="):
			cfg.initialQuery = strings.TrimPrefix(arg, "--query=")
		case arg == "--fzf-style":
			i++
			if i >= len(args) {
				return config{}, errors.New("missing value for --fzf-style")
			}
			style, err := parseFzfStyle(args[i])
			if err != nil {
				return config{}, err
			}
			cfg.fzfStyle = style
		case strings.HasPrefix(arg, "--fzf-style="):
			style, err := parseFzfStyle(strings.TrimPrefix(arg, "--fzf-style="))
			if err != nil {
				return config{}, err
			}
			cfg.fzfStyle = style
		case arg == "--language":
			cfg.showLanguage = true
		case arg == "--no-language":
			cfg.showLanguage = false
		case arg == "--git":
			cfg.showGit = true
		case arg == "--no-git":
			cfg.showGit = false
		case arg == "-v" || arg == "--version":
			if _, err := fmt.Fprintf(os.Stdout, "%s %s\n", appName, version); err != nil {
				return config{}, err
			}
			return config{}, exitCodeError{code: 0}
		case arg == "-h" || arg == "--help":
			if err := printUsage(); err != nil {
				return config{}, err
			}
			return config{}, exitCodeError{code: 0}
		case arg == "--":
			if i+1 < len(args) {
				if !rootSet {
					cfg.roots = nil
					rootSet = true
				}
				cfg.roots = append(cfg.roots, args[i+1:]...)
			}
			i = len(args)
		case strings.HasPrefix(arg, "-"):
			return config{}, fmt.Errorf("unknown option: %s", arg)
		default:
			if !rootSet {
				cfg.roots = nil
				rootSet = true
			}
			cfg.roots = append(cfg.roots, arg)
		}
	}

	if outputsShellIntegration(cfg.mode) {
		return cfg, nil
	}

	if cfg.shellBindings {
		return config{}, errors.New("--bindings can only be used with --bash, --zsh, or --fish")
	}

	resolvedRoots, err := resolveRoots(cfg.roots)
	if err != nil {
		return config{}, err
	}
	cfg.roots = resolvedRoots
	cfg.showRoot = len(cfg.roots) > 1
	if cfg.showRoot {
		cfg.rootLabels = buildRootLabels(cfg.roots)
		cfg.rootLabelWidth = computeRootLabelWidth(cfg.rootLabels)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return config{}, err
	}
	if resolvedCwd, err := filepath.EvalSymlinks(cwd); err == nil {
		cfg.cwd = resolvedCwd
	} else {
		cfg.cwd = cwd
	}

	cfg.cols = resolveColumns()
	applyLayoutWidths(&cfg)

	return cfg, nil
}

func printUsage() error {
	_, err := fmt.Fprintf(os.Stdout, `Usage: %s [--bash|--zsh|--fish] [--bindings] [--query TEXT] [--fzf-style STYLE] [--[no-]language] [--[no-]git] [-v|--version] [ROOT...]

Launch an fzf-based directory picker for directories under one or more roots.
Selecting an existing entry opens that directory; submitting a new query creates it.
In multi-root mode, creation uses the current root. Ctrl-R cycles that root, and
the footer shows the active target.

Options:
  --bash           Print bash shell integration
  --zsh            Print zsh shell integration
  --fish           Print fish shell integration
  --bindings       Include default Ctrl-G shell bindings with shell integration
  --query TEXT     Start with an initial query
  --fzf-style STYLE
                   Picker style: full (default), minimal, or plain
  --language       Show the language column (default)
  --no-language    Hide the language column
  --git            Show the git metadata column (default)
  --no-git         Hide the git metadata column
  -v, --version    Show version
  -h, --help       Show this help text

Shell integration:
  bash/zsh         source <(workspace-launcher --bash|--zsh [--bindings])
  fish             workspace-launcher --fish [--bindings] | source

Environment:
  WORKSPACE_LAUNCHER_ROOT           Default root directories, split with the OS path list separator (default: ~/git-repos)
  WORKSPACE_LAUNCHER_JOBS           Parallel jobs, clamped to 1..CPU count
  WORKSPACE_LAUNCHER_GIT_DIRTY      Highlight dirty git entries when set to 1 (default: 0)
  WORKSPACE_LAUNCHER_RECENCY        Sort recency by directory mtime or latest git commit
  WORKSPACE_LAUNCHER_SHOW_LANGUAGE  Show the language column when set to 1 (default: 1)
  WORKSPACE_LAUNCHER_SHOW_GIT       Show the git metadata column when set to 1 (default: 1)
`, filepath.Base(os.Args[0]))
	return err
}

func parseFzfStyle(style string) (string, error) {
	switch style {
	case fzfStyleFull, fzfStyleMinimal, fzfStylePlain:
		return style, nil
	default:
		return "", fmt.Errorf("invalid value for --fzf-style: %s", style)
	}
}

func resolveRoot(root string) (string, error) {
	root = expandHome(root)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("root does not exist: %s", root)
		}
		return "", err
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("root does not exist: %s", root)
		}
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root does not exist: %s", root)
	}
	return resolvedRoot, nil
}

func resolveRoots(roots []string) ([]string, error) {
	if len(roots) == 0 {
		return nil, errors.New("at least one root is required")
	}

	resolved := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		resolvedRoot, err := resolveRoot(root)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[resolvedRoot]; ok {
			continue
		}
		seen[resolvedRoot] = struct{}{}
		resolved = append(resolved, resolvedRoot)
	}
	if len(resolved) == 0 {
		return nil, errors.New("at least one root is required")
	}
	return resolved, nil
}

func expandHome(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func parseRootList(value string) []string {
	if value == "" {
		return nil
	}
	parts := filepath.SplitList(value)
	if len(parts) == 0 {
		parts = []string{value}
	}
	roots := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		roots = append(roots, part)
	}
	return roots
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func resolveColumns() int {
	if _, ok := os.LookupEnv("COLUMNS"); ok {
		return parsePositiveEnvInt("COLUMNS", 120)
	}

	if cols, err := terminalColumns(int(os.Stdout.Fd())); err == nil && cols > 0 {
		return cols
	}

	tty, err := os.Open("/dev/tty")
	if err == nil {
		defer tty.Close()
		if cols, ttyErr := terminalColumns(int(tty.Fd())); ttyErr == nil && cols > 0 {
			return cols
		}
	}

	return 120
}

func terminalColumns(fd int) (int, error) {
	if !term.IsTerminal(fd) {
		return 0, errors.New("not a terminal")
	}
	cols, _, err := term.GetSize(fd)
	if err != nil {
		return 0, err
	}
	if cols <= 0 {
		return 0, errors.New("invalid terminal width")
	}
	return cols, nil
}

func parsePositiveEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

func clampJobs(jobs, maxJobs int) int {
	if jobs < 1 {
		return 1
	}
	if jobs > maxJobs {
		return maxJobs
	}
	return jobs
}
