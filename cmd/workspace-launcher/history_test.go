package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWorkspaceHistoryConcurrentUpdates(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if err := updateWorkspaceState(func(state workspaceState) { state[fmt.Sprint(i)] = workspaceEntry{LastOpened: int64(i + 1)} }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	state, err := loadWorkspaceState()
	if err != nil || len(state) != 20 {
		t.Fatalf("state entries=%d, err=%v", len(state), err)
	}
}

func TestWorkspaceHistoryPinsVisitsAndClear(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if err := manageWorkspaceState(config{stateAction: "--pin", stateTarget: alias}); err != nil {
		t.Fatal(err)
	}
	recordWorkspaceVisit(alias, 20)
	recordWorkspaceVisit(root, 10)
	state, err := loadWorkspaceState()
	if err != nil || len(state) != 1 || !state[root].Pinned || state[root].LastOpened != 20 {
		t.Fatalf("state=%v err=%v", state, err)
	}
	if err := manageWorkspaceState(config{stateAction: "--clear-history"}); err != nil {
		t.Fatal(err)
	}
	state, _ = loadWorkspaceState()
	if !state[root].Pinned || state[root].LastOpened != 0 {
		t.Fatalf("clear lost pin: %v", state)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := manageWorkspaceState(config{stateAction: "--unpin", stateTarget: root}); err != nil {
		t.Fatal(err)
	}
	state, _ = loadWorkspaceState()
	if len(state) != 0 {
		t.Fatalf("unpin deleted directory: %v", state)
	}
}

func TestWorkspaceHistoryCorruptionPreserved(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path, _ := workspaceStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := updateWorkspaceState(func(s workspaceState) { s["x"] = workspaceEntry{} }); err == nil {
		t.Fatal("expected corrupt state error")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "broken" {
		t.Fatal("corrupt file overwritten")
	}
}

func TestWorkspaceHistorySort(t *testing.T) {
	items := []candidate{{path: "new", epoch: 100}, {path: "visited", opened: 10}, {path: "z-pin", pinned: true, opened: 30}, {path: "a-pin", pinned: true}, {path: "older", epoch: 1}}
	sortCandidates(items)
	for i, want := range []string{"a-pin", "z-pin", "visited", "new", "older"} {
		if items[i].path != want {
			t.Fatalf("position %d = %s, want %s", i, items[i].path, want)
		}
	}
}

func TestWorkspaceHistoryRecencyModes(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"visited", "pinned", "unvisited"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{recencyMtime, recencyGit, recencyOpened} {
		cfg := config{roots: []string{root}, jobs: 1, recency: mode, cols: 120, state: workspaceState{
			filepath.Join(root, "visited"): {LastOpened: 100},
			filepath.Join(root, "pinned"):  {Pinned: true},
		}}
		candidates, err := buildCandidates(cfg)
		if err != nil {
			t.Fatal(err)
		}
		sortCandidates(candidates)
		if filepath.Base(candidates[0].path) != "pinned" {
			t.Fatalf("%s lost pinned priority", mode)
		}
		for _, c := range candidates {
			if mode != recencyOpened && c.opened != 0 {
				t.Fatalf("history changed %s sorting", mode)
			}
		}
		if mode == recencyOpened && filepath.Base(candidates[1].path) != "visited" {
			t.Fatal("visit did not rise above unvisited directory")
		}
	}
}

func TestWorkspaceHistoryConfig(t *testing.T) {
	t.Setenv("WORKSPACE_LAUNCHER_ROOT", filepath.Join(t.TempDir(), "missing"))
	for _, args := range [][]string{{"--clear-history"}, {"--pin", "/example"}, {"--unpin", "/deleted"}} {
		if _, err := parseConfig(args); err != nil {
			t.Fatalf("state command needs scan roots: %v", err)
		}
	}
	for _, args := range [][]string{{"--pin"}, {"--recency", "bad"}, {"--clear-history", "--bash"}, {"--pin", "/a", "--clear-history"}} {
		if _, err := parseConfig(args); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
	t.Setenv("WORKSPACE_LAUNCHER_RECENCY", "git")
	cfg, err := parseConfig([]string{"--recency=opened", t.TempDir()})
	if err != nil || cfg.recency != recencyOpened {
		t.Fatalf("recency override: %s %v", cfg.recency, err)
	}
}
