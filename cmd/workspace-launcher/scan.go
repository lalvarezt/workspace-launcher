package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

func buildCandidates(cfg config) ([]candidate, error) {
	children := make([]childDir, 0)
	for _, root := range cfg.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}

		children = slices.Grow(children, len(entries))
		for _, entry := range entries {
			path := filepath.Join(root, entry.Name())
			children = append(children, childDir{
				name:      entry.Name(),
				path:      path,
				root:      root,
				rootLabel: cfg.rootLabels[root],
				isDir:     entry.IsDir(),
			})
		}
	}
	if len(children) == 0 {
		return nil, nil
	}

	details := make([]repoDetails, len(children))
	needsInspect := cfg.showLanguage || cfg.showGit || cfg.recency == recencyGit
	var epochCache *gitEpochCache
	if cfg.recency == recencyGit {
		epochCache = &gitEpochCache{}
	}
	if cfg.jobs <= 1 || len(children) == 1 {
		for i, child := range children {
			detail, err := inspectRepoEntryWithCache(cfg, child, needsInspect, epochCache)
			if err != nil {
				return nil, err
			}
			details[i] = detail
		}
		details = compactRepoDetails(details)
		if len(details) == 0 {
			return nil, nil
		}
		return renderCandidates(cfg, details), nil
	}

	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error
	var next atomic.Uint64
	workerCount := min(cfg.jobs, len(children))

	for range workerCount {
		wg.Go(func() {
			for {
				idx := int(next.Add(1) - 1)
				if idx >= len(children) {
					return
				}
				detail, err := inspectRepoEntryWithCache(cfg, children[idx], needsInspect, epochCache)
				if err != nil {
					errOnce.Do(func() {
						firstErr = err
					})
					continue
				}
				details[idx] = detail
			}
		})
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	details = compactRepoDetails(details)
	if len(details) == 0 {
		return nil, nil
	}
	return renderCandidates(cfg, details), nil
}

func inspectRepoEntry(cfg config, child childDir, inspect bool) (repoDetails, error) {
	return inspectRepoEntryWithCache(cfg, child, inspect, nil)
}

func inspectRepoEntryWithCache(cfg config, child childDir, inspect bool, epochCache *gitEpochCache) (repoDetails, error) {
	if cfg.recency == recencyGit && child.isDir {
		detail, err := inspectRepoWithCache(cfg, child, inspect, epochCache)
		if errors.Is(err, os.ErrNotExist) {
			return repoDetails{}, nil
		}
		return detail, err
	}

	info, err := os.Stat(child.path)
	if err != nil || !info.IsDir() {
		return repoDetails{}, nil
	}
	child.modEpoch = info.ModTime().Unix()
	return inspectRepoWithCache(cfg, child, inspect, epochCache)
}

func compactRepoDetails(details []repoDetails) []repoDetails {
	out := details[:0]
	for _, detail := range details {
		if detail.child.path != "" {
			out = append(out, detail)
		}
	}
	return out
}

func inspectRepo(cfg config, child childDir, inspect bool) (repoDetails, error) {
	return inspectRepoWithCache(cfg, child, inspect, nil)
}

func inspectRepoWithCache(cfg config, child childDir, inspect bool, epochCache *gitEpochCache) (repoDetails, error) {
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
		wantDirty := cfg.gitDirty && cfg.showGit && !cfg.deferGitDirty
		git = inspectGitMetaWithKnownDir(child.path, facts.gitIsDir, facts.gitDir, cfg.showGit, cfg.recency == recencyGit, wantDirty, epochCache)
	}
	if cfg.recency == recencyGit && git.epoch > 0 {
		epoch = git.epoch
	} else if cfg.recency == recencyGit && child.isDir && child.modEpoch == 0 {
		if info, statErr := os.Stat(child.path); statErr == nil {
			epoch = info.ModTime().Unix()
		} else {
			return repoDetails{}, nil
		}
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
		ageText:   formatAge(cfg.now, epoch),
		epoch:     epoch,
	}, nil
}

func renderCandidates(cfg config, details []repoDetails) []candidate {
	cfg = candidateLayout(cfg, details)
	out := make([]candidate, len(details))
	for i := range details {
		out[i] = renderCandidate(cfg, &details[i])
	}
	return out
}

func candidateLayout(cfg config, details []repoDetails) config {
	cfg.nameWidth = computeNameColumnWidth(details)
	cfg.ageColumnWidth = computeAgeColumnWidth(cfg.now, details)
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
	return cfg
}

func renderCandidate(cfg config, detail *repoDetails) candidate {
	styled := effectiveFzfStyle(cfg.fzfStyle) != fzfStylePlain
	columns := visibleCandidateColumns(cfg)
	defaultMarkerField := paintFieldStyled(styled, cDim, " ")
	displayCapacity := candidateDisplayCapacity(cfg, len(columns), styled)
	git := detail.git
	if cfg.gitDirty && cfg.deferGitDirty && git.present && git.dirtyStatus == dirtyStatusUnset {
		git.dirtyStatus = dirtyStatusPending
	}

	branch := git.branchLabel
	if branch == "" {
		branch = "-"
	}
	branchText := branchSearchText(git.branchLabel)

	markerField := defaultMarkerField
	if isCurrentRepo(cfg.cwd, detail.child.path) {
		markerField = paintFieldStyled(styled, cCurrent, "*")
	}

	var display strings.Builder
	display.Grow(displayCapacity)
	var searchPartBuffer [3]string
	searchParts := searchPartBuffer[:0]
	for columnIndex, column := range columns {
		if columnIndex > 0 {
			display.WriteByte('\t')
		}
		switch column {
		case candidateColumnRoot:
			display.WriteString(paintFieldStyled(styled, cDim, fitField(detail.child.rootLabel, cfg.rootLabelWidth)))
			searchParts = append(searchParts, detail.child.rootLabel)
		case candidateColumnName:
			display.WriteString(markerField)
			display.WriteByte(' ')
			display.WriteString(paintFieldStyled(styled, cName, fitField(detail.child.name, cfg.nameWidth)))
			searchParts = append(searchParts, detail.matchText)
		case candidateColumnGit:
			display.WriteString(renderGitFieldStyled(git, branch, cfg.gitColumnWidth, styled))
			searchParts = append(searchParts, branchText)
		case candidateColumnLanguage:
			display.WriteString(renderLangFieldStyled(detail.lang, cfg.langColumnWidth, styled))
		case candidateColumnAge:
			display.WriteString(renderAgeFieldStyled(detail.ageText, cfg.ageColumnWidth, styled))
		}
		if columnIndex < len(columns)-1 && gapWidth > 1 {
			for range gapWidth - 1 {
				display.WriteByte(' ')
			}
		}
	}

	detail.git = git
	return candidate{
		path:       detail.child.path,
		rootText:   detail.child.rootLabel,
		display:    display.String(),
		matchText:  detail.matchText,
		branchText: branchText,
		searchText: buildCandidateSearchText(searchParts...),
		epoch:      detail.epoch,
		detail:     detail,
	}
}

func candidateDisplayCapacity(cfg config, columnCount int, styled bool) int {
	capacity := cfg.nameWidth + 2 + cfg.ageColumnWidth + 16
	if cfg.showRoot {
		capacity += cfg.rootLabelWidth
	}
	if cfg.showGit {
		capacity += cfg.gitColumnWidth
	}
	if cfg.showLanguage {
		capacity += cfg.langColumnWidth
	}
	if columnCount > 1 {
		capacity += (columnCount - 1) * gapWidth
	}
	if styled {
		capacity += 96
	}
	return capacity
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
				if !facts.gitIsDir {
					if content, readErr := os.ReadFile(filepath.Join(dir, ".git")); readErr == nil {
						if gitDir, _, parseErr := parseGitDirFile(dir, content); parseErr == nil {
							facts.gitDir = gitDir
						}
					}
				}
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
	depth = min(depth, len(info.parts))
	start := max(len(info.parts)-depth, 0)
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

func computeGitColumnWidth(details []repoDetails) int {
	longest := gitMinWidth
	for _, detail := range details {
		width := gitFieldDisplayWidth(detail.git, detail.git.branchLabel)
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
