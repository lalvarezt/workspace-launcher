package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func pickRepo(cfg config, fzfPath string, candidates []candidate) (pickerResult, error) {
	if cfg.headlessBench {
		return pickRepoHeadless(cfg, candidates)
	}

	state, err := createPickerState(cfg, candidates)
	if err != nil {
		return pickerResult{}, err
	}
	if state.dir != "" {
		defer os.RemoveAll(state.dir)
	}

	args := baseFzfArgs(cfg, state)
	args = append(args, fzfStyleArgs(cfg, state)...)
	cmd := exec.Command(fzfPath, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return pickerResult{}, err
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return pickerResult{}, err
	}

	writeErr := writeCandidates(stdin, candidates)
	waitErr := cmd.Wait()
	if isPickerAbort(waitErr) {
		if writeErr == nil || isClosedPickerPipe(writeErr) {
			return pickerResult{}, nil
		}
		return pickerResult{}, writeErr
	}
	if writeErr != nil {
		return pickerResult{}, writeErr
	}
	if waitErr != nil {
		return pickerResult{}, waitErr
	}
	result := parsePickerResult(stdout.String())
	if result == (pickerResult{}) {
		return pickerResult{}, nil
	}
	result.createRoot = readPickerCreateRoot(cfg, state)
	return result, nil
}

func createPickerState(cfg config, candidates []candidate) (pickerState, error) {
	if len(cfg.roots) < 2 {
		return pickerState{}, nil
	}

	dir, err := os.MkdirTemp("", "workspace-launcher-fzf-*")
	if err != nil {
		return pickerState{}, err
	}

	state := pickerState{
		dir:            dir,
		rootFile:       filepath.Join(dir, "active-root"),
		footerFile:     filepath.Join(dir, "footer"),
		candidatesFile: filepath.Join(dir, "candidates"),
		cycleFile:      filepath.Join(dir, "cycle-root.sh"),
		filterFile:     filepath.Join(dir, "filter-root.sh"),
	}

	initialRoot := activeRootAll
	if err := os.WriteFile(state.rootFile, []byte(initialRoot), 0o600); err != nil {
		os.RemoveAll(dir)
		return pickerState{}, err
	}
	if err := os.WriteFile(state.footerFile, []byte(createFooterText(cfg, initialRoot)), 0o600); err != nil {
		os.RemoveAll(dir)
		return pickerState{}, err
	}
	if err := writeCandidateFile(state.candidatesFile, candidates); err != nil {
		os.RemoveAll(dir)
		return pickerState{}, err
	}
	if err := os.WriteFile(state.cycleFile, []byte(buildCycleRootScript(cfg)), 0o700); err != nil {
		os.RemoveAll(dir)
		return pickerState{}, err
	}
	if err := os.WriteFile(state.filterFile, []byte(buildFilterCandidatesScript()), 0o700); err != nil {
		os.RemoveAll(dir)
		return pickerState{}, err
	}

	return state, nil
}

func baseFzfArgs(cfg config, state pickerState) []string {
	args := []string{
		"--scheme=history",
		"--layout=reverse",
		"--tabstop=1",
		"--info=hidden",
		"--delimiter=\t",
		"--with-nth=5..",
		"--nth=" + fzfSearchNth(cfg),
		"--print-query",
		"--expect=ctrl-e,ctrl-n",
		"--query=" + cfg.initialQuery,
		"--bind=enter:accept-or-print-query",
	}

	if state.cycleFile != "" {
		args = append(args,
			"--bind=ctrl-r:execute-silent("+shellSingleQuote(state.cycleFile)+" "+shellSingleQuote(state.rootFile)+" "+shellSingleQuote(state.footerFile)+")+reload("+shellSingleQuote(state.filterFile)+" "+shellSingleQuote(state.rootFile)+" "+shellSingleQuote(state.candidatesFile)+")+transform-footer(cat "+shellSingleQuote(state.footerFile)+")",
		)
	}

	return args
}

func fzfStyleArgs(cfg config, state pickerState) []string {
	footerText := defaultFooterText()
	if state.footerFile != "" {
		footerText = createFooterText(cfg, activeRootAll)
	}

	ghostText := "Type to filter, Enter to open, Ctrl-E to edit, Ctrl-N to create"
	if state.cycleFile != "" {
		ghostText += ", Ctrl-R to switch root"
	}

	switch effectiveFzfStyle(cfg.fzfStyle) {
	case fzfStyleFull:
		return []string{
			"--ansi",
			"--prompt=",
			"--pointer=▌",
			"--color=bg:-1,bg+:#1d252c,fg:#d8d0c4,fg+:#f6efe2",
			"--color=hl:#e0a65b,hl+:#ffd08a,prompt:#8ecfd0,query:#f6efe2,ghost:#6d7d88",
			"--color=border:#50606b,label:#91c7c8,list-border:#5d7282,list-label:#a4d5d6",
			"--color=input-border:#8a6c4f,input-label:#efbf7a,footer-border:#44515c",
			"--color=pointer:#efbf7a,separator:#36434d,scrollbar:#55636e",
			"--ghost=" + ghostText,
			"--input-border",
			"--input-label= Search/New ",
			"--list-border",
			"--list-label= Folders ",
			"--footer=" + footerText,
			"--footer-border=line",
			"--bind=result:transform-list-label:printf \" Folders (%s) \" \"$FZF_MATCH_COUNT\"",
		}
	case fzfStyleMinimal:
		return []string{
			"--ansi",
			"--prompt=",
			"--pointer=▌",
			"--ghost=" + ghostText,
			"--input-border",
			"--input-label= Search/New ",
			"--list-border",
			"--list-label= Folders ",
			"--footer=" + footerText,
			"--footer-border=line",
			"--bind=result:transform-list-label:printf \" Folders (%s) \" \"$FZF_MATCH_COUNT\"",
		}
	case fzfStylePlain:
		return nil
	default:
		return nil
	}
}

func effectiveFzfStyle(style string) string {
	if style == "" {
		return fzfStyleFull
	}
	return style
}

func fzfSearchNth(cfg config) string {
	// --nth applies to the fields exposed by --with-nth, not the hidden serialized
	// prefix fields. Keep this aligned with the visible display-column order.
	columns := make([]string, 0, 2)
	column := 1
	if cfg.showRoot {
		column++
	}

	columns = append(columns, strconv.Itoa(column))
	column++
	if cfg.showLanguage {
		column++
	}
	if cfg.showGit {
		columns = append(columns, strconv.Itoa(column))
	}

	return strings.Join(columns, ",")
}

func pickRepoHeadless(cfg config, candidates []candidate) (pickerResult, error) {
	query := strings.ToLower(cfg.initialQuery)
	for _, cand := range candidates {
		line := serializeCandidate(cand)
		if query == "" || strings.Contains(strings.ToLower(candidateSearchText(cand)), query) {
			return pickerResult{selection: line, createRoot: defaultCreateRoot(cfg)}, nil
		}
	}
	return pickerResult{}, exitCodeError{code: 1}
}

func writeCandidates(w io.WriteCloser, candidates []candidate) error {
	defer w.Close()
	buf := bufio.NewWriterSize(w, 1<<20)
	for _, cand := range candidates {
		if _, err := buf.WriteString(serializeCandidate(cand)); err != nil {
			return err
		}
		if err := buf.WriteByte('\n'); err != nil {
			return err
		}
	}
	return buf.Flush()
}

func writeCandidateFile(path string, candidates []candidate) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := bufio.NewWriterSize(file, 1<<20)
	for _, cand := range candidates {
		if _, err := buf.WriteString(serializeCandidate(cand)); err != nil {
			return err
		}
		if err := buf.WriteByte('\n'); err != nil {
			return err
		}
	}
	return buf.Flush()
}

func isPickerAbort(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == 130)
}

func isClosedPickerPipe(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EPIPE)
}

func serializeCandidate(cand candidate) string {
	return cand.path + "\t" + cand.matchText + "\t\t" + cand.branchText + "\t" + cand.display
}

func candidateSearchText(cand candidate) string {
	if cand.branchText == "" {
		return cand.matchText
	}
	return cand.matchText + " " + cand.branchText
}

func branchSearchText(branch string) string {
	if branch == "" || branch == "-" {
		return ""
	}
	return branch
}

func defaultFooterText() string {
	return "Enter open | Ctrl-E edit | Ctrl-N create | Esc quit"
}

func createFooterText(cfg config, root string) string {
	if len(cfg.roots) < 2 {
		return defaultFooterText()
	}

	return renderFooterRootBadge(cfg, root) + "  Enter open | Ctrl-E edit | Ctrl-N create | Ctrl-R switch root | Esc quit"
}

func pickerRootLabel(cfg config, root string) string {
	if root == activeRootAll {
		return activeRootAllLabel
	}
	if cfg.rootLabels != nil {
		if label := cfg.rootLabels[root]; label != "" {
			return label
		}
	}
	base := filepath.Base(root)
	if base != "." && base != string(filepath.Separator) && base != "" {
		return base
	}
	return root
}

func buildCycleRootScript(cfg config) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -eu\n")
	b.WriteString("root_file=$1\n")
	b.WriteString("footer_file=$2\n")
	fmt.Fprintf(&b, "current=%s\n", shellSingleQuote(activeRootAll))
	b.WriteString("if [ -f \"$root_file\" ]; then\n")
	b.WriteString("  current=$(cat \"$root_file\")\n")
	b.WriteString("fi\n")
	b.WriteString("case \"$current\" in\n")
	fmt.Fprintf(&b, "  %s)\n", shellSingleQuote(activeRootAll))
	fmt.Fprintf(&b, "    next_root=%s\n", shellSingleQuote(cfg.roots[0]))
	fmt.Fprintf(&b, "    next_footer=%s\n", shellSingleQuote(createFooterText(cfg, cfg.roots[0])))
	b.WriteString("    ;;\n")
	for i, root := range cfg.roots {
		nextRoot := activeRootAll
		if i < len(cfg.roots)-1 {
			nextRoot = cfg.roots[i+1]
		}
		fmt.Fprintf(&b, "  %s)\n", shellSingleQuote(root))
		fmt.Fprintf(&b, "    next_root=%s\n", shellSingleQuote(nextRoot))
		fmt.Fprintf(&b, "    next_footer=%s\n", shellSingleQuote(createFooterText(cfg, nextRoot)))
		b.WriteString("    ;;\n")
	}
	fmt.Fprintf(&b, "  *)\n    next_root=%s\n    next_footer=%s\n    ;;\n", shellSingleQuote(activeRootAll), shellSingleQuote(createFooterText(cfg, activeRootAll)))
	b.WriteString("esac\n")
	b.WriteString("printf '%s' \"$next_root\" > \"$root_file\"\n")
	b.WriteString("printf '%s' \"$next_footer\" > \"$footer_file\"\n")
	return b.String()
}

func buildFilterCandidatesScript() string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -eu\n")
	b.WriteString("root_file=$1\n")
	b.WriteString("candidates_file=$2\n")
	fmt.Fprintf(&b, "active_root=%s\n", shellSingleQuote(activeRootAll))
	b.WriteString("if [ -f \"$root_file\" ]; then\n")
	b.WriteString("  active_root=$(cat \"$root_file\")\n")
	b.WriteString("fi\n")
	fmt.Fprintf(&b, "if [ \"$active_root\" = %s ]; then\n", shellSingleQuote(activeRootAll))
	b.WriteString("  cat \"$candidates_file\"\n")
	b.WriteString("  exit 0\n")
	b.WriteString("fi\n")
	b.WriteString("awk -F '\\t' -v root=\"$active_root\" 'index($1, root \"/\") == 1 || $1 == root { print }' \"$candidates_file\"\n")
	return b.String()
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func readPickerCreateRoot(cfg config, state pickerState) string {
	if state.rootFile == "" {
		return defaultCreateRoot(cfg)
	}
	content, err := os.ReadFile(state.rootFile)
	if err != nil {
		return defaultCreateRoot(cfg)
	}
	root := strings.TrimSpace(string(content))
	if root == activeRootAll {
		return activeRootAll
	}
	for _, configuredRoot := range cfg.roots {
		if root == configuredRoot {
			return root
		}
	}
	return defaultCreateRoot(cfg)
}

func parsePickerResult(result string) pickerResult {
	result = strings.TrimRight(result, "\n")
	if result == "" {
		return pickerResult{}
	}

	lines := strings.Split(result, "\n")
	if len(lines) == 1 {
		if strings.Contains(lines[0], "\t") {
			return pickerResult{selection: lines[0]}
		}
		return pickerResult{query: lines[0]}
	}

	parsed := pickerResult{query: lines[0]}
	lines = lines[1:]
	if len(lines) == 0 {
		return parsed
	}
	if lines[0] == "" || isPickerKey(lines[0]) {
		parsed.key = lines[0]
		lines = lines[1:]
	}
	if len(lines) > 0 {
		parsed.selection = lines[0]
	}
	return parsed
}

func isPickerKey(key string) bool {
	switch key {
	case "ctrl-e", "ctrl-n":
		return true
	default:
		return false
	}
}
