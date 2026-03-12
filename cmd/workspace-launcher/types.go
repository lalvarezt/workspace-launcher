package main

import "fmt"

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

type pickerState struct {
	dir            string
	rootFile       string
	footerFile     string
	candidatesFile string
	cycleFile      string
	filterFile     string
}

type exitCodeError struct {
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("exit %d", e.code)
}
