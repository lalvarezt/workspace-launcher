package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func buildCandidates(cfg config) ([]candidate, error) {
	children := make([]childDir, 0)
	for _, root := range cfg.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			path := filepath.Join(root, entry.Name())
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				continue
			}
			children = append(children, childDir{
				name:      entry.Name(),
				path:      path,
				root:      root,
				rootLabel: cfg.rootLabels[root],
				modEpoch:  info.ModTime().Unix(),
			})
		}
	}
	if len(children) == 0 {
		return nil, nil
	}

	details := make([]repoDetails, len(children))
	needsInspect := cfg.showLanguage || cfg.showGit || cfg.recency == recencyGit
	if !needsInspect || cfg.jobs <= 1 || len(children) == 1 {
		for i, child := range children {
			detail, err := inspectRepo(cfg, child, needsInspect)
			if err != nil {
				return nil, err
			}
			details[i] = detail
		}
		return renderCandidates(cfg, details), nil
	}

	type jobResult struct {
		index  int
		detail repoDetails
		err    error
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
				detail, err := inspectRepo(cfg, children[idx], needsInspect)
				out <- jobResult{index: idx, detail: detail, err: err}
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
		details[res.index] = res.detail
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return renderCandidates(cfg, details), nil
}

func inspectRepo(cfg config, child childDir, inspect bool) (repoDetails, error) {
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
		git = inspectGitMeta(child.path, facts.gitIsDir, cfg.showGit, cfg.recency == recencyGit, cfg.gitDirty && cfg.showGit)
	}
	if cfg.recency == recencyGit && git.epoch > 0 {
		epoch = git.epoch
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
		epoch:     epoch,
	}, nil
}

func renderCandidates(cfg config, details []repoDetails) []candidate {
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

	out := make([]candidate, len(details))
	styled := effectiveFzfStyle(cfg.fzfStyle) != fzfStylePlain
	for i, detail := range details {
		branch := detail.git.branchLabel
		if branch == "" {
			branch = "-"
		}

		markerField := paintFieldStyled(styled, cDim, " ")
		if isCurrentRepo(cfg.cwd, detail.child.path) {
			markerField = paintFieldStyled(styled, cCurrent, "*")
		}
		nameField := markerField + " " + paintFieldStyled(styled, cName, fitField(detail.child.name, cfg.nameWidth))
		ageField := renderAgeFieldStyled(formatAge(cfg.now, detail.epoch), cfg.ageColumnWidth, styled)

		fields := make([]string, 0, 4)
		if cfg.showRoot {
			fields = append(fields, paintFieldStyled(styled, cDim, fitField(detail.child.rootLabel, cfg.rootLabelWidth)))
		}
		fields = append(fields, nameField)
		if cfg.showLanguage {
			fields = append(fields, renderLangFieldStyled(detail.lang, cfg.langColumnWidth, styled))
		}
		if cfg.showGit {
			fields = append(fields, renderGitFieldStyled(detail.git, branch, cfg.gitColumnWidth, styled))
		}
		fields = append(fields, ageField)

		out[i] = candidate{
			path:       detail.child.path,
			display:    joinDisplayFields(fields),
			matchText:  detail.matchText,
			branchText: branchSearchText(detail.git.branchLabel),
			epoch:      detail.epoch,
		}
	}
	return out
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
	if depth > len(info.parts) {
		depth = len(info.parts)
	}
	start := len(info.parts) - depth
	if start < 0 {
		start = 0
	}
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
		width := displayWidth(gitFieldText(detail.git, detail.git.branchLabel))
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
