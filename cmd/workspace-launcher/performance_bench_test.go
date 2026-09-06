package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkPickerFlush(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("repos_%d", count), func(b *testing.B) {
			cfg := benchmarkConfig("/repos", 8, recencyGit)
			cfg.gitDirty = true
			details := make([]repoDetails, count)
			for i := range details {
				name := fmt.Sprintf("repo-%05d", i)
				details[i] = repoDetails{
					child: childDir{path: "/repos/" + name, name: name, root: "/repos"},
					git:   gitMeta{present: true, branchLabel: "main", dirtyStatus: dirtyStatusClean},
					lang:  "Go", matchText: name, epoch: 1700000300, ageText: "1h",
				}
			}
			for _, batch := range []int{1, 128, count} {
				b.Run(fmt.Sprintf("updates_%d", batch), func(b *testing.B) {
					r := pickerRuntime{
						cfg: cfg, candidates: renderCandidates(cfg, details), ctx: context.Background(),
						state: pickerState{listenSocket: servePickerActions(b), candidatesFile: filepath.Join(b.TempDir(), "candidates")},
					}
					if err := r.flushCandidates(); err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for n := 0; n < b.N; n++ {
						for j := range batch {
							r.applyDirtyUpdate(dirtyUpdate{path: r.candidates[(n*batch+j)%count].path, dirty: n%2 == 0, status: dirtyStatusDirty})
						}
						if err := r.flushCandidates(); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func BenchmarkBuildCandidates_PackedSharedWorktrees(b *testing.B) {
	root := b.TempDir()
	repo := initTestRepo(b)
	commitAt(b, repo, "1700000300", "packed-worktree")
	for i := range 32 {
		runGit(b, repo, "worktree", "add", "--detach", "-q", filepath.Join(root, fmt.Sprintf("worktree-%02d", i)))
	}
	runGit(b, repo, "repack", "-ad")
	runGit(b, repo, "prune-packed")
	if _, err := gitLastCommitEpochFast(repo); err != nil {
		b.Fatal(err)
	}
	for _, jobs := range []int{1, 8} {
		b.Run(fmt.Sprintf("jobs_%d", jobs), func(b *testing.B) {
			cfg := benchmarkConfig(root, jobs, recencyGit)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				candidates, err := buildCandidates(cfg)
				if err != nil || len(candidates) != 32 {
					b.Fatalf("scan: count=%d, error=%v", len(candidates), err)
				}
				for _, c := range candidates {
					if c.epoch != 1700000300 {
						b.Fatalf("epoch=%d", c.epoch)
					}
				}
				benchCandidatesSink = candidates
			}
		})
	}
}

func BenchmarkDirtyUpdateBatch(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("repos_%d", count), func(b *testing.B) {
			candidates := make([]candidate, count)
			for i := range candidates {
				candidates[i] = candidate{
					path:   fmt.Sprintf("/repos/repo-%05d", i),
					detail: &repoDetails{git: gitMeta{present: true}},
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				r := pickerRuntime{candidates: candidates}
				for _, c := range candidates {
					if !r.applyDirtyUpdate(dirtyUpdate{path: c.path, dirty: n%2 == 0, status: dirtyStatusDirty}) {
						b.Fatal("update rejected")
					}
				}
			}
		})
	}
}

func BenchmarkCollectDirFacts_NoMarkers(b *testing.B) {
	for _, count := range []int{16, 10000} {
		b.Run(fmt.Sprintf("entries_%d", count), func(b *testing.B) {
			dir := b.TempDir()
			for i := range count {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%05d", i)), nil, 0o600); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				if _, err := collectDirFacts(dir, true, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
