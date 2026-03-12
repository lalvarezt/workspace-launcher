package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

func resolveSelection(cfg config, result pickerResult) (string, error) {
	switch result.key {
	case "ctrl-e":
		target, ok := selectedPath(result.selection)
		if !ok {
			return "", errors.New("no directory selected")
		}
		return "", openInEditor(target)
	case "ctrl-n":
		return createWorkspace(cfg, result.query, result.createRoot)
	case "":
		if target, ok := selectedPath(result.selection); ok {
			return target, nil
		}
		name := result.query
		if name == "" {
			name = result.selection
		}
		return createWorkspace(cfg, name, result.createRoot)
	default:
		return "", fmt.Errorf("unknown key: %s", result.key)
	}
}

func selectedPath(selection string) (string, bool) {
	if !strings.Contains(selection, "\t") {
		return "", false
	}
	return strings.SplitN(selection, "\t", 2)[0], true
}

func createWorkspace(cfg config, name, currentRoot string) (string, error) {
	if err := validateNewName(name); err != nil {
		return "", err
	}

	root, err := resolveCreateRoot(cfg, currentRoot)
	if err != nil {
		return "", err
	}

	target := filepath.Join(root, name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	return target, nil
}

func defaultCreateRoot(cfg config) string {
	if len(cfg.roots) == 0 {
		return ""
	}
	return cfg.roots[0]
}

func resolveCreateRoot(cfg config, currentRoot string) (string, error) {
	if currentRoot == activeRootAll {
		return defaultCreateRoot(cfg), nil
	}

	if slices.Contains(cfg.roots, currentRoot) {
		return currentRoot, nil
	}

	if len(cfg.roots) == 1 {
		return defaultCreateRoot(cfg), nil
	}

	return "", errors.New("no active create root selected")
}

func validateNewName(name string) error {
	if name == "" {
		return errors.New("empty query")
	}
	if strings.ContainsRune(name, filepath.Separator) {
		return errors.New("new directory name cannot contain '/'")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("invalid directory name: %s", name)
	}
	return nil
}

func openInEditor(target string) error {
	if os.Getenv("VISUAL") == "" && os.Getenv("EDITOR") == "" {
		return errors.New("VISUAL or EDITOR is not set")
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /dev/tty: %w", err)
	}
	defer tty.Close()
	shell := os.Getenv("BASH")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "-lc", `exec ${VISUAL:-${EDITOR:-}} "$1"`, "sh", target)
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return exitCodeError{code: exitErr.ExitCode()}
		}
		return fmt.Errorf("open editor: %w", err)
	}
	return exitCodeError{code: 0}
}
