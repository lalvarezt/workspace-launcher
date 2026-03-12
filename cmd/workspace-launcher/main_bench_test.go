package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

var (
	benchCandidatesSink []candidate
	benchGitMetaSink    gitMeta
	benchPickerSink     pickerResult
)

func BenchmarkBuildCandidates_Mtime(b *testing.B) {
	for _, repoCount := range []int{100, 1000} {
		repoCount := repoCount
		b.Run(fmt.Sprintf("repos_%d", repoCount), func(b *testing.B) {
			root := b.TempDir()
			createBenchWorkspaceRoot(b, root, repoCount, false)
			for _, jobs := range benchmarkJobCounts() {
				jobs := jobs
				b.Run(fmt.Sprintf("jobs_%d", jobs), func(b *testing.B) {
					cfg := benchmarkConfig(root, jobs, recencyMtime)
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						cands, err := buildCandidates(cfg)
						if err != nil {
							b.Fatalf("buildCandidates returned error: %v", err)
						}
						benchCandidatesSink = cands
					}
				})
			}
		})
	}
}

func BenchmarkBuildCandidates_GitRecency(b *testing.B) {
	for _, repoCount := range []int{100, 1000} {
		repoCount := repoCount
		b.Run(fmt.Sprintf("repos_%d", repoCount), func(b *testing.B) {
			root := b.TempDir()
			createBenchWorkspaceRoot(b, root, repoCount, true)
			for _, jobs := range benchmarkJobCounts() {
				jobs := jobs
				b.Run(fmt.Sprintf("jobs_%d", jobs), func(b *testing.B) {
					cfg := benchmarkConfig(root, jobs, recencyGit)
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						cands, err := buildCandidates(cfg)
						if err != nil {
							b.Fatalf("buildCandidates returned error: %v", err)
						}
						benchCandidatesSink = cands
					}
				})
			}
		})
	}
}

func BenchmarkInspectGitMeta_RegularRepo(b *testing.B) {
	repo := initTestRepo(b)
	commitAt(b, repo, "1700000000", "regular")

	meta := inspectGitMeta(repo, true, true, true, false)
	if !meta.present || meta.epoch == 0 {
		b.Fatalf("unexpected git metadata: %+v", meta)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchGitMetaSink = inspectGitMeta(repo, true, true, true, false)
	}
}

func BenchmarkInspectGitMeta_PackedRefs(b *testing.B) {
	repo := initTestRepo(b)
	commitAt(b, repo, "1700000100", "packed")
	runGit(b, repo, "pack-refs", "--all")

	refPath := filepath.Join(repo, ".git", "refs", "heads", currentBranchName(b, repo))
	if err := os.Remove(refPath); err != nil && !os.IsNotExist(err) {
		b.Fatalf("remove loose ref: %v", err)
	}

	meta := inspectGitMeta(repo, true, true, true, false)
	if !meta.present || meta.epoch == 0 {
		b.Fatalf("unexpected git metadata: %+v", meta)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchGitMetaSink = inspectGitMeta(repo, true, true, true, false)
	}
}

func BenchmarkInspectGitMeta_Worktree(b *testing.B) {
	repo := initTestRepo(b)
	commitAt(b, repo, "1700000200", "worktree")

	worktree := filepath.Join(b.TempDir(), "wt")
	runGit(b, repo, "worktree", "add", worktree)

	meta := inspectGitMeta(worktree, false, true, true, false)
	if !meta.present || !meta.isWorktree || meta.epoch == 0 {
		b.Fatalf("unexpected git metadata: %+v", meta)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchGitMetaSink = inspectGitMeta(worktree, false, true, true, false)
	}
}

func BenchmarkPickRepoHeadless_EmptyQuery(b *testing.B) {
	for _, candCount := range []int{100, 1000} {
		candCount := candCount
		b.Run(fmt.Sprintf("cands_%d", candCount), func(b *testing.B) {
			cfg := config{headlessBench: true, roots: []string{"/tmp/workspaces"}}
			cands := makeBenchCandidates(candCount)

			got, err := pickRepoHeadless(cfg, cands)
			if err != nil {
				b.Fatalf("pickRepoHeadless returned error: %v", err)
			}
			if got.selection == "" {
				b.Fatal("expected a selection")
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := pickRepoHeadless(cfg, cands)
				if err != nil {
					b.Fatalf("pickRepoHeadless returned error: %v", err)
				}
				benchPickerSink = result
			}
		})
	}
}

func BenchmarkPickRepoHeadless_QueryMatchEarly(b *testing.B) {
	for _, candCount := range []int{100, 1000} {
		candCount := candCount
		b.Run(fmt.Sprintf("cands_%d", candCount), func(b *testing.B) {
			cands := makeBenchCandidates(candCount)
			cands[0].matchText = "needle-early"
			cands[0].display = "needle-early"
			cands[0].searchText = buildCandidateSearchText(cands[0].matchText, cands[0].branchText)
			cfg := config{
				headlessBench: true,
				initialQuery:  "needle-early",
				roots:         []string{"/tmp/workspaces"},
			}

			got, err := pickRepoHeadless(cfg, cands)
			if err != nil {
				b.Fatalf("pickRepoHeadless returned error: %v", err)
			}
			if got.selection == "" {
				b.Fatal("expected a selection")
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := pickRepoHeadless(cfg, cands)
				if err != nil {
					b.Fatalf("pickRepoHeadless returned error: %v", err)
				}
				benchPickerSink = result
			}
		})
	}
}

func BenchmarkPickRepoHeadless_QueryMatchLate(b *testing.B) {
	for _, candCount := range []int{100, 1000} {
		candCount := candCount
		b.Run(fmt.Sprintf("cands_%d", candCount), func(b *testing.B) {
			cands := makeBenchCandidates(candCount)
			last := len(cands) - 1
			cands[last].branchText = "feature/needle-late"
			cands[last].searchText = buildCandidateSearchText(cands[last].matchText, cands[last].branchText)
			cfg := config{
				headlessBench: true,
				initialQuery:  "needle-late",
				roots:         []string{"/tmp/workspaces"},
			}

			got, err := pickRepoHeadless(cfg, cands)
			if err != nil {
				b.Fatalf("pickRepoHeadless returned error: %v", err)
			}
			if got.selection == "" {
				b.Fatal("expected a selection")
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := pickRepoHeadless(cfg, cands)
				if err != nil {
					b.Fatalf("pickRepoHeadless returned error: %v", err)
				}
				benchPickerSink = result
			}
		})
	}
}

func benchmarkJobCounts() []int {
	maxJobs := runtime.NumCPU()
	if maxJobs < 1 {
		maxJobs = 1
	}
	if maxJobs == 1 {
		return []int{1}
	}
	return []int{1, maxJobs}
}

func benchmarkConfig(root string, jobs int, recency string) config {
	return config{
		mode:         modePath,
		fzfStyle:     fzfStylePlain,
		roots:        []string{root},
		jobs:         jobs,
		recency:      recency,
		showLanguage: true,
		showGit:      true,
		now:          1700005000,
		cols:         120,
		nameWidth:    32,
	}
}

func createBenchWorkspaceRoot(b testing.TB, root string, repoCount int, withGit bool) {
	b.Helper()
	for i := 0; i < repoCount; i++ {
		dir := filepath.Join(root, fmt.Sprintf("repo-%04d", i))
		epoch := int64(1700000000 + i)
		if withGit {
			makeBenchGitRepo(b, dir, epoch, benchmarkMarkerFile(i))
			continue
		}
		makeDir(b, dir, epoch, benchmarkMarkerFile(i))
	}
}

func makeBenchGitRepo(b testing.TB, dir string, epoch int64, marker string) {
	b.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatalf("mkdir %s: %v", dir, err)
	}
	runGit(b, dir, "init")
	runGit(b, dir, "config", "user.name", "test")
	runGit(b, dir, "config", "user.email", "test@example.com")
	if marker != "" {
		if err := os.WriteFile(filepath.Join(dir, marker), []byte("x"), 0o644); err != nil {
			b.Fatalf("write marker %s: %v", marker, err)
		}
	}
	commitAt(b, dir, fmt.Sprintf("%d", epoch), fmt.Sprintf("commit-%d", epoch))
	setDirTime(b, dir, epoch-100)
}

func benchmarkMarkerFile(index int) string {
	markers := []string{
		"go.mod",
		"Cargo.toml",
		"package.json",
		"pyproject.toml",
		"init.lua",
		"Gemfile",
		"flake.nix",
	}
	return markers[index%len(markers)]
}

func makeBenchCandidates(count int) []candidate {
	cands := make([]candidate, count)
	for i := range count {
		name := fmt.Sprintf("repo-%04d", i)
		branch := fmt.Sprintf("feature/%s", name)
		cands[i] = candidate{
			path:       filepath.Join("/tmp/workspaces", name),
			display:    name,
			matchText:  name,
			branchText: branch,
			searchText: buildCandidateSearchText(name, branch),
		}
	}
	return cands
}
