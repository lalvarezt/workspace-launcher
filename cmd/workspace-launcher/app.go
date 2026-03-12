package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].epoch == candidates[j].epoch {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].epoch > candidates[j].epoch
	})

	result, err := pickRepo(cfg, fzfPath, candidates)
	if err != nil {
		return err
	}
	if result == (pickerResult{}) {
		return exitCodeError{code: 0}
	}

	target, err := resolveSelection(cfg, result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, target)
	return err
}
