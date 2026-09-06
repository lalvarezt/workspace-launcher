package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestPackedWorktreesShareGitFallback(t *testing.T) {
	repo := initTestRepo(t)
	commitAt(t, repo, "1700000300", "packed")
	root := t.TempDir()
	var worktrees []string
	for _, name := range []string{"a", "b", "c", "d"} {
		worktree := filepath.Join(root, name)
		runGit(t, repo, "worktree", "add", "--detach", "-q", worktree)
		worktrees = append(worktrees, worktree)
	}
	runGit(t, repo, "repack", "-ad")
	runGit(t, repo, "prune-packed")
	layout, err := resolveGitLayout(worktrees[0])
	if err != nil {
		t.Fatal(err)
	}
	head, err := readHeadFile(layout.gitDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readCommitEpoch(layout, head); !os.IsNotExist(err) {
		t.Fatalf("expected packed-only commit, got %v", err)
	}

	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := "#!/bin/sh\nprintf 'call\\n' >> " + shellSingleQuote(calls) + "\nexec " + shellSingleQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cache := &gitEpochCache{}
	var wg sync.WaitGroup
	for _, worktree := range worktrees {
		wg.Go(func() {
			meta := inspectGitMetaWithCache(worktree, false, true, true, false, cache)
			if meta.epoch != 1700000300 {
				t.Errorf("epoch=%d", meta.epoch)
			}
		})
	}
	wg.Wait()
	content, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(content, []byte("call\n")); got != 1 {
		t.Fatalf("Git subprocesses=%d, want 1", got)
	}
}

func TestPackedEpochUsesRequestedCommitAndRetriesFailures(t *testing.T) {
	repo := initTestRepo(t)
	commitAt(t, repo, "1700000300", "first")
	layout, err := resolveGitLayout(repo)
	if err != nil {
		t.Fatal(err)
	}
	head, err := readHeadFile(layout.gitDir)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := resolveHeadHashFromHead(layout, head)
	if err != nil {
		t.Fatal(err)
	}
	commitAt(t, repo, "1700000400", "second")
	runGit(t, repo, "repack", "-ad")
	runGit(t, repo, "prune-packed")
	cache := &gitEpochCache{}
	missing := t.TempDir()
	if _, err := readCommitEpochWithCache(missing, gitLayout{gitDir: missing, commonDir: missing}, hash, cache); err == nil {
		t.Fatal("expected unavailable object to fail")
	}
	epoch, err := readCommitEpochWithCache(repo, layout, hash, cache)
	if err != nil || epoch != 1700000300 {
		t.Fatalf("epoch=%d, error=%v, want original commit timestamp", epoch, err)
	}
}

func servePickerActions(t testing.TB) string {
	t.Helper()
	// Keep the socket pathname below the Unix socket length limit.
	dir, err := os.MkdirTemp("", "picker-actions-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		<-done
	})
	return socket
}

func TestDirtyUpdateIndexSurvivesFlushAndRefresh(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "init")
	}
	cfg := benchmarkConfig(root, 1, recencyMtime)
	candidates, err := buildCandidates(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := pickerRuntime{
		cfg: cfg, candidates: candidates, ctx: context.Background(), activeRoot: root,
		state: pickerState{listenSocket: servePickerActions(t), candidatesFile: filepath.Join(t.TempDir(), "candidates")},
	}
	b := filepath.Join(root, "b")
	if !r.applyDirtyUpdate(dirtyUpdate{path: b, dirty: true, status: dirtyStatusDirty}) {
		t.Fatal("initial update rejected")
	}
	if err := r.flushCandidates(); err != nil {
		t.Fatal(err)
	}
	if !r.applyDirtyUpdate(dirtyUpdate{path: b, status: dirtyStatusClean}) {
		t.Fatal("update after flush rejected")
	}
	if err := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "c")); err != nil {
		t.Fatal(err)
	}
	for _, activeRoot := range []string{root, activeRootAll} {
		r.activeRoot = activeRoot
		r.refreshActiveRoot(make(chan dirtyUpdate, 1), make(chan dirtyBatchDone, 1))
		if r.applyDirtyUpdate(dirtyUpdate{path: filepath.Join(root, "a")}) {
			t.Fatal("removed candidate still indexed")
		}
		if !r.applyDirtyUpdate(dirtyUpdate{path: b, dirty: true, status: dirtyStatusDirty}) {
			t.Fatal("update after refresh rejected")
		}
		for _, c := range r.candidates {
			if c.detail.git.dirty != (c.path == b) {
				t.Fatalf("wrong candidate updated: %s", c.path)
			}
		}
	}
}

func TestDirtyUpdateIndexPreservesFirstMatch(t *testing.T) {
	r := pickerRuntime{candidates: []candidate{
		{path: "duplicate", detail: &repoDetails{git: gitMeta{present: true}}},
		{path: "duplicate", detail: &repoDetails{git: gitMeta{present: true}}},
		{path: "missing-detail"},
		{path: "not-git", detail: &repoDetails{}},
	}}
	if !r.applyDirtyUpdate(dirtyUpdate{path: "duplicate", dirty: true}) || !r.candidates[0].detail.git.dirty || r.candidates[1].detail.git.dirty {
		t.Fatal("duplicate paths must update only the first match")
	}
	for _, path := range strings.Fields("unknown missing-detail not-git") {
		if r.applyDirtyUpdate(dirtyUpdate{path: path}) {
			t.Fatalf("unexpected update for %s", path)
		}
	}
}

func TestIncrementalPickerFlushMatchesFullRender(t *testing.T) {
	socket := servePickerActions(t)
	for _, style := range []string{fzfStylePlain, fzfStyleFull} {
		for _, cols := range []int{40, 120} {
			cfg := benchmarkConfig("/repos", 1, recencyGit)
			cfg.fzfStyle, cfg.cols = style, cols
			cfg.gitDirty, cfg.deferGitDirty = true, true
			details := []repoDetails{
				{child: childDir{path: "/repos/a", name: "a"}, git: gitMeta{present: true, branchLabel: "main"}, lang: "Go"},
				{child: childDir{path: "/repos/b", name: "longer-name"}, git: gitMeta{present: true, branchLabel: "feature/longer-branch", isWorktree: true}, lang: "Rust"},
			}
			r := pickerRuntime{
				cfg: cfg, candidates: renderCandidates(cfg, details), ctx: context.Background(),
				state: pickerState{listenSocket: socket, candidatesFile: filepath.Join(t.TempDir(), "candidates")},
			}
			check := func() {
				t.Helper()
				wantDetails := make([]repoDetails, len(r.candidates))
				for i, c := range r.candidates {
					wantDetails[i] = *c.detail
				}
				want := renderCandidates(cfg, wantDetails)
				if err := r.flushCandidates(); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(r.candidates, want) {
					t.Fatalf("incremental render differs from full render: style=%s cols=%d", style, cols)
				}
			}
			check()
			for _, status := range []dirtyStatus{dirtyStatusClean, dirtyStatusDirty, dirtyStatusUnavailable} {
				r.applyDirtyUpdate(dirtyUpdate{path: "/repos/a", dirty: status == dirtyStatusDirty, status: status})
				check()
			}
			r.markPending(activeRootAll)
			check()
			// A layout change must invalidate even otherwise unchanged rows.
			r.candidates[1].detail.git.branchLabel = "x"
			r.markRowChanged(1)
			check()
		}
	}
}
