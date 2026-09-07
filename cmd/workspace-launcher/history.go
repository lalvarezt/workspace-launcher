package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type workspaceEntry struct {
	Pinned     bool  `json:"pinned,omitempty"`
	LastOpened int64 `json:"last_opened,omitempty"`
}

type workspaceState map[string]workspaceEntry

func canonicalWorkspacePath(path string) string {
	abs, err := filepath.Abs(expandHome(path))
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func workspaceStatePath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, appName, "history.json"), nil
}

func readWorkspaceState(path string) (workspaceState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(workspaceState), nil
	}
	if err != nil {
		return nil, err
	}
	state := make(workspaceState)
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if state == nil {
		state = make(workspaceState)
	}
	return state, nil
}

func loadWorkspaceState() (workspaceState, error) {
	path, err := workspaceStatePath()
	if err != nil {
		return nil, err
	}
	return readWorkspaceState(path)
}

// Lock a separate file so atomic replacement does not invalidate the lock.
func updateWorkspaceState(update func(workspaceState)) error {
	path, err := workspaceStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for workspace history lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	state, err := readWorkspaceState(path)
	if err != nil {
		return err
	}
	update(state)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".history-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func recordWorkspaceVisit(path string, opened int64) {
	path = canonicalWorkspacePath(path)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return
	}
	if err := updateWorkspaceState(func(state workspaceState) {
		entry := state[path]
		entry.LastOpened = max(entry.LastOpened, opened)
		state[path] = entry
	}); err != nil {
		fmt.Fprintf(os.Stderr, "%s: could not save visit: %v\n", appName, err)
	}
}

func manageWorkspaceState(cfg config) error {
	target := canonicalWorkspacePath(cfg.stateTarget)
	if cfg.stateAction == "--pin" {
		var err error
		target, err = resolveRoot(cfg.stateTarget)
		if err != nil {
			return err
		}
	}
	return updateWorkspaceState(func(state workspaceState) {
		if cfg.stateAction == "--clear-history" {
			for path, entry := range state {
				if entry.Pinned {
					state[path] = workspaceEntry{Pinned: true}
				} else {
					delete(state, path)
				}
			}
			return
		}
		entry := state[target]
		entry.Pinned = cfg.stateAction == "--pin"
		if !entry.Pinned && entry.LastOpened == 0 {
			delete(state, target)
		} else {
			state[target] = entry
		}
	})
}
