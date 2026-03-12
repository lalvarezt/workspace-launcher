package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	shellscripts "github.com/lalvarezt/workspace-launcher/shell"
	"golang.org/x/term"
	"golang.org/x/text/width"
)

const appName = "workspace-launcher"

var version = "dev"

const (
	activeRootAll      = "__workspace_launcher_all__"
	activeRootAllLabel = "All roots"
	footerRootMaxWidth = 20
)

const (
	modePath = "path"
	modeBash = "bash"
	modeZsh  = "zsh"
	modeFish = "fish"

	recencyMtime = "mtime"
	recencyGit   = "git"

	fzfStyleFull    = "full"
	fzfStyleMinimal = "minimal"
	fzfStylePlain   = "plain"
)

const (
	columnGap      = "   "
	gapWidth       = 3
	rootMinWidth   = 8
	rootMaxWidth   = 40
	rootFloorWidth = 4
	langWidth      = 12
	langMinWidth   = 2
	gitMinWidth    = 2
	gitMaxWidth    = 48
	nameMinWidth   = 16
	ageWidth       = 12
	ageTwoWidth    = 8
	ageOneWidth    = 4
	chromeWidth    = 10
)

const (
	cReset     = "\033[0m"
	cDim       = "\033[38;5;244m"
	cName      = "\033[38;5;252m"
	cCurrent   = "\033[38;5;223m"
	cRootText  = "\033[38;5;235m"
	cRootBadge = "\033[48;5;151m"
	cGo        = "\033[38;5;81m"
	cRust      = "\033[38;5;209m"
	cPython    = "\033[38;5;221m"
	cNode      = "\033[38;5;78m"
	cLua       = "\033[38;5;111m"
	cRuby      = "\033[38;5;203m"
	cNix       = "\033[38;5;110m"
	cMisc      = "\033[38;5;180m"
	cGit       = "\033[38;5;109m"
	cGitDirty  = "\033[38;5;215m"
	cWorktree  = "\033[38;5;151m"
	cGitLock   = "\033[38;5;180m"
	cSubmodule = "\033[38;5;179m"
	cTime      = "\033[38;5;246m"
)

type config struct {
	mode            string
	shellBindings   bool
	initialQuery    string
	fzfStyle        string
	roots           []string
	rootLabels      map[string]string
	jobs            int
	gitDirty        bool
	recency         string
	showLanguage    bool
	showGit         bool
	showRoot        bool
	headlessBench   bool
	now             int64
	cwd             string
	cols            int
	ageColumnWidth  int
	langColumnWidth int
	gitColumnWidth  int
	rootLabelWidth  int
	nameWidth       int
}

type childDir struct {
	name      string
	path      string
	root      string
	rootLabel string
	modEpoch  int64
}

type candidate struct {
	path       string
	display    string
	matchText  string
	branchText string
	epoch      int64
}

type pickerResult struct {
	query      string
	key        string
	selection  string
	createRoot string
}

type repoDetails struct {
	child     childDir
	lang      string
	git       gitMeta
	matchText string
	epoch     int64
}

type dirFacts struct {
	hasGit          bool
	gitIsDir        bool
	hasGoMod        bool
	hasCargoToml    bool
	hasPackageJSON  bool
	hasPyproject    bool
	hasRequirements bool
	hasSetupPy      bool
	hasInitLua      bool
	hasLuarc        bool
	hasGemfile      bool
	hasFlakeNix     bool
	hasDefaultNix   bool
}

type gitMeta struct {
	present     bool
	isWorktree  bool
	isLocked    bool
	isSubmodule bool
	branchLabel string
	headHash    string
	epoch       int64
	dirty       bool
}

type gitLayout struct {
	gitDir    string
	commonDir string
}

type rootLabelParts struct {
	clean string
	parts []string
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
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		return err
	}

	if outputsShellIntegration(cfg.mode) {
		script, err := renderShellIntegration(cfg.mode, cfg.shellBindings)
		if err != nil {
			return err
		}
		_, err = io.WriteString(os.Stdout, script)
		return err
	}

	fzfPath := ""
	if !cfg.headlessBench {
		fzfPath, err = resolveFzf()
		if err != nil {
			return err
		}
	}

	candidates, err := buildCandidates(cfg)
	if err != nil {
		return err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].epoch == candidates[j].epoch {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].epoch > candidates[j].epoch
	})

	result, err := pickRepo(cfg, fzfPath, candidates)
	if err != nil {
		return err
	}
	if result == (pickerResult{}) {
		return exitCodeError{code: 0}
	}

	target, err := resolveSelection(cfg, result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, target)
	return err
}

func parseConfig(args []string) (config, error) {
	maxJobs := runtime.NumCPU()
	if maxJobs < 1 {
		maxJobs = 1
	}

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
			printUsage()
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

func printUsage() {
	fmt.Fprintf(os.Stdout, `Usage: %s [--bash|--zsh|--fish] [--bindings] [--query TEXT] [--fzf-style STYLE] [--[no-]language] [--[no-]git] [-v|--version] [ROOT...]

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

func buildCandidates(cfg config) ([]candidate, error) {
	children := make([]childDir, 0)
	for _, root := range cfg.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			path := filepath.Join(root, entry.Name())
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				continue
			}
			children = append(children, childDir{
				name:      entry.Name(),
				path:      path,
				root:      root,
				rootLabel: cfg.rootLabels[root],
				modEpoch:  info.ModTime().Unix(),
			})
		}
	}
	if len(children) == 0 {
		return nil, nil
	}

	details := make([]repoDetails, len(children))
	needsInspect := cfg.showLanguage || cfg.showGit || cfg.recency == recencyGit
	if !needsInspect || cfg.jobs <= 1 || len(children) == 1 {
		for i, child := range children {
			detail, err := inspectRepo(cfg, child, needsInspect)
			if err != nil {
				return nil, err
			}
			details[i] = detail
		}
		return renderCandidates(cfg, details), nil
	}

	type jobResult struct {
		index  int
		detail repoDetails
		err    error
	}

	jobs := make(chan int, len(children))
	out := make(chan jobResult, len(children))
	var wg sync.WaitGroup
	workerCount := cfg.jobs
	if workerCount > len(children) {
		workerCount = len(children)
	}

	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				detail, err := inspectRepo(cfg, children[idx], needsInspect)
				out <- jobResult{index: idx, detail: detail, err: err}
			}
		}()
	}

	for i := range children {
		jobs <- i
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(out)
	}()

	var firstErr error
	for res := range out {
		if res.err != nil && firstErr == nil {
			firstErr = res.err
			continue
		}
		details[res.index] = res.detail
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return renderCandidates(cfg, details), nil
}

func inspectRepo(cfg config, child childDir, inspect bool) (repoDetails, error) {
	facts := dirFacts{}
	if inspect {
		needGit := cfg.showGit || cfg.recency == recencyGit
		needLanguage := cfg.showLanguage
		var err error
		facts, err = collectDirFacts(child.path, needGit, needLanguage)
		if err != nil {
			return repoDetails{}, err
		}
	}

	epoch := child.modEpoch
	git := gitMeta{}
	if facts.hasGit && (cfg.showGit || cfg.recency == recencyGit) {
		git = inspectGitMeta(child.path, facts.gitIsDir, cfg.showGit, cfg.recency == recencyGit, cfg.gitDirty && cfg.showGit)
	}
	if cfg.recency == recencyGit && git.epoch > 0 {
		epoch = git.epoch
	}

	lang := ""
	if cfg.showLanguage {
		lang = detectLanguage(facts)
	}

	return repoDetails{
		child:     child,
		lang:      lang,
		git:       git,
		matchText: child.name,
		epoch:     epoch,
	}, nil
}

func renderCandidates(cfg config, details []repoDetails) []candidate {
	if width := computeNameColumnWidth(details); width > cfg.nameWidth {
		cfg.nameWidth = width
	}
	cfg.ageColumnWidth = ageWidth
	if cfg.showLanguage {
		cfg.langColumnWidth = langWidth
	} else {
		cfg.langColumnWidth = 0
	}
	if cfg.showGit {
		cfg.gitColumnWidth = computeGitColumnWidth(details)
	} else {
		cfg.gitColumnWidth = 0
	}
	applyLayoutWidths(&cfg)

	out := make([]candidate, len(details))
	styled := effectiveFzfStyle(cfg.fzfStyle) != fzfStylePlain
	for i, detail := range details {
		branch := detail.git.branchLabel
		if branch == "" {
			branch = "-"
		}

		markerField := paintFieldStyled(styled, cDim, " ")
		if isCurrentRepo(cfg.cwd, detail.child.path) {
			markerField = paintFieldStyled(styled, cCurrent, "*")
		}
		nameField := markerField + " " + paintFieldStyled(styled, cName, fitField(detail.child.name, cfg.nameWidth))
		ageField := renderAgeFieldStyled(formatAge(cfg.now, detail.epoch), cfg.ageColumnWidth, styled)

		fields := make([]string, 0, 4)
		if cfg.showRoot {
			fields = append(fields, paintFieldStyled(styled, cDim, fitField(detail.child.rootLabel, cfg.rootLabelWidth)))
		}
		fields = append(fields, nameField)
		if cfg.showLanguage {
			fields = append(fields, renderLangFieldStyled(detail.lang, cfg.langColumnWidth, styled))
		}
		if cfg.showGit {
			fields = append(fields, renderGitFieldStyled(detail.git, branch, cfg.gitColumnWidth, styled))
		}
		fields = append(fields, ageField)

		out[i] = candidate{
			path:       detail.child.path,
			display:    joinDisplayFields(fields),
			matchText:  detail.matchText,
			branchText: branchSearchText(detail.git.branchLabel),
			epoch:      detail.epoch,
		}
	}
	return out
}

func describeRepo(cfg config, child childDir, inspect bool) (candidate, error) {
	detail, err := inspectRepo(cfg, child, inspect)
	if err != nil {
		return candidate{}, err
	}
	if cfg.cols == 0 {
		if cfg.ageColumnWidth == 0 {
			cfg.ageColumnWidth = ageWidth
		}
		metaWidth := cfg.ageColumnWidth
		if cfg.showLanguage {
			if cfg.langColumnWidth == 0 {
				cfg.langColumnWidth = langWidth
			}
			metaWidth += cfg.langColumnWidth + gapWidth
		}
		if cfg.showGit {
			if cfg.gitColumnWidth == 0 {
				cfg.gitColumnWidth = computeGitColumnWidth([]repoDetails{detail})
			}
			metaWidth += cfg.gitColumnWidth + gapWidth
		}
		cfg.cols = cfg.nameWidth + chromeWidth + metaWidth
		if cfg.showRoot {
			cfg.cols += cfg.rootLabelWidth + gapWidth
		}
	}
	return renderCandidates(cfg, []repoDetails{detail})[0], nil
}

func collectDirFacts(dir string, needGit, needLanguage bool) (dirFacts, error) {
	facts := dirFacts{}
	languageDetected := false

	file, err := os.Open(dir)
	if err != nil {
		return facts, err
	}
	defer file.Close()

	for {
		entries, readErr := file.ReadDir(16)
		for _, entry := range entries {
			switch entry.Name() {
			case ".git":
				facts.hasGit = true
				facts.gitIsDir = entry.IsDir()
			case "go.mod":
				facts.hasGoMod = true
			case "Cargo.toml":
				facts.hasCargoToml = true
			case "package.json":
				facts.hasPackageJSON = true
			case "pyproject.toml":
				facts.hasPyproject = true
			case "requirements.txt":
				facts.hasRequirements = true
			case "setup.py":
				facts.hasSetupPy = true
			case "init.lua":
				facts.hasInitLua = true
			case ".luarc.json":
				facts.hasLuarc = true
			case "Gemfile":
				facts.hasGemfile = true
			case "flake.nix":
				facts.hasFlakeNix = true
			case "default.nix":
				facts.hasDefaultNix = true
			}

			if !languageDetected && detectLanguage(facts) != "-" {
				languageDetected = true
			}
			if (!needGit || facts.hasGit) && (!needLanguage || languageDetected) {
				return facts, nil
			}
		}

		if errors.Is(readErr, io.EOF) {
			return facts, nil
		}
		if readErr != nil {
			return facts, readErr
		}
	}
}

func buildRootLabels(roots []string) map[string]string {
	labels := make(map[string]string, len(roots))
	if len(roots) == 0 {
		return labels
	}

	partsByRoot := make(map[string]rootLabelParts, len(roots))
	depths := make(map[string]int, len(roots))
	for _, root := range roots {
		clean := filepath.Clean(root)
		partsByRoot[root] = rootLabelParts{
			clean: clean,
			parts: splitPathParts(clean),
		}
		depths[root] = 1
	}

	for {
		groups := make(map[string][]string, len(roots))
		for _, root := range roots {
			label := rootLabelAtDepth(partsByRoot[root], depths[root])
			groups[label] = append(groups[label], root)
		}

		collisions := false
		progressed := false
		for label, group := range groups {
			if len(group) == 1 {
				labels[group[0]] = label
				continue
			}
			collisions = true
			for _, root := range group {
				info := partsByRoot[root]
				if depths[root] < len(info.parts) {
					depths[root]++
					progressed = true
					continue
				}
				labels[root] = info.clean
			}
		}

		if !collisions {
			return labels
		}
		if !progressed {
			for _, root := range roots {
				if _, ok := labels[root]; !ok {
					labels[root] = partsByRoot[root].clean
				}
			}
			return labels
		}
	}
}

func splitPathParts(path string) []string {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	remainder := strings.TrimPrefix(clean, volume)
	remainder = strings.TrimPrefix(remainder, string(filepath.Separator))
	if remainder == "" {
		return nil
	}
	parts := strings.Split(remainder, string(filepath.Separator))
	out := parts[:0]
	for _, part := range parts {
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func rootLabelAtDepth(info rootLabelParts, depth int) string {
	if len(info.parts) == 0 {
		return info.clean
	}
	if depth > len(info.parts) {
		depth = len(info.parts)
	}
	start := len(info.parts) - depth
	if start < 0 {
		start = 0
	}
	return filepath.Join(info.parts[start:]...)
}

func computeRootLabelWidth(labels map[string]string) int {
	longest := rootMinWidth
	for _, label := range labels {
		width := displayWidth(label)
		if width > longest {
			longest = width
		}
	}
	if longest > rootMaxWidth {
		return rootMaxWidth
	}
	return longest
}

func applyLayoutWidths(cfg *config) {
	targetNameWidth := cfg.nameWidth
	if targetNameWidth < nameMinWidth {
		targetNameWidth = nameMinWidth
	}

	langColumnWidth := 0
	if cfg.showLanguage {
		langColumnWidth = cfg.langColumnWidth
		if langColumnWidth == 0 {
			langColumnWidth = langWidth
		}
	}

	gitColumnWidth := 0
	if cfg.showGit {
		gitColumnWidth = cfg.gitColumnWidth
		if gitColumnWidth == 0 {
			gitColumnWidth = gitMinWidth
		}
	}

	rootLabelWidth := 0
	if cfg.showRoot {
		rootLabelWidth = cfg.rootLabelWidth
	}

	ageColumnWidth := cfg.ageColumnWidth
	if ageColumnWidth == 0 {
		ageColumnWidth = ageWidth
	}
	ageColumnWidth = normalizeAgeColumnWidth(ageColumnWidth)

	reservedWidth := chromeWidth + ageColumnWidth
	if cfg.showLanguage {
		reservedWidth += langColumnWidth + gapWidth
	}
	if cfg.showGit {
		reservedWidth += gitColumnWidth + gapWidth
	}
	if cfg.showRoot {
		reservedWidth += rootLabelWidth + gapWidth
	}

	nameWidth := cfg.cols - reservedWidth
	deficit := targetNameWidth - nameWidth
	if deficit > 0 && cfg.showLanguage {
		shrink := deficit
		if maxShrink := langColumnWidth - langMinWidth; maxShrink < shrink {
			shrink = maxShrink
		}
		langColumnWidth -= shrink
		deficit -= shrink
	}
	if deficit > 0 {
		ageColumnWidth = shrinkAgeColumnWidth(ageColumnWidth, deficit)
	}
	if deficit > 0 {
		reservedWidth = chromeWidth + ageColumnWidth
		if cfg.showLanguage {
			reservedWidth += langColumnWidth + gapWidth
		}
		if cfg.showGit {
			reservedWidth += gitColumnWidth + gapWidth
		}
		if cfg.showRoot {
			reservedWidth += rootLabelWidth + gapWidth
		}
		nameWidth = cfg.cols - reservedWidth
		deficit = targetNameWidth - nameWidth
	}
	if deficit > 0 && cfg.showGit {
		shrink := deficit
		if maxShrink := gitColumnWidth - gitMinWidth; maxShrink < shrink {
			shrink = maxShrink
		}
		gitColumnWidth -= shrink
		deficit -= shrink
	}

	reservedWidth = chromeWidth + ageColumnWidth
	if cfg.showLanguage {
		reservedWidth += langColumnWidth + gapWidth
	}
	if cfg.showGit {
		reservedWidth += gitColumnWidth + gapWidth
	}
	if cfg.showRoot {
		reservedWidth += rootLabelWidth + gapWidth
	}

	cfg.ageColumnWidth = ageColumnWidth
	cfg.langColumnWidth = langColumnWidth
	cfg.gitColumnWidth = gitColumnWidth
	cfg.rootLabelWidth = rootLabelWidth
	cfg.nameWidth = cfg.cols - reservedWidth
	if cfg.nameWidth < nameMinWidth {
		cfg.nameWidth = nameMinWidth
	}
}

func computeGitColumnWidth(details []repoDetails) int {
	longest := gitMinWidth
	for _, detail := range details {
		width := displayWidth(gitFieldText(detail.git, detail.git.branchLabel))
		if width > longest {
			longest = width
		}
	}
	if longest > gitMaxWidth {
		return gitMaxWidth
	}
	return longest
}

func computeNameColumnWidth(details []repoDetails) int {
	longest := nameMinWidth
	for _, detail := range details {
		width := displayWidth(detail.child.name)
		if width > longest {
			longest = width
		}
	}
	return longest
}

func detectLanguage(facts dirFacts) string {
	switch {
	case facts.hasGoMod:
		return "Go"
	case facts.hasCargoToml:
		return "Rust"
	case facts.hasPackageJSON:
		return "Node"
	case facts.hasPyproject || facts.hasRequirements || facts.hasSetupPy:
		return "Python"
	case facts.hasInitLua || facts.hasLuarc:
		return "Lua"
	case facts.hasGemfile:
		return "Ruby"
	case facts.hasFlakeNix || facts.hasDefaultNix:
		return "Nix"
	default:
		return "-"
	}
}

func inspectGitMeta(dir string, gitIsDir, wantBranch, wantEpoch, wantDirty bool) gitMeta {
	meta := gitMeta{
		present:     true,
		branchLabel: "-",
	}

	if !wantBranch && !wantEpoch {
		meta.isWorktree = !gitIsDir
		if !gitIsDir {
			gitDir, isWorktree, err := inspectDotGit(dir)
			if err == nil {
				meta.isWorktree = isWorktree
				meta.isSubmodule, meta.isLocked = classifyLinkedGitDir(gitDir, isWorktree)
				if meta.isSubmodule {
					meta.isWorktree = false
				}
			}
		}
		if wantDirty {
			if dirty, dirtyErr := gitIsDirty(dir); dirtyErr == nil {
				meta.dirty = dirty
			}
		}
		return meta
	}

	gitDir := filepath.Join(dir, ".git")
	isWorktree := false
	if !gitIsDir {
		var err error
		gitDir, isWorktree, err = inspectDotGit(dir)
		if err != nil {
			if wantDirty {
				if dirty, dirtyErr := gitIsDirty(dir); dirtyErr == nil {
					meta.dirty = dirty
				}
			}
			if wantEpoch {
				if epoch, epochErr := gitLastCommitEpochSlow(dir); epochErr == nil && epoch > 0 {
					meta.epoch = epoch
				}
			}
			return meta
		}
	}

	meta.isWorktree = isWorktree
	meta.isSubmodule, meta.isLocked = classifyLinkedGitDir(gitDir, isWorktree)
	if meta.isSubmodule {
		meta.isWorktree = false
	}
	if wantEpoch {
		layout, layoutErr := finalizeGitLayout(gitDir)
		if layoutErr == nil {
			head, headErr := readHeadFile(layout.gitDir)
			if headErr == nil {
				if wantBranch {
					meta.branchLabel = formatHeadLabel(head)
				}
				if hash, resolveErr := resolveHeadHashFromHead(layout, head); resolveErr == nil {
					meta.headHash = hash
					if epoch, readErr := readCommitEpoch(layout, hash); readErr == nil && epoch > 0 {
						meta.epoch = epoch
					}
				}
			}
		}
	} else {
		head, headErr := readHeadFile(gitDir)
		if headErr == nil && wantBranch {
			meta.branchLabel = formatHeadLabel(head)
		}
	}

	if wantEpoch && meta.epoch <= 0 {
		if epoch, epochErr := gitLastCommitEpochSlow(dir); epochErr == nil && epoch > 0 {
			meta.epoch = epoch
		}
	}
	if wantDirty {
		if dirty, dirtyErr := gitIsDirty(dir); dirtyErr == nil {
			meta.dirty = dirty
		}
	}

	return meta
}

func classifyLinkedGitDir(gitDir string, isWorktree bool) (bool, bool) {
	if !isWorktree {
		return false, false
	}

	cleanParts := strings.Split(filepath.Clean(gitDir), string(filepath.Separator))
	gitIndex := -1
	for i := len(cleanParts) - 1; i >= 0; i-- {
		if cleanParts[i] == ".git" {
			gitIndex = i
			break
		}
	}
	if gitIndex >= 0 && gitIndex+1 < len(cleanParts) && cleanParts[gitIndex+1] == "modules" {
		return true, false
	}
	if _, err := os.Stat(filepath.Join(gitDir, "locked")); err == nil {
		return false, true
	}
	return false, false
}

func gitLastCommitEpochFast(dir string) (int64, error) {
	layout, err := resolveGitLayout(dir)
	if err == nil {
		head, headErr := readHeadFile(layout.gitDir)
		if headErr == nil {
			headHash, resolveErr := resolveHeadHashFromHead(layout, head)
			if resolveErr == nil {
				if epoch, readErr := readCommitEpoch(layout, headHash); readErr == nil && epoch > 0 {
					return epoch, nil
				}
			}
		}
	}
	return gitLastCommitEpochSlow(dir)
}

func gitLastCommitEpochSlow(dir string) (int64, error) {
	cmd := exec.Command("git", "-C", dir, "-c", "log.showSignature=false", "log", "-1", "--format=%ct")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return 0, errors.New("empty git epoch")
	}
	epoch, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return epoch, nil
}

func resolveGitLayout(dir string) (gitLayout, error) {
	gitDir, _, err := inspectDotGit(dir)
	if err != nil {
		return gitLayout{}, err
	}
	return finalizeGitLayout(filepath.Clean(gitDir))
}

func inspectDotGit(dir string) (string, bool, error) {
	gitPath := filepath.Join(dir, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", false, err
	}
	if info.IsDir() {
		return gitPath, false, nil
	}

	content, err := os.ReadFile(gitPath)
	if err != nil {
		return "", false, err
	}
	line := strings.TrimSpace(string(content))
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return "", false, errors.New("unsupported .git file format")
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	return filepath.Clean(gitDir), true, nil
}

func finalizeGitLayout(gitDir string) (gitLayout, error) {
	layout := gitLayout{
		gitDir:    gitDir,
		commonDir: gitDir,
	}
	content, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return layout, nil
		}
		return gitLayout{}, err
	}
	commonDir := strings.TrimSpace(string(content))
	if commonDir == "" {
		return layout, nil
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	layout.commonDir = filepath.Clean(commonDir)
	return layout, nil
}

func readHeadFile(gitDir string) (string, error) {
	content, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(content))
	if head == "" {
		return "", errors.New("empty HEAD")
	}
	return head, nil
}

func resolveHeadHash(layout gitLayout) (string, error) {
	head, err := readHeadFile(layout.gitDir)
	if err != nil {
		return "", err
	}
	return resolveHeadHashFromHead(layout, head)
}

func resolveHeadHashFromHead(layout gitLayout, head string) (string, error) {
	if !strings.HasPrefix(head, "ref: ") {
		return head, nil
	}

	refName := strings.TrimSpace(strings.TrimPrefix(head, "ref: "))
	for _, baseDir := range []string{layout.gitDir, layout.commonDir} {
		refPath := filepath.Join(baseDir, filepath.FromSlash(refName))
		if refValue, err := os.ReadFile(refPath); err == nil {
			hash := strings.TrimSpace(string(refValue))
			if hash != "" {
				return hash, nil
			}
		}
	}

	return lookupPackedRef(layout, refName)
}

func formatHeadLabel(head string) string {
	head = strings.TrimSpace(head)
	if head == "" {
		return "-"
	}
	if strings.HasPrefix(head, "ref: ") {
		return formatRefLabel(strings.TrimSpace(strings.TrimPrefix(head, "ref: ")))
	}
	if len(head) > 7 {
		head = head[:7]
	}
	return "detached@" + head
}

func formatRefLabel(refName string) string {
	refName = strings.TrimSpace(refName)
	if refName == "" {
		return "-"
	}
	switch {
	case strings.HasPrefix(refName, "refs/heads/"):
		return strings.TrimPrefix(refName, "refs/heads/")
	case strings.HasPrefix(refName, "refs/remotes/"):
		return strings.TrimPrefix(refName, "refs/remotes/")
	case strings.HasPrefix(refName, "refs/"):
		tail := strings.TrimPrefix(refName, "refs/")
		if strings.Count(tail, "/") <= 1 {
			return path.Base(tail)
		}
		return tail
	default:
		return path.Base(refName)
	}
}

func lookupPackedRef(layout gitLayout, refName string) (string, error) {
	for _, baseDir := range []string{layout.gitDir, layout.commonDir} {
		hash, err := lookupPackedRefFile(filepath.Join(baseDir, "packed-refs"), refName)
		if err == nil {
			return hash, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", errors.New("ref not found")
}

func lookupPackedRefFile(path, refName string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[1] == refName {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("ref not found")
}

func readCommitEpoch(layout gitLayout, hash string) (int64, error) {
	if len(hash) < 40 {
		return 0, errors.New("invalid commit hash")
	}

	objectDirs := []string{
		filepath.Join(layout.gitDir, "objects"),
		filepath.Join(layout.commonDir, "objects"),
	}
	for _, objectDir := range objectDirs {
		epoch, err := readCommitEpochFromObjects(objectDir, hash)
		if err == nil {
			return epoch, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
	}
	return 0, os.ErrNotExist
}

func readCommitEpochFromObjects(objectDir, hash string) (int64, error) {
	objectPath := filepath.Join(objectDir, hash[:2], hash[2:])
	file, err := os.Open(objectPath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	reader, err := zlib.NewReader(file)
	if err != nil {
		return 0, err
	}
	defer reader.Close()

	buf := bufio.NewReaderSize(reader, 1024)
	if _, err := buf.ReadBytes(0); err != nil {
		return 0, errors.New("invalid object header")
	}

	for {
		line, err := buf.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if strings.HasPrefix(line, "committer ") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) < 3 {
				break
			}
			epoch, parseErr := strconv.ParseInt(fields[len(fields)-2], 10, 64)
			if parseErr != nil {
				return 0, parseErr
			}
			return epoch, nil
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	return 0, errors.New("committer line not found")
}

func gitIsDirty(dir string) (bool, error) {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=normal")
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(output)) > 0, nil
}

type pickerState struct {
	dir            string
	rootFile       string
	footerFile     string
	candidatesFile string
	cycleFile      string
	filterFile     string
}

func pickRepo(cfg config, fzfPath string, candidates []candidate) (pickerResult, error) {
	if cfg.headlessBench {
		return pickRepoHeadless(cfg, candidates)
	}

	state, err := createPickerState(cfg, candidates)
	if err != nil {
		return pickerResult{}, err
	}
	if state.dir != "" {
		defer os.RemoveAll(state.dir)
	}

	args := baseFzfArgs(cfg, state)
	args = append(args, fzfStyleArgs(cfg, state)...)
	cmd := exec.Command(fzfPath, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return pickerResult{}, err
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return pickerResult{}, err
	}

	writeErr := writeCandidates(stdin, candidates)
	waitErr := cmd.Wait()
	if isPickerAbort(waitErr) {
		if writeErr == nil || isClosedPickerPipe(writeErr) {
			return pickerResult{}, nil
		}
		return pickerResult{}, writeErr
	}
	if writeErr != nil {
		return pickerResult{}, writeErr
	}
	if waitErr != nil {
		return pickerResult{}, waitErr
	}
	result := parsePickerResult(stdout.String())
	result.createRoot = readPickerCreateRoot(cfg, state)
	return result, nil
}

func createPickerState(cfg config, candidates []candidate) (pickerState, error) {
	if len(cfg.roots) < 2 {
		return pickerState{}, nil
	}

	dir, err := os.MkdirTemp("", "workspace-launcher-fzf-*")
	if err != nil {
		return pickerState{}, err
	}

	state := pickerState{
		dir:            dir,
		rootFile:       filepath.Join(dir, "active-root"),
		footerFile:     filepath.Join(dir, "footer"),
		candidatesFile: filepath.Join(dir, "candidates"),
		cycleFile:      filepath.Join(dir, "cycle-root.sh"),
		filterFile:     filepath.Join(dir, "filter-root.sh"),
	}

	initialRoot := activeRootAll
	if err := os.WriteFile(state.rootFile, []byte(initialRoot), 0o600); err != nil {
		os.RemoveAll(dir)
		return pickerState{}, err
	}
	if err := os.WriteFile(state.footerFile, []byte(createFooterText(cfg, initialRoot)), 0o600); err != nil {
		os.RemoveAll(dir)
		return pickerState{}, err
	}
	if err := writeCandidateFile(state.candidatesFile, candidates); err != nil {
		os.RemoveAll(dir)
		return pickerState{}, err
	}
	if err := os.WriteFile(state.cycleFile, []byte(buildCycleRootScript(cfg)), 0o700); err != nil {
		os.RemoveAll(dir)
		return pickerState{}, err
	}
	if err := os.WriteFile(state.filterFile, []byte(buildFilterCandidatesScript()), 0o700); err != nil {
		os.RemoveAll(dir)
		return pickerState{}, err
	}

	return state, nil
}

func baseFzfArgs(cfg config, state pickerState) []string {
	args := []string{
		"--scheme=history",
		"--layout=reverse",
		"--tabstop=1",
		"--info=hidden",
		"--delimiter=\t",
		"--with-nth=5..",
		"--nth=" + fzfSearchNth(cfg),
		"--print-query",
		"--expect=ctrl-e,ctrl-n",
		"--query=" + cfg.initialQuery,
		"--bind=enter:accept-or-print-query",
	}

	if state.cycleFile != "" {
		args = append(args,
			"--bind=ctrl-r:execute-silent("+shellSingleQuote(state.cycleFile)+" "+shellSingleQuote(state.rootFile)+" "+shellSingleQuote(state.footerFile)+")+reload("+shellSingleQuote(state.filterFile)+" "+shellSingleQuote(state.rootFile)+" "+shellSingleQuote(state.candidatesFile)+")+transform-footer(cat "+shellSingleQuote(state.footerFile)+")",
		)
	}

	return args
}

func fzfStyleArgs(cfg config, state pickerState) []string {
	footerText := defaultFooterText()
	if state.footerFile != "" {
		footerText = createFooterText(cfg, activeRootAll)
	}

	ghostText := "Type to filter, Enter to open, Ctrl-E to edit, Ctrl-N to create"
	if state.cycleFile != "" {
		ghostText += ", Ctrl-R to switch root"
	}

	switch effectiveFzfStyle(cfg.fzfStyle) {
	case fzfStyleFull:
		return []string{
			"--ansi",
			"--prompt=",
			"--pointer=▌",
			"--color=bg:-1,bg+:#1d252c,fg:#d8d0c4,fg+:#f6efe2",
			"--color=hl:#e0a65b,hl+:#ffd08a,prompt:#8ecfd0,query:#f6efe2,ghost:#6d7d88",
			"--color=border:#50606b,label:#91c7c8,list-border:#5d7282,list-label:#a4d5d6",
			"--color=input-border:#8a6c4f,input-label:#efbf7a,footer-border:#44515c",
			"--color=pointer:#efbf7a,separator:#36434d,scrollbar:#55636e",
			"--ghost=" + ghostText,
			"--input-border",
			"--input-label= Search/New ",
			"--list-border",
			"--list-label= Folders ",
			"--footer=" + footerText,
			"--footer-border=line",
			"--bind=result:transform-list-label:printf \" Folders (%s) \" \"$FZF_MATCH_COUNT\"",
		}
	case fzfStyleMinimal:
		return []string{
			"--ansi",
			"--prompt=",
			"--pointer=▌",
			"--ghost=" + ghostText,
			"--input-border",
			"--input-label= Search/New ",
			"--list-border",
			"--list-label= Folders ",
			"--footer=" + footerText,
			"--footer-border=line",
			"--bind=result:transform-list-label:printf \" Folders (%s) \" \"$FZF_MATCH_COUNT\"",
		}
	case fzfStylePlain:
		return nil
	default:
		return nil
	}
}

func effectiveFzfStyle(style string) string {
	if style == "" {
		return fzfStyleFull
	}
	return style
}

func fzfSearchNth(cfg config) string {
	// --nth applies to the fields exposed by --with-nth, not the hidden serialized
	// prefix fields. Keep this aligned with the visible display-column order.
	columns := make([]string, 0, 2)
	column := 1
	if cfg.showRoot {
		column++
	}

	columns = append(columns, strconv.Itoa(column))
	column++
	if cfg.showLanguage {
		column++
	}
	if cfg.showGit {
		columns = append(columns, strconv.Itoa(column))
	}

	return strings.Join(columns, ",")
}

func pickRepoHeadless(cfg config, candidates []candidate) (pickerResult, error) {
	query := strings.ToLower(cfg.initialQuery)
	for _, cand := range candidates {
		line := serializeCandidate(cand)
		if query == "" || strings.Contains(strings.ToLower(candidateSearchText(cand)), query) {
			return pickerResult{selection: line, createRoot: defaultCreateRoot(cfg)}, nil
		}
	}
	return pickerResult{}, exitCodeError{code: 1}
}

func writeCandidates(w io.WriteCloser, candidates []candidate) error {
	defer w.Close()
	buf := bufio.NewWriterSize(w, 1<<20)
	for _, cand := range candidates {
		if _, err := buf.WriteString(serializeCandidate(cand)); err != nil {
			return err
		}
		if err := buf.WriteByte('\n'); err != nil {
			return err
		}
	}
	return buf.Flush()
}

func writeCandidateFile(path string, candidates []candidate) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := bufio.NewWriterSize(file, 1<<20)
	for _, cand := range candidates {
		if _, err := buf.WriteString(serializeCandidate(cand)); err != nil {
			return err
		}
		if err := buf.WriteByte('\n'); err != nil {
			return err
		}
	}
	return buf.Flush()
}

func isPickerAbort(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == 130)
}

func isClosedPickerPipe(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EPIPE)
}

func serializeCandidate(cand candidate) string {
	return cand.path + "\t" + cand.matchText + "\t\t" + cand.branchText + "\t" + cand.display
}

func candidateSearchText(cand candidate) string {
	if cand.branchText == "" {
		return cand.matchText
	}
	return cand.matchText + " " + cand.branchText
}

func branchSearchText(branch string) string {
	if branch == "" || branch == "-" {
		return ""
	}
	return branch
}

func defaultFooterText() string {
	return "Enter open | Ctrl-E edit | Ctrl-N create | Esc quit"
}

func createFooterText(cfg config, root string) string {
	if len(cfg.roots) < 2 {
		return defaultFooterText()
	}

	return renderFooterRootBadge(cfg, root) + "  Enter open | Ctrl-E edit | Ctrl-N create | Ctrl-R switch root | Esc quit"
}

func pickerRootLabel(cfg config, root string) string {
	if root == activeRootAll {
		return activeRootAllLabel
	}
	if cfg.rootLabels != nil {
		if label := cfg.rootLabels[root]; label != "" {
			return label
		}
	}
	base := filepath.Base(root)
	if base != "." && base != string(filepath.Separator) && base != "" {
		return base
	}
	return root
}

func buildCycleRootScript(cfg config) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -eu\n")
	b.WriteString("root_file=$1\n")
	b.WriteString("footer_file=$2\n")
	fmt.Fprintf(&b, "current=%s\n", shellSingleQuote(activeRootAll))
	b.WriteString("if [ -f \"$root_file\" ]; then\n")
	b.WriteString("  current=$(cat \"$root_file\")\n")
	b.WriteString("fi\n")
	b.WriteString("case \"$current\" in\n")
	fmt.Fprintf(&b, "  %s)\n", shellSingleQuote(activeRootAll))
	fmt.Fprintf(&b, "    next_root=%s\n", shellSingleQuote(cfg.roots[0]))
	fmt.Fprintf(&b, "    next_footer=%s\n", shellSingleQuote(createFooterText(cfg, cfg.roots[0])))
	b.WriteString("    ;;\n")
	for i, root := range cfg.roots {
		nextRoot := activeRootAll
		if i < len(cfg.roots)-1 {
			nextRoot = cfg.roots[i+1]
		}
		fmt.Fprintf(&b, "  %s)\n", shellSingleQuote(root))
		fmt.Fprintf(&b, "    next_root=%s\n", shellSingleQuote(nextRoot))
		fmt.Fprintf(&b, "    next_footer=%s\n", shellSingleQuote(createFooterText(cfg, nextRoot)))
		b.WriteString("    ;;\n")
	}
	fmt.Fprintf(&b, "  *)\n    next_root=%s\n    next_footer=%s\n    ;;\n", shellSingleQuote(activeRootAll), shellSingleQuote(createFooterText(cfg, activeRootAll)))
	b.WriteString("esac\n")
	b.WriteString("printf '%s' \"$next_root\" > \"$root_file\"\n")
	b.WriteString("printf '%s' \"$next_footer\" > \"$footer_file\"\n")
	return b.String()
}

func buildFilterCandidatesScript() string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -eu\n")
	b.WriteString("root_file=$1\n")
	b.WriteString("candidates_file=$2\n")
	fmt.Fprintf(&b, "active_root=%s\n", shellSingleQuote(activeRootAll))
	b.WriteString("if [ -f \"$root_file\" ]; then\n")
	b.WriteString("  active_root=$(cat \"$root_file\")\n")
	b.WriteString("fi\n")
	fmt.Fprintf(&b, "if [ \"$active_root\" = %s ]; then\n", shellSingleQuote(activeRootAll))
	b.WriteString("  cat \"$candidates_file\"\n")
	b.WriteString("  exit 0\n")
	b.WriteString("fi\n")
	b.WriteString("awk -F '\\t' -v root=\"$active_root\" 'index($1, root \"/\") == 1 || $1 == root { print }' \"$candidates_file\"\n")
	return b.String()
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func readPickerCreateRoot(cfg config, state pickerState) string {
	if state.rootFile == "" {
		return defaultCreateRoot(cfg)
	}
	content, err := os.ReadFile(state.rootFile)
	if err != nil {
		return defaultCreateRoot(cfg)
	}
	root := strings.TrimSpace(string(content))
	if root == activeRootAll {
		return activeRootAll
	}
	for _, configuredRoot := range cfg.roots {
		if root == configuredRoot {
			return root
		}
	}
	return defaultCreateRoot(cfg)
}

func parsePickerResult(result string) pickerResult {
	result = strings.TrimRight(result, "\n")
	if result == "" {
		return pickerResult{}
	}

	lines := strings.Split(result, "\n")
	if len(lines) == 1 {
		if strings.Contains(lines[0], "\t") {
			return pickerResult{selection: lines[0]}
		}
		return pickerResult{query: lines[0]}
	}

	parsed := pickerResult{query: lines[0]}
	lines = lines[1:]
	if len(lines) == 0 {
		return parsed
	}
	if lines[0] == "" || isPickerKey(lines[0]) {
		parsed.key = lines[0]
		lines = lines[1:]
	}
	if len(lines) > 0 {
		parsed.selection = lines[0]
	}
	return parsed
}

func isPickerKey(key string) bool {
	switch key {
	case "ctrl-e", "ctrl-n":
		return true
	default:
		return false
	}
}

func resolveSelection(cfg config, result pickerResult) (string, error) {
	switch result.key {
	case "ctrl-e":
		target, ok := selectedPath(result.selection)
		if !ok {
			return "", errors.New("no directory selected")
		}
		return "", openInEditor(target)
	case "ctrl-n":
		return createWorkspace(cfg, result.query, result.createRoot)
	case "":
		if target, ok := selectedPath(result.selection); ok {
			return target, nil
		}
		name := result.query
		if name == "" {
			name = result.selection
		}
		return createWorkspace(cfg, name, result.createRoot)
	default:
		return "", fmt.Errorf("unknown key: %s", result.key)
	}
}

func selectedPath(selection string) (string, bool) {
	if !strings.Contains(selection, "\t") {
		return "", false
	}
	return strings.SplitN(selection, "\t", 2)[0], true
}

func createWorkspace(cfg config, name, currentRoot string) (string, error) {
	if err := validateNewName(name); err != nil {
		return "", err
	}

	root, err := resolveCreateRoot(cfg, currentRoot)
	if err != nil {
		return "", err
	}

	target := filepath.Join(root, name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	return target, nil
}

func defaultCreateRoot(cfg config) string {
	if len(cfg.roots) == 0 {
		return ""
	}
	return cfg.roots[0]
}

func resolveCreateRoot(cfg config, currentRoot string) (string, error) {
	if currentRoot == activeRootAll {
		return defaultCreateRoot(cfg), nil
	}

	for _, configuredRoot := range cfg.roots {
		if currentRoot == configuredRoot {
			return configuredRoot, nil
		}
	}

	if len(cfg.roots) == 1 {
		return defaultCreateRoot(cfg), nil
	}

	return "", errors.New("no active create root selected")
}

func validateNewName(name string) error {
	if name == "" {
		return errors.New("empty query")
	}
	if strings.ContainsRune(name, filepath.Separator) {
		return errors.New("new directory name cannot contain '/'")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("invalid directory name: %s", name)
	}
	return nil
}

func openInEditor(target string) error {
	if os.Getenv("VISUAL") == "" && os.Getenv("EDITOR") == "" {
		return errors.New("VISUAL or EDITOR is not set")
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /dev/tty: %w", err)
	}
	defer tty.Close()
	shell := os.Getenv("BASH")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "-lc", `exec ${VISUAL:-${EDITOR:-}} "$1"`, "sh", target)
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitCodeError{code: exitErr.ExitCode()}
		}
		return fmt.Errorf("open editor: %w", err)
	}
	return exitCodeError{code: 0}
}

func outputsShellIntegration(mode string) bool {
	switch mode {
	case modeBash, modeZsh, modeFish:
		return true
	default:
		return false
	}
}

func renderShellIntegration(mode string, bindings bool) (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	return renderShellIntegrationForPath(mode, exePath, bindings)
}

func renderShellIntegrationForPath(mode, binPath string, bindings bool) (string, error) {
	switch mode {
	case modeBash:
		return shellscripts.Bash(binPath, bindings), nil
	case modeZsh:
		return shellscripts.Zsh(binPath, bindings), nil
	case modeFish:
		return shellscripts.Fish(binPath, bindings), nil
	default:
		return "", fmt.Errorf("unknown shell integration mode: %s", mode)
	}
}

func resolveFzf() (string, error) {
	if fzfBin := os.Getenv("FZF_BIN"); fzfBin != "" {
		return fzfBin, nil
	}
	exePath, err := os.Executable()
	if err == nil {
		exePath, _ = filepath.EvalSymlinks(exePath)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(exePath), ".."))
		candidate := filepath.Join(repoRoot, "bin", "fzf")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	path, err := exec.LookPath("fzf")
	if err != nil {
		return "", errors.New("fzf not found")
	}
	return path, nil
}

func formatAge(now, epoch int64) string {
	diff := now - epoch
	if diff < 0 {
		diff = 0
	}
	days := diff / 86400
	hours := (diff % 86400) / 3600
	mins := (diff % 3600) / 60
	return fmt.Sprintf("%02dd %02dh %02dm", days, hours, mins)
}

func normalizeAgeColumnWidth(width int) int {
	switch {
	case width >= ageWidth:
		return ageWidth
	case width >= ageTwoWidth:
		return ageTwoWidth
	default:
		return ageOneWidth
	}
}

func shrinkAgeColumnWidth(width, deficit int) int {
	width = normalizeAgeColumnWidth(width)
	for deficit > 0 {
		next := width
		switch width {
		case ageWidth:
			next = ageTwoWidth
		case ageTwoWidth:
			next = ageOneWidth
		default:
			return width
		}
		deficit -= width - next
		width = next
	}
	return width
}

func isCurrentRepo(cwd, dir string) bool {
	return cwd == dir || strings.HasPrefix(cwd, dir+string(filepath.Separator))
}

func fitField(text string, width int) string {
	if width <= 0 {
		return ""
	}
	visibleWidth := displayWidth(text)
	if visibleWidth <= width {
		return text + strings.Repeat(" ", width-visibleWidth)
	}
	if width <= 3 {
		return trimDisplayWidth(text, width)
	}
	trimmed := trimDisplayWidth(text, width-3) + "..."
	return trimmed + strings.Repeat(" ", width-displayWidth(trimmed))
}

func centerField(text string, width int) string {
	if width <= 0 {
		return ""
	}

	fitted := fitField(text, width)
	trimmed := strings.TrimRight(fitted, " ")
	visibleWidth := displayWidth(trimmed)
	if visibleWidth >= width {
		return fitted
	}

	leftPad := (width - visibleWidth) / 2
	rightPad := width - visibleWidth - leftPad
	return strings.Repeat(" ", leftPad) + trimmed + strings.Repeat(" ", rightPad)
}

func joinDisplayFields(fields []string) string {
	if len(fields) == 0 {
		return ""
	}

	padded := make([]string, len(fields))
	for i, field := range fields {
		padded[i] = field
		if i < len(fields)-1 && gapWidth > 1 {
			padded[i] += strings.Repeat(" ", gapWidth-1)
		}
	}

	return strings.Join(padded, "\t")
}

func displayWidth(text string) int {
	width := 0
	for _, r := range text {
		width += runeDisplayWidth(r)
	}
	return width
}

func runeDisplayWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return 0
	case r <= unicode.MaxASCII:
		return 1
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	case unicode.In(r, unicode.Co):
		return 1
	default:
		kind := width.LookupRune(r).Kind()
		if kind == width.EastAsianWide || kind == width.EastAsianFullwidth {
			return 2
		}
		return 1
	}
}

func trimDisplayWidth(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	width := 0
	var b strings.Builder
	for _, r := range text {
		rw := runeDisplayWidth(r)
		if width+rw > maxWidth {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String()
}

func paintFieldStyled(styled bool, color, text string) string {
	if !styled {
		return text
	}
	return color + text + cReset
}

func renderFooterRootBadge(cfg config, root string) string {
	label := pickerRootLabel(cfg, root)
	width := computeFooterRootWidth(cfg.rootLabels)
	if width == 0 {
		width = footerRootMaxWidth
	}
	text := " " + centerField(label, width) + " "
	if effectiveFzfStyle(cfg.fzfStyle) == fzfStylePlain {
		return text
	}
	return cRootText + cRootBadge + text + cReset
}

func computeFooterRootWidth(labels map[string]string) int {
	longest := displayWidth(activeRootAllLabel)
	for _, label := range labels {
		width := displayWidth(label)
		if width > longest {
			longest = width
		}
	}
	if longest > footerRootMaxWidth {
		return footerRootMaxWidth
	}
	return longest
}

func renderAgeFieldStyled(age string, width int, styled bool) string {
	if width <= 0 {
		return ""
	}

	blocks := strings.Fields(age)
	switch normalizedWidth := normalizeAgeColumnWidth(width); {
	case normalizedWidth >= ageWidth:
		return paintFieldStyled(styled, cTime, fitField(age, width))
	case normalizedWidth >= ageTwoWidth:
		limit := 2
		if len(blocks) < limit {
			limit = len(blocks)
		}
		text := strings.Join(blocks[:limit], " ")
		return paintFieldStyled(styled, cTime, fitField(text, width))
	default:
		text := age
		if len(blocks) > 0 {
			text = blocks[0]
		}
		return paintFieldStyled(styled, cTime, fitField(text, width))
	}
}

func renderLangFieldStyled(lang string, width int, styled bool) string {
	icon := "•"
	label := "Misc"
	color := cMisc

	switch lang {
	case "Go":
		icon, label, color = "", "Go", cGo
	case "Rust":
		icon, label, color = "", "Rust", cRust
	case "Python":
		icon, label, color = "", "Python", cPython
	case "Node":
		icon, label, color = "", "Node", cNode
	case "Lua":
		icon, label, color = "", "Lua", cLua
	case "Ruby":
		icon, label, color = "", "Ruby", cRuby
	case "Nix":
		icon, label, color = "", "Nix", cNix
	}

	iconCell := icon + "  "
	if icon == "•" {
		iconCell = "•  "
	}
	if width <= 0 {
		return ""
	}
	return paintFieldStyled(styled, color, fitField(iconCell+label, width))
}

func gitFieldText(meta gitMeta, branch string) string {
	if !meta.present {
		return "-"
	}

	icon := ""
	switch {
	case meta.isLocked:
		icon = ""
	case meta.isWorktree:
		icon = "󰙅"
	case meta.isSubmodule:
		icon = ""
	}

	text := icon
	if branch != "" && branch != "-" {
		text += "  " + branch
	}
	return text
}

func renderGitFieldStyled(meta gitMeta, branch string, width int, styled bool) string {
	if !meta.present {
		return paintFieldStyled(styled, cDim, fitField("-", width))
	}

	color := cGit
	switch {
	case meta.isLocked:
		color = cGitLock
	case meta.isWorktree:
		color = cWorktree
	case meta.isSubmodule:
		color = cSubmodule
	}
	if meta.dirty {
		color = cGitDirty
	}

	return paintFieldStyled(styled, color, fitField(gitFieldText(meta, branch), width))
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

type exitCodeError struct {
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("exit %d", e.code)
}
