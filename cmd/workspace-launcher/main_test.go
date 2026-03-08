package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGitLastCommitEpochFastLooseObject(t *testing.T) {
	repo := initTestRepo(t)
	commitAt(t, repo, "1700000000", "first")

	epoch, err := gitLastCommitEpochFast(repo)
	if err != nil {
		t.Fatalf("gitLastCommitEpochFast returned error: %v", err)
	}
	if epoch != 1700000000 {
		t.Fatalf("unexpected epoch: got %d want %d", epoch, 1700000000)
	}
}

func TestGitLastCommitEpochFastPackedRefs(t *testing.T) {
	repo := initTestRepo(t)
	commitAt(t, repo, "1700000100", "packed")
	runGit(t, repo, "pack-refs", "--all")

	refPath := filepath.Join(repo, ".git", "refs", "heads", currentBranchName(t, repo))
	if err := os.Remove(refPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove loose ref: %v", err)
	}

	epoch, err := gitLastCommitEpochFast(repo)
	if err != nil {
		t.Fatalf("gitLastCommitEpochFast returned error: %v", err)
	}
	if epoch != 1700000100 {
		t.Fatalf("unexpected epoch: got %d want %d", epoch, 1700000100)
	}
}

func TestGitLastCommitEpochFastDetachedHead(t *testing.T) {
	repo := initTestRepo(t)
	commitAt(t, repo, "1700000200", "detached")

	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "checkout", "--detach", head)

	epoch, err := gitLastCommitEpochFast(repo)
	if err != nil {
		t.Fatalf("gitLastCommitEpochFast returned error: %v", err)
	}
	if epoch != 1700000200 {
		t.Fatalf("unexpected epoch: got %d want %d", epoch, 1700000200)
	}
}

func TestResolveGitLayoutWorktree(t *testing.T) {
	repo := initTestRepo(t)
	commitAt(t, repo, "1700000300", "base")

	worktree := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", worktree)

	layout, err := resolveGitLayout(worktree)
	if err != nil {
		t.Fatalf("resolveGitLayout returned error: %v", err)
	}
	if !strings.Contains(layout.gitDir, filepath.Join(".git", "worktrees")) {
		t.Fatalf("unexpected worktree git dir: %s", layout.gitDir)
	}
	if layout.commonDir == layout.gitDir {
		t.Fatalf("expected worktree commonDir to differ from gitDir")
	}
}

func TestGitLastCommitEpochFastWorktree(t *testing.T) {
	repo := initTestRepo(t)
	commitAt(t, repo, "1700000300", "base")

	worktree := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", worktree)

	epoch, err := gitLastCommitEpochFast(worktree)
	if err != nil {
		t.Fatalf("gitLastCommitEpochFast returned error: %v", err)
	}
	if epoch != 1700000300 {
		t.Fatalf("unexpected epoch: got %d want %d", epoch, 1700000300)
	}
}

func TestDescribeRepoFallsBackToMtimeWhenGitHasNoCommits(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "empty-git")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "test")
	runGit(t, repo, "config", "user.email", "test@example.com")

	mtime := time.Unix(1700000400, 0)
	if err := os.Chtimes(repo, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	cfg := config{
		recency:      recencyGit,
		showLanguage: true,
		showGit:      true,
		now:          1700000500,
		nameWidth:    32,
	}
	cand, err := describeRepo(cfg, childDir{
		name:     filepath.Base(repo),
		path:     repo,
		modEpoch: mtime.Unix(),
	}, true)
	if err != nil {
		t.Fatalf("describeRepo returned error: %v", err)
	}
	if cand.epoch != mtime.Unix() {
		t.Fatalf("unexpected epoch: got %d want %d", cand.epoch, mtime.Unix())
	}
}

func TestBuildCandidatesMatchesExpectedOrdering(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, "plain-new"), 1700001000, "")
	makeGitRepo(t, filepath.Join(root, "git-old"), 1700000000)
	makeGitRepo(t, filepath.Join(root, "git-new"), 1700002000)

	cfg := config{
		root:         root,
		jobs:         4,
		recency:      recencyGit,
		showLanguage: true,
		showGit:      true,
		now:          1700003000,
		nameWidth:    32,
	}

	cands, err := buildCandidates(cfg)
	if err != nil {
		t.Fatalf("buildCandidates returned error: %v", err)
	}

	sort.Slice(cands, func(i, j int) bool {
		if cands[i].epoch == cands[j].epoch {
			return cands[i].path < cands[j].path
		}
		return cands[i].epoch > cands[j].epoch
	})

	got := []string{
		filepath.Base(cands[0].path),
		filepath.Base(cands[1].path),
		filepath.Base(cands[2].path),
	}
	want := []string{"git-new", "plain-new", "git-old"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected ordering: got %v want %v", got, want)
		}
	}
}

func TestPickRepoHeadlessSelectsFirstCandidate(t *testing.T) {
	cfg := config{headlessBench: true}
	candidates := []candidate{
		{path: "/tmp/b", display: "beta"},
		{path: "/tmp/a", display: "alpha"},
	}

	got, err := pickRepoHeadless(cfg, candidates)
	if err != nil {
		t.Fatalf("pickRepoHeadless returned error: %v", err)
	}
	if got != "/tmp/b\tbeta" {
		t.Fatalf("unexpected selection: %q", got)
	}
}

func TestPickRepoHeadlessFiltersByQuery(t *testing.T) {
	cfg := config{headlessBench: true, initialQuery: "alp"}
	candidates := []candidate{
		{path: "/tmp/b", display: "beta"},
		{path: "/tmp/a", display: "alpha"},
	}

	got, err := pickRepoHeadless(cfg, candidates)
	if err != nil {
		t.Fatalf("pickRepoHeadless returned error: %v", err)
	}
	if got != "/tmp/a\talpha" {
		t.Fatalf("unexpected filtered selection: %q", got)
	}
}

func TestSplitResultTreatsEmptyExpectLineAsNoKey(t *testing.T) {
	key, selection := splitResult("\n/tmp/fzf\tentry")
	if key != "" {
		t.Fatalf("unexpected key: %q", key)
	}
	if selection != "/tmp/fzf\tentry" {
		t.Fatalf("unexpected selection: %q", selection)
	}
}

func TestSplitResultSeparatesExpectedKey(t *testing.T) {
	key, selection := splitResult("ctrl-e\n/tmp/fzf\tentry")
	if key != "ctrl-e" {
		t.Fatalf("unexpected key: %q", key)
	}
	if selection != "/tmp/fzf\tentry" {
		t.Fatalf("unexpected selection: %q", selection)
	}
}

func TestRenderShellIntegrationIncludesPreludeAndWidget(t *testing.T) {
	tempExe := filepath.Join(t.TempDir(), `workspace-launcher$bin"test`)

	tests := []struct {
		mode        string
		wantPrelude string
		wantSnippet string
	}{
		{mode: modeBash, wantPrelude: `__workspace_launcher_bin='`, wantSnippet: "workspace-launcher-widget"},
		{mode: modeZsh, wantPrelude: `__workspace_launcher_bin='`, wantSnippet: "workspace-launcher-widget"},
		{mode: modeFish, wantPrelude: `set -g __workspace_launcher_bin "`, wantSnippet: "workspace-launcher-widget"},
	}

	for _, tt := range tests {
		script, err := renderShellIntegrationForPath(tt.mode, tempExe, false)
		if err != nil {
			t.Fatalf("renderShellIntegration(%s) returned error: %v", tt.mode, err)
		}
		if !strings.Contains(script, tt.wantPrelude) {
			t.Fatalf("expected prelude for %s in %q", tt.mode, script)
		}
		if !strings.Contains(script, tt.wantSnippet) {
			t.Fatalf("expected widget for %s in %q", tt.mode, script)
		}
	}
}

func TestRenderShellIntegrationOmitsBindingsByDefault(t *testing.T) {
	tests := []struct {
		mode   string
		wantNo string
	}{
		{mode: modeBash, wantNo: `bind -m emacs-standard`},
		{mode: modeZsh, wantNo: `bindkey -M emacs '^G' workspace-launcher-widget`},
		{mode: modeFish, wantNo: `bind \cg workspace-launcher-widget`},
	}

	for _, tt := range tests {
		script, err := renderShellIntegrationForPath(tt.mode, "/tmp/workspace-launcher", false)
		if err != nil {
			t.Fatalf("renderShellIntegration(%s) returned error: %v", tt.mode, err)
		}
		if strings.Contains(script, tt.wantNo) {
			t.Fatalf("expected %s integration to omit default bindings in %q", tt.mode, script)
		}
	}
}

func TestRenderShellIntegrationIncludesBindingsWhenRequested(t *testing.T) {
	tests := []struct {
		mode string
		want string
	}{
		{mode: modeBash, want: `bind -m emacs-standard`},
		{mode: modeZsh, want: `bindkey -M emacs '^G' workspace-launcher-widget`},
		{mode: modeFish, want: `bind \cg workspace-launcher-widget`},
	}

	for _, tt := range tests {
		script, err := renderShellIntegrationForPath(tt.mode, "/tmp/workspace-launcher", true)
		if err != nil {
			t.Fatalf("renderShellIntegration(%s) returned error: %v", tt.mode, err)
		}
		if !strings.Contains(script, tt.want) {
			t.Fatalf("expected %s integration to include bindings in %q", tt.mode, script)
		}
	}
}

func TestParseConfigRejectsBindingsWithoutShellMode(t *testing.T) {
	_, err := parseConfig([]string{"--bindings"})
	if err == nil || err.Error() != "--bindings can only be used with --bash, --zsh, or --fish" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseConfigAcceptsBindingsWithShellMode(t *testing.T) {
	cfg, err := parseConfig([]string{"--zsh", "--bindings"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if cfg.mode != modeZsh {
		t.Fatalf("unexpected mode: got %q want %q", cfg.mode, modeZsh)
	}
	if !cfg.shellBindings {
		t.Fatal("expected shellBindings to be enabled")
	}
}

func makeDir(t *testing.T, dir string, epoch int64, marker string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if marker != "" {
		if err := os.WriteFile(filepath.Join(dir, marker), []byte("x"), 0o644); err != nil {
			t.Fatalf("write marker %s: %v", marker, err)
		}
	}
	setDirTime(t, dir, epoch)
}

func makeGitRepo(t *testing.T, dir string, epoch int64) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, dir, "add", ".")
	env := append(os.Environ(),
		"GIT_AUTHOR_DATE="+strconv.FormatInt(epoch, 10)+" +0000",
		"GIT_COMMITTER_DATE="+strconv.FormatInt(epoch, 10)+" +0000",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=commit.gpgsign",
		"GIT_CONFIG_VALUE_0=false",
	)
	cmd := exec.Command("git", "-C", dir, "commit", "-q", "-m", "init")
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit in %s failed: %v\n%s", dir, err, output)
	}
	setDirTime(t, dir, epoch-100)
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "test")
	runGit(t, repo, "config", "user.email", "test@example.com")
	return repo
}

func commitAt(t *testing.T, repo, epoch, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, repo, "add", ".")
	env := append(os.Environ(),
		"GIT_AUTHOR_DATE="+epoch+" +0000",
		"GIT_COMMITTER_DATE="+epoch+" +0000",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=commit.gpgsign",
		"GIT_CONFIG_VALUE_0=false",
	)
	cmd := exec.Command("git", "-C", repo, "commit", "-q", "-m", contents)
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit in %s failed: %v\n%s", repo, err, output)
	}
}

func currentBranchName(t *testing.T, repo string) string {
	t.Helper()
	branch := strings.TrimSpace(runGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	if branch == "" || branch == "HEAD" {
		t.Fatalf("unexpected branch name: %q", branch)
	}
	return branch
}

func setDirTime(t *testing.T, dir string, epoch int64) {
	t.Helper()
	ts := time.Unix(epoch, 0)
	if err := os.Chtimes(dir, ts, ts); err != nil {
		t.Fatalf("chtimes %s: %v", dir, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=commit.gpgsign",
		"GIT_CONFIG_VALUE_0=false",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, output)
	}
	return string(output)
}
