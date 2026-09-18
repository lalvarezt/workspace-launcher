package main

import (
	"fmt"
	"path/filepath"
	"testing"
)

func BenchmarkBuildCandidates_WithHistory(b *testing.B) {
	root := b.TempDir()
	createBenchWorkspaceRoot(b, root, 1000, false)
	cfg := benchmarkConfig(root, 8, recencyOpened)
	cfg.state = workspaceState{canonicalWorkspacePath(filepath.Join(root, "repo-0000")): {Pinned: true}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var err error
		benchCandidatesSink, err = buildCandidates(cfg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderCandidates_WithHistory(b *testing.B) {
	root := b.TempDir()
	createBenchWorkspaceRoot(b, root, 1000, false)
	cfg := benchmarkConfig(root, 8, recencyOpened)
	cfg.state = workspaceState{canonicalWorkspacePath(filepath.Join(root, "repo-0000")): {Pinned: true}}
	candidates, err := buildCandidates(cfg)
	if err != nil {
		b.Fatal(err)
	}
	details := make([]repoDetails, len(candidates))
	for i := range candidates {
		details[i] = *candidates[i].detail
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		benchCandidatesSink = renderCandidates(cfg, details)
	}
}

func BenchmarkCollectDirFacts_GitOnly(b *testing.B) {
	for _, count := range []int{16, 10000} {
		b.Run(fmt.Sprintf("entries_%d", count), func(b *testing.B) {
			dir := b.TempDir()
			for i := range count {
				makeDir(b, filepath.Join(dir, fmt.Sprintf("dir-%05d", i)), 1700000000, "")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := collectDirFacts(dir, true, false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
