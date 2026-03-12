package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	shellscripts "github.com/lalvarezt/workspace-launcher/shell"
)

func outputsShellIntegration(mode string) bool {
	switch mode {
	case modeBash, modeZsh, modeFish:
		return true
	default:
		return false
	}
}

func renderShellIntegration(mode string, bindings bool) (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	return renderShellIntegrationForPath(mode, exePath, bindings)
}

func renderShellIntegrationForPath(mode, binPath string, bindings bool) (string, error) {
	switch mode {
	case modeBash:
		return shellscripts.Bash(binPath, bindings), nil
	case modeZsh:
		return shellscripts.Zsh(binPath, bindings), nil
	case modeFish:
		return shellscripts.Fish(binPath, bindings), nil
	default:
		return "", fmt.Errorf("unknown shell integration mode: %s", mode)
	}
}

func resolveFzf() (string, error) {
	if fzfBin := os.Getenv("FZF_BIN"); fzfBin != "" {
		return fzfBin, nil
	}
	exePath, err := os.Executable()
	if err == nil {
		exePath, _ = filepath.EvalSymlinks(exePath)
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(exePath), ".."))
		candidate := filepath.Join(repoRoot, "bin", "fzf")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	path, err := exec.LookPath("fzf")
	if err != nil {
		return "", errors.New("fzf not found")
	}
	return path, nil
}
