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
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const appName = "workspace-launcher"

var version = "dev"

const (
	modePrint = "print"
	modeShell = "shell"

	recencyMtime = "mtime"
	recencyGit   = "git"
)

const (
	columnGap      = "   "
	gapWidth       = 3
	langWidth      = 12
	langLabelWidth = langWidth - 3
	gitWidth       = 5
	ageWidth       = 12
	chromeWidth    = 18
)

const (
	cReset    = "\033[0m"
	cDim      = "\033[38;5;244m"
	cName     = "\033[38;5;252m"
	cCurrent  = "\033[38;5;223m"
	cGo       = "\033[38;5;81m"
	cRust     = "\033[38;5;209m"
	cPython   = "\033[38;5;221m"
	cNode     = "\033[38;5;78m"
	cLua      = "\033[38;5;111m"
	cRuby     = "\033[38;5;203m"
	cNix      = "\033[38;5;110m"
	cMisc     = "\033[38;5;180m"
	cGit      = "\033[38;5;109m"
	cGitDirty = "\033[38;5;215m"
	cTime     = "\033[38;5;246m"
)

type config struct {
	mode          string
	initialQuery  string
	root          string
	jobs          int
	gitDirty      bool
	recency       string
	showLanguage  bool
	showGit       bool
	headlessBench bool
	now           int64
	cwd           string
	nameWidth     int
}

type childDir struct {
	name     string
	path     string
	modEpoch int64
}

type candidate struct {
	path    string
	display string
	epoch   int64
}

type dirFacts struct {
	hasGit          bool
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

type gitLayout struct {
	gitDir    string
	commonDir string
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
	if result == "" {
		return exitCodeError{code: 1}
	}

	key, selection := splitResult(result)
	target, err := resolveSelection(cfg, key, selection)
	if err != nil {
		return err
	}

	switch cfg.mode {
	case modePrint:
		_, err = fmt.Fprintln(os.Stdout, target)
		return err
	case modeShell:
		if err := os.Chdir(target); err != nil {
			return err
		}
		return execShell()
	default:
		return fmt.Errorf("unknown mode: %s", cfg.mode)
	}
}

func parseConfig(args []string) (config, error) {
	maxJobs := runtime.NumCPU()
	if maxJobs < 1 {
		maxJobs = 1
	}

	root := getenvDefault("WORKSPACE_LAUNCHER_ROOT", "~/git-repos")
	jobs := clampJobs(parsePositiveEnvInt("WORKSPACE_LAUNCHER_JOBS", maxJobs), maxJobs)
	cfg := config{
		mode:          modePrint,
		root:          root,
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
		case arg == "--print":
			cfg.mode = modePrint
		case arg == "--shell":
			cfg.mode = modeShell
		case arg == "--query":
			i++
			if i >= len(args) {
				return config{}, errors.New("missing value for --query")
			}
			cfg.initialQuery = args[i]
		case strings.HasPrefix(arg, "--query="):
			cfg.initialQuery = strings.TrimPrefix(arg, "--query=")
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
				if rootSet || i+2 < len(args) {
					return config{}, errors.New("too many arguments")
				}
				cfg.root = args[i+1]
				rootSet = true
			}
			i = len(args)
		case strings.HasPrefix(arg, "-"):
			return config{}, fmt.Errorf("unknown option: %s", arg)
		default:
			if rootSet {
				return config{}, errors.New("too many arguments")
			}
			cfg.root = arg
			rootSet = true
		}
	}

	resolvedRoot, err := resolveRoot(cfg.root)
	if err != nil {
		return config{}, err
	}
	cfg.root = resolvedRoot

	cwd, err := os.Getwd()
	if err != nil {
		return config{}, err
	}
	if resolvedCwd, err := filepath.EvalSymlinks(cwd); err == nil {
		cfg.cwd = resolvedCwd
	} else {
		cfg.cwd = cwd
	}

	cols := parsePositiveEnvInt("COLUMNS", 120)
	metaWidth := ageWidth
	if cfg.showLanguage {
		metaWidth += langWidth + gapWidth
	}
	if cfg.showGit {
		metaWidth += gitWidth + gapWidth
	}
	cfg.nameWidth = cols - chromeWidth - metaWidth
	if cfg.nameWidth < 16 {
		cfg.nameWidth = 16
	}

	return cfg, nil
}

func printUsage() {
	fmt.Fprintf(os.Stdout, `Usage: %s [--print|--shell] [--query TEXT] [--[no-]language] [--[no-]git] [-v|--version] [ROOT]

Launch an fzf-based directory picker for directories under ROOT.
Selecting an existing entry opens that directory; submitting a new query creates it.

Options:
  --print          Print the selected or created path (default)
  --shell          Start an interactive shell in the selected path
  --query TEXT     Start with an initial query
  --language       Show the language column (default)
  --no-language    Hide the language column
  --git            Show the git-state column (default)
  --no-git         Hide the git-state column
  -v, --version    Show version
  -h, --help       Show this help text

Environment:
  WORKSPACE_LAUNCHER_ROOT           Default root directory (default: ~/git-repos)
  WORKSPACE_LAUNCHER_JOBS           Parallel jobs, clamped to 1..CPU count
  WORKSPACE_LAUNCHER_GIT_DIRTY      Show dirty repos with git* when set to 1 (default: 0)
  WORKSPACE_LAUNCHER_RECENCY        Sort recency by directory mtime or latest git commit
  WORKSPACE_LAUNCHER_SHOW_LANGUAGE  Show the language column when set to 1 (default: 1)
  WORKSPACE_LAUNCHER_SHOW_GIT       Show the git-state column when set to 1 (default: 1)
`, filepath.Base(os.Args[0]))
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

func buildCandidates(cfg config) ([]candidate, error) {
	entries, err := os.ReadDir(cfg.root)
	if err != nil {
		return nil, err
	}

	children := make([]childDir, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(cfg.root, entry.Name())
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		children = append(children, childDir{
			name:     entry.Name(),
			path:     path,
			modEpoch: info.ModTime().Unix(),
		})
	}
	if len(children) == 0 {
		return nil, nil
	}

	results := make([]candidate, len(children))
	needsInspect := cfg.showLanguage || cfg.showGit || cfg.recency == recencyGit
	if !needsInspect || cfg.jobs <= 1 || len(children) == 1 {
		for i, child := range children {
			cand, err := describeRepo(cfg, child, needsInspect)
			if err != nil {
				return nil, err
			}
			results[i] = cand
		}
		return results, nil
	}

	type jobResult struct {
		index int
		cand  candidate
		err   error
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
				cand, err := describeRepo(cfg, children[idx], needsInspect)
				out <- jobResult{index: idx, cand: cand, err: err}
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
		results[res.index] = res.cand
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

func describeRepo(cfg config, child childDir, inspect bool) (candidate, error) {
	facts := dirFacts{}
	if inspect {
		needGit := cfg.showGit || cfg.recency == recencyGit
		needLanguage := cfg.showLanguage
		languageDetected := false
		entries, err := os.ReadDir(child.path)
		if err != nil {
			return candidate{}, err
		}
		for _, entry := range entries {
			name := entry.Name()
			switch name {
			case ".git":
				facts.hasGit = true
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
				break
			}
		}
	}

	epoch := child.modEpoch
	if cfg.recency == recencyGit && facts.hasGit {
		if gitEpoch, err := gitLastCommitEpochFast(child.path); err == nil && gitEpoch > 0 {
			epoch = gitEpoch
		}
	}

	lang := ""
	if cfg.showLanguage {
		lang = detectLanguage(facts)
	}

	gitState := ""
	if cfg.showGit {
		gitState = detectGitState(cfg, child.path, facts.hasGit)
	}

	markerField := paintField(cDim, " ")
	if isCurrentRepo(cfg.cwd, child.path) {
		markerField = paintField(cCurrent, "*")
	}
	nameField := paintField(cName, fitField(child.name, cfg.nameWidth))
	ageField := paintField(cTime, fitField(formatAge(cfg.now, epoch), ageWidth))

	var display strings.Builder
	display.Grow(len(child.name) + 96)
	display.WriteString(markerField)
	display.WriteString(" ")
	display.WriteString(nameField)
	if cfg.showLanguage {
		display.WriteString(columnGap)
		display.WriteString(renderLangField(lang))
	}
	if cfg.showGit {
		display.WriteString(columnGap)
		display.WriteString(renderGitField(gitState))
	}
	display.WriteString(columnGap)
	display.WriteString(ageField)

	return candidate{
		path:    child.path,
		display: display.String(),
		epoch:   epoch,
	}, nil
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

func detectGitState(cfg config, dir string, hasGit bool) string {
	if !hasGit {
		return "-"
	}
	if !cfg.gitDirty {
		return "git"
	}
	dirty, err := gitIsDirty(dir)
	if err != nil {
		return "git"
	}
	if dirty {
		return "git*"
	}
	return "git"
}

func gitLastCommitEpochFast(dir string) (int64, error) {
	layout, err := resolveGitLayout(dir)
	if err == nil {
		headHash, resolveErr := resolveHeadHash(layout)
		if resolveErr == nil {
			if epoch, readErr := readCommitEpoch(layout, headHash); readErr == nil && epoch > 0 {
				return epoch, nil
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
	gitPath := filepath.Join(dir, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return gitLayout{}, err
	}
	gitDir := gitPath
	if info.IsDir() {
		return finalizeGitLayout(gitDir)
	}

	content, err := os.ReadFile(gitPath)
	if err != nil {
		return gitLayout{}, err
	}
	line := strings.TrimSpace(string(content))
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return gitLayout{}, errors.New("unsupported .git file format")
	}
	gitDir = strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	return finalizeGitLayout(filepath.Clean(gitDir))
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

func resolveHeadHash(layout gitLayout) (string, error) {
	content, err := os.ReadFile(filepath.Join(layout.gitDir, "HEAD"))
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(content))
	if head == "" {
		return "", errors.New("empty HEAD")
	}
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

func pickRepo(cfg config, fzfPath string, candidates []candidate) (string, error) {
	if cfg.headlessBench {
		return pickRepoHeadless(cfg, candidates)
	}

	cmd := exec.Command(
		fzfPath,
		"--ansi",
		"--layout=reverse",
		"--prompt=",
		"--pointer=▌",
		"--color=bg:-1,bg+:#1d252c,fg:#d8d0c4,fg+:#f6efe2",
		"--color=hl:#e0a65b,hl+:#ffd08a,prompt:#8ecfd0,query:#f6efe2,ghost:#6d7d88",
		"--color=border:#50606b,label:#91c7c8,list-border:#5d7282,list-label:#a4d5d6",
		"--color=input-border:#8a6c4f,input-label:#efbf7a,footer-border:#44515c,footer-label:#87b69f",
		"--color=pointer:#efbf7a,separator:#36434d,scrollbar:#55636e",
		"--ghost=Type to filter, Enter to open, Ctrl-E to edit, Ctrl-N to create",
		"--input-border",
		"--input-label= Search/New ",
		"--list-border",
		"--list-label= Folders ",
		"--footer=Enter open | Ctrl-E edit | Ctrl-N create | Esc quit",
		"--footer-border=line",
		"--info=hidden",
		"--delimiter=\t",
		"--with-nth=2..",
		"--expect=ctrl-e",
		"--query="+cfg.initialQuery,
		"--bind=enter:accept-or-print-query",
		"--bind=ctrl-n:print-query+accept",
		"--bind=result:transform-list-label:printf \" Folders (%s) \" \"$FZF_MATCH_COUNT\"",
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return "", err
	}

	writeErr := writeCandidates(stdin, candidates)
	waitErr := cmd.Wait()
	if writeErr != nil {
		return "", writeErr
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
			return "", exitCodeError{code: 1}
		}
		return "", waitErr
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func pickRepoHeadless(cfg config, candidates []candidate) (string, error) {
	query := strings.ToLower(cfg.initialQuery)
	for _, cand := range candidates {
		line := cand.path + "\t" + cand.display
		if query == "" || strings.Contains(strings.ToLower(line), query) {
			return line, nil
		}
	}
	return "", exitCodeError{code: 1}
}

func writeCandidates(w io.WriteCloser, candidates []candidate) error {
	defer w.Close()
	buf := bufio.NewWriterSize(w, 1<<20)
	for _, cand := range candidates {
		if _, err := buf.WriteString(cand.path); err != nil {
			return err
		}
		if _, err := buf.WriteString("\t"); err != nil {
			return err
		}
		if _, err := buf.WriteString(cand.display); err != nil {
			return err
		}
		if err := buf.WriteByte('\n'); err != nil {
			return err
		}
	}
	return buf.Flush()
}

func splitResult(result string) (string, string) {
	parts := strings.SplitN(result, "\n", 2)
	if len(parts) == 2 && (parts[0] == "" || strings.HasPrefix(parts[0], "ctrl-")) {
		return parts[0], parts[1]
	}
	return "", result
}

func resolveSelection(cfg config, key, selection string) (string, error) {
	switch key {
	case "ctrl-e":
		if !strings.Contains(selection, "\t") {
			return "", errors.New("no directory selected")
		}
		target := strings.SplitN(selection, "\t", 2)[0]
		return target, openInEditor(target)
	case "":
		if strings.Contains(selection, "\t") {
			return strings.SplitN(selection, "\t", 2)[0], nil
		}
		if err := validateNewName(selection); err != nil {
			return "", err
		}
		target := filepath.Join(cfg.root, selection)
		if err := os.MkdirAll(target, 0o755); err != nil {
			return "", err
		}
		return target, nil
	default:
		return "", fmt.Errorf("unknown key: %s", key)
	}
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
	shell := os.Getenv("BASH")
	if shell == "" {
		shell = "/bin/bash"
	}
	return syscall.Exec(shell, []string{shell, "-lc", `exec ${VISUAL:-${EDITOR:-}} "$1"`, "sh", target}, os.Environ())
}

func execShell() error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	return syscall.Exec(shell, []string{shell, "-il"}, os.Environ())
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

func isCurrentRepo(cwd, dir string) bool {
	return cwd == dir || strings.HasPrefix(cwd, dir+string(filepath.Separator))
}

func fitField(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= width {
		return fmt.Sprintf("%-*s", width, text)
	}
	if width <= 3 {
		runes := []rune(text)
		return string(runes[:width])
	}
	runes := []rune(text)
	return fmt.Sprintf("%-*s", width, string(runes[:width-3])+"...")
}

func paintField(color, text string) string {
	return color + text + cReset
}

func renderLangField(lang string) string {
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
	return paintField(color, iconCell+fitField(label, langLabelWidth))
}

func renderGitField(state string) string {
	switch state {
	case "git":
		return paintField(cGit, fitField("git", gitWidth))
	case "git*":
		return paintField(cGitDirty, fitField("git*", gitWidth))
	default:
		return paintField(cDim, fitField("-", gitWidth))
	}
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

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
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
