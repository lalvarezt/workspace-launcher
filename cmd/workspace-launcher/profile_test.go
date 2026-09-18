package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryPathsThroughSymlinkedRootAndWorkspace(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	repo := filepath.Join(root, "repo")
	makeDir(t, repo, 1700000000, "go.mod")
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	rootAlias := filepath.Join(base, "root-alias")
	if err := os.Symlink(root, rootAlias); err != nil {
		t.Fatal(err)
	}
	for _, scanRoot := range []string{root, rootAlias} {
		cfg := benchmarkConfig(scanRoot, 8, recencyOpened)
		cfg.state = workspaceState{canonicalWorkspacePath(repo): {Pinned: true, LastOpened: 42}}
		candidates, err := buildCandidates(cfg)
		if err != nil || len(candidates) != 2 {
			t.Fatalf("scan: count=%d, error=%v", len(candidates), err)
		}
		for _, c := range candidates {
			if !c.pinned || c.opened != 42 {
				t.Fatalf("history missing for %s: %+v", c.path, c)
			}
			cfg.state[canonicalWorkspacePath(repo)] = workspaceEntry{LastOpened: 84}
			rendered := renderCandidate(cfg, c.detail)
			if rendered.pinned || rendered.opened != 84 {
				t.Fatalf("rerender used stale history: %+v", rendered)
			}
			cfg.state[canonicalWorkspacePath(repo)] = workspaceEntry{Pinned: true, LastOpened: 42}
		}
	}
}

func TestCollectDirFactsGitEntryTypes(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			gitDir := t.TempDir()
			gitPath := filepath.Join(dir, ".git")
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(gitPath, 0o700)
			case "file":
				err = os.WriteFile(gitPath, []byte("gitdir: "+gitDir+"\n"), 0o600)
			case "symlink":
				err = os.Symlink(gitDir, gitPath)
			}
			if err != nil {
				t.Fatal(err)
			}
			facts, err := collectDirFacts(dir, true, false)
			if err != nil || !facts.hasGit || facts.gitIsDir != (kind == "directory") {
				t.Fatalf("facts=%+v, error=%v", facts, err)
			}
			if kind == "file" && facts.gitDir != gitDir {
				t.Fatalf("gitDir=%q, want %q", facts.gitDir, gitDir)
			}
		})
	}
}

func TestCollectDirFactsAcrossBatches(t *testing.T) {
	dir := t.TempDir()
	for i := range 128 {
		if err := os.WriteFile(filepath.Join(dir, strings.Repeat("x", i+1)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	// No language marker forces the scan through every batch and EOF.
	facts, err := collectDirFacts(dir, true, true)
	if err != nil || !facts.hasGit || !facts.gitIsDir || detectLanguage(facts) != "-" {
		t.Fatalf("facts=%+v, error=%v", facts, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	facts, err = collectDirFacts(dir, true, true)
	if err != nil || !facts.hasGit || detectLanguage(facts) != "Go" {
		t.Fatalf("facts=%+v, error=%v", facts, err)
	}
}

func TestSerializedCandidatesMatchesPickerProtocol(t *testing.T) {
	candidates := []candidate{
		{path: "/repos/with space", matchText: "with space", branchText: "main", display: "\x1b[31mred\x1b[0m\tGo"},
		{path: "/repos/large", matchText: "large", display: strings.Repeat("界", 100000)},
		{},
	}
	var want strings.Builder
	for i := range candidates {
		want.WriteString(serializeCandidate(&candidates[i]))
		want.WriteByte('\n')
	}
	var got bytes.Buffer
	if err := writeSerializedCandidates(&got, candidates); err != nil {
		t.Fatal(err)
	}
	if got.String() != want.String() {
		t.Fatal("serialized rows differ from picker protocol")
	}
	if err := writeSerializedCandidates(failingCandidateWriter{}, candidates); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write error=%v", err)
	}
}

type failingCandidateWriter struct{}

func (failingCandidateWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
