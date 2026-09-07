package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const appName = "workspace-launcher"

var version = "dev"

func main() {
	if err := run(); err != nil {
		if exitErr, ok := errors.AsType[exitCodeError](err); ok {
			os.Exit(exitErr.code)
		}
		if _, writeErr := fmt.Fprintf(os.Stderr, "%s: %s\n", filepath.Base(os.Args[0]), err); writeErr != nil {
			os.Exit(1)
		}
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		return err
	}

	if outputsShellIntegration(cfg.mode) {
		script, err := renderShellIntegration(cfg.mode, cfg.shellBindings)
		if err != nil {
			return err
		}
		_, err = io.WriteString(os.Stdout, script)
		return err
	}

	if cfg.stateAction != "" {
		return manageWorkspaceState(cfg)
	}
	if !cfg.headlessBench {
		cfg.state, err = loadWorkspaceState()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: history unavailable: %v\n", appName, err)
		}
	}
	fzfPath := ""
	if !cfg.headlessBench {
		fzfPath, err = resolveFzf()
		if err != nil {
			return err
		}
	}

	candidates, err := buildCandidates(cfg)
	if err != nil {
		return err
	}
	sortCandidates(candidates)

	result, err := pickRepo(cfg, fzfPath, candidates)
	if err != nil {
		return err
	}
	if result == (pickerResult{}) {
		return exitCodeError{code: 0}
	}

	target, err := resolveSelection(cfg, result)
	if result.key == "ctrl-e" {
		var exitErr exitCodeError
		if errors.As(err, &exitErr) && exitErr.code == 0 && !cfg.headlessBench {
			if selected, ok := selectedPath(result.selection); ok {
				recordWorkspaceVisit(selected, time.Now().UnixNano())
			}
		}
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, target)
	if err == nil && target != "" && !cfg.headlessBench {
		recordWorkspaceVisit(target, time.Now().UnixNano())
	}
	return err
}

func sortCandidates(candidates []candidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].pinned != candidates[j].pinned {
			return candidates[i].pinned
		}
		if candidates[i].pinned {
			return candidates[i].path < candidates[j].path
		}
		if candidates[i].opened != candidates[j].opened {
			return candidates[i].opened > candidates[j].opened
		}
		if candidates[i].epoch == candidates[j].epoch {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].epoch > candidates[j].epoch
	})
}
