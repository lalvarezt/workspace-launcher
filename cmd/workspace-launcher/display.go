package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/text/width"
)

func applyLayoutWidths(cfg *config) {
	targetNameWidth := max(cfg.nameWidth, nameMinWidth)

	langColumnWidth := 0
	if cfg.showLanguage {
		langColumnWidth = cfg.langColumnWidth
		if langColumnWidth == 0 {
			langColumnWidth = langWidth
		}
	}

	gitColumnWidth := 0
	if cfg.showGit {
		gitColumnWidth = cfg.gitColumnWidth
		if gitColumnWidth == 0 {
			gitColumnWidth = gitMinWidth
		}
	}

	rootLabelWidth := 0
	if cfg.showRoot {
		rootLabelWidth = cfg.rootLabelWidth
	}

	ageColumnWidth := cfg.ageColumnWidth
	if ageColumnWidth == 0 {
		ageColumnWidth = ageWidth
	}
	ageTwoColumnWidth, ageOneColumnWidth := compactAgeColumnWidths(ageColumnWidth)

	reservedWidth := chromeWidth + ageColumnWidth
	if cfg.showLanguage {
		reservedWidth += langColumnWidth + gapWidth
	}
	if cfg.showGit {
		reservedWidth += gitColumnWidth + gapWidth
	}
	if cfg.showRoot {
		reservedWidth += rootLabelWidth + gapWidth
	}

	nameWidth := cfg.cols - reservedWidth
	deficit := targetNameWidth - nameWidth
	if deficit > 0 && cfg.showLanguage {
		shrink := deficit
		if maxShrink := langColumnWidth - langMinWidth; maxShrink < shrink {
			shrink = maxShrink
		}
		langColumnWidth -= shrink
		deficit -= shrink
	}
	if deficit > 0 {
		ageColumnWidth = shrinkAgeColumnWidth(ageColumnWidth, ageTwoColumnWidth, ageOneColumnWidth, deficit)
	}
	if deficit > 0 {
		reservedWidth = chromeWidth + ageColumnWidth
		if cfg.showLanguage {
			reservedWidth += langColumnWidth + gapWidth
		}
		if cfg.showGit {
			reservedWidth += gitColumnWidth + gapWidth
		}
		if cfg.showRoot {
			reservedWidth += rootLabelWidth + gapWidth
		}
		nameWidth = cfg.cols - reservedWidth
		deficit = targetNameWidth - nameWidth
	}
	if deficit > 0 && cfg.showGit {
		shrink := deficit
		if maxShrink := gitColumnWidth - gitMinWidth; maxShrink < shrink {
			shrink = maxShrink
		}
		gitColumnWidth -= shrink
		deficit -= shrink
	}
	if deficit > 0 && cfg.showRoot {
		shrink := deficit
		if maxShrink := rootLabelWidth - rootFloorWidth; maxShrink < shrink {
			shrink = maxShrink
		}
		rootLabelWidth -= shrink
	}

	reservedWidth = chromeWidth + ageColumnWidth
	if cfg.showLanguage {
		reservedWidth += langColumnWidth + gapWidth
	}
	if cfg.showGit {
		reservedWidth += gitColumnWidth + gapWidth
	}
	if cfg.showRoot {
		reservedWidth += rootLabelWidth + gapWidth
	}

	cfg.ageColumnWidth = ageColumnWidth
	cfg.langColumnWidth = langColumnWidth
	cfg.gitColumnWidth = gitColumnWidth
	cfg.rootLabelWidth = rootLabelWidth
	cfg.nameWidth = max(cfg.cols-reservedWidth, nameMinWidth)
}

func formatAge(now, epoch int64) string {
	diff := max(now-epoch, int64(0))
	days := diff / 86400
	hours := (diff % 86400) / 3600
	mins := (diff % 3600) / 60
	return fmt.Sprintf("%02dd %02dh %02dm", days, hours, mins)
}

func computeAgeColumnWidth(now int64, details []repoDetails) int {
	width := ageWidth
	for _, detail := range details {
		currentWidth := displayWidth(formatAge(now, detail.epoch)) + 1
		if currentWidth > width {
			width = currentWidth
		}
	}
	return width
}

func compactAgeColumnWidths(fullWidth int) (int, int) {
	fullWidth = max(fullWidth, ageWidth)
	twoBlockWidth := max(fullWidth-4, ageTwoWidth)
	oneBlockWidth := max(fullWidth-8, ageOneWidth)

	return twoBlockWidth, oneBlockWidth
}

func shrinkAgeColumnWidth(width, twoBlockWidth, oneBlockWidth, deficit int) int {
	for deficit > 0 {
		var next int
		switch {
		case width > twoBlockWidth:
			next = twoBlockWidth
		case width > oneBlockWidth:
			next = oneBlockWidth
		default:
			return width
		}
		deficit -= width - next
		width = next
	}
	return width
}

func isCurrentRepo(cwd, dir string) bool {
	return cwd == dir || strings.HasPrefix(cwd, dir+string(filepath.Separator))
}

func fitField(text string, width int) string {
	if width <= 0 {
		return ""
	}
	visibleWidth := displayWidth(text)
	if visibleWidth <= width {
		return text + strings.Repeat(" ", width-visibleWidth)
	}
	if width <= 3 {
		return trimDisplayWidth(text, width)
	}
	trimmed := trimDisplayWidth(text, width-3) + "..."
	return trimmed + strings.Repeat(" ", width-displayWidth(trimmed))
}

func fitFieldRight(text string, width int) string {
	if width <= 0 {
		return ""
	}
	visibleWidth := displayWidth(text)
	if visibleWidth <= width {
		return strings.Repeat(" ", width-visibleWidth) + text
	}
	return fitField(text, width)
}

func centerField(text string, width int) string {
	if width <= 0 {
		return ""
	}

	fitted := fitField(text, width)
	trimmed := strings.TrimRight(fitted, " ")
	visibleWidth := displayWidth(trimmed)
	if visibleWidth >= width {
		return fitted
	}

	leftPad := (width - visibleWidth) / 2
	rightPad := width - visibleWidth - leftPad
	return strings.Repeat(" ", leftPad) + trimmed + strings.Repeat(" ", rightPad)
}

func joinDisplayFields(fields []string) string {
	if len(fields) == 0 {
		return ""
	}

	padded := make([]string, len(fields))
	for i, field := range fields {
		padded[i] = field
		if i < len(fields)-1 && gapWidth > 1 {
			padded[i] += strings.Repeat(" ", gapWidth-1)
		}
	}

	return strings.Join(padded, "\t")
}

func displayWidth(text string) int {
	width := 0
	for _, r := range text {
		width += runeDisplayWidth(r)
	}
	return width
}

func trimDisplayWidth(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	width := 0
	var b strings.Builder
	for _, r := range text {
		rw := runeDisplayWidth(r)
		if width+rw > maxWidth {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String()
}

func runeDisplayWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return 0
	case r <= unicode.MaxASCII:
		return 1
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	case unicode.In(r, unicode.Co):
		return 1
	default:
		kind := width.LookupRune(r).Kind()
		if kind == width.EastAsianWide || kind == width.EastAsianFullwidth {
			return 2
		}
		return 1
	}
}

func paintFieldStyled(styled bool, color, text string) string {
	if !styled {
		return text
	}
	return color + text + cReset
}

func renderFooterRootBadge(cfg config, root string) string {
	label := pickerRootLabel(cfg, root)
	width := computeFooterRootWidth(cfg.rootLabels)
	if width == 0 {
		width = footerRootMaxWidth
	}
	text := " " + centerField(label, width) + " "
	if effectiveFzfStyle(cfg.fzfStyle) == fzfStylePlain {
		return text
	}
	return cRootText + cRootBadge + text + cReset
}

func computeFooterRootWidth(labels map[string]string) int {
	longest := displayWidth(activeRootAllLabel)
	for _, label := range labels {
		width := displayWidth(label)
		if width > longest {
			longest = width
		}
	}
	if longest > footerRootMaxWidth {
		return footerRootMaxWidth
	}
	return longest
}

func renderAgeFieldStyled(age string, width int, styled bool) string {
	if width <= 0 {
		return ""
	}

	render := func(text string, trailingPad bool) string {
		if width == 1 {
			return paintFieldStyled(styled, cTime, fitFieldRight(text, width))
		}
		if trailingPad {
			return paintFieldStyled(styled, cTime, fitFieldRight(text, width-1)+" ")
		}
		return paintFieldStyled(styled, cTime, fitFieldRight(text, width))
	}

	blocks := strings.Fields(age)
	candidates := []string{age}
	if len(blocks) >= 2 {
		candidates = append(candidates, strings.Join(blocks[:2], " "))
	}
	if len(blocks) >= 1 {
		candidates = append(candidates, blocks[0])
	}

	for _, candidate := range candidates {
		if displayWidth(candidate)+1 <= width {
			return render(candidate, true)
		}
		if displayWidth(candidate) <= width {
			return render(candidate, false)
		}
	}

	if len(blocks) == 0 {
		return render(age, false)
	}
	return render(blocks[0], false)
}

func renderLangFieldStyled(lang string, width int, styled bool) string {
	icon := "•"
	label := "Misc"
	color := cMisc

	switch lang {
	case "Go":
		icon, label, color = "", "Go", cGo
	case "Rust":
		icon, label, color = "", "Rust", cRust
	case "Python":
		icon, label, color = "", "Python", cPython
	case "Node":
		icon, label, color = "", "Node", cNode
	case "Lua":
		icon, label, color = "", "Lua", cLua
	case "Ruby":
		icon, label, color = "", "Ruby", cRuby
	case "Nix":
		icon, label, color = "", "Nix", cNix
	}

	iconCell := icon + "  "
	if icon == "•" {
		iconCell = "•  "
	}
	if width <= 0 {
		return ""
	}
	return paintFieldStyled(styled, color, fitField(iconCell+label, width))
}

func gitFieldText(meta gitMeta, branch string) string {
	if !meta.present {
		return "-"
	}

	icon := ""
	switch {
	case meta.isLocked:
		icon = ""
	case meta.isWorktree:
		icon = "󰙅"
	case meta.isSubmodule:
		icon = ""
	}

	text := icon
	if branch != "" && branch != "-" {
		text += "  " + branch
	}
	return text
}

func renderGitFieldStyled(meta gitMeta, branch string, width int, styled bool) string {
	if !meta.present {
		return paintFieldStyled(styled, cDim, fitField("-", width))
	}

	color := cGit
	switch {
	case meta.isLocked:
		color = cGitLock
	case meta.isWorktree:
		color = cWorktree
	case meta.isSubmodule:
		color = cSubmodule
	}
	if meta.dirty {
		color = cGitDirty
	}

	return paintFieldStyled(styled, color, fitField(gitFieldText(meta, branch), width))
}
