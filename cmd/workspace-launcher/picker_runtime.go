package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	pickerPollInterval        = 50 * time.Millisecond
	pickerFlushInterval       = 75 * time.Millisecond
	pickerReloadSettleTimeout = 50 * time.Millisecond
)

type dirtyUpdate struct {
	generation uint64
	path       string
	dirty      bool
	status     dirtyStatus
}

type dirtyBatchDone struct {
	generation uint64
}

type dirtyJob struct {
	path string
}

type pickerRuntime struct {
	cfg        config
	state      pickerState
	candidates []candidate

	mu           sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	statusCancel context.CancelFunc
	statusWG     sync.WaitGroup

	activeRoot string
	generation uint64
}

func newPickerRuntime(cfg config, state pickerState, candidates []candidate) *pickerRuntime {
	if state.listenSocket == "" {
		return nil
	}

	initial := make([]candidate, len(candidates))
	copy(initial, candidates)
	return &pickerRuntime{
		cfg:        cfg,
		state:      state,
		candidates: initial,
		activeRoot: readActivePickerRoot(state.rootFile, cfg),
	}
}

func (r *pickerRuntime) Start() {
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.done = make(chan struct{})
	go r.loop()
}

func (r *pickerRuntime) Stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
}

func (r *pickerRuntime) loop() {
	defer close(r.done)

	updates := make(chan dirtyUpdate, 128)
	done := make(chan dirtyBatchDone, 2)
	pollTicker := time.NewTicker(pickerPollInterval)
	flushTicker := time.NewTicker(pickerFlushInterval)
	defer pollTicker.Stop()
	defer flushTicker.Stop()

	r.startDirtyStatus(updates, done, false)
	pendingFlush := false

	for {
		select {
		case <-r.ctx.Done():
			r.stopDirtyStatus()
			return
		case update := <-updates:
			if update.generation != r.generation {
				continue
			}
			if r.applyDirtyUpdate(update) {
				pendingFlush = true
			}
		case finished := <-done:
			if finished.generation != r.generation {
				continue
			}
			if pendingFlush {
				_ = r.flushCandidates("Git status ready")
				pendingFlush = false
			} else {
				_ = r.setFooter("Git status ready")
			}
		case <-flushTicker.C:
			if pendingFlush {
				_ = r.flushCandidates()
				pendingFlush = false
			}
		case <-pollTicker.C:
			root := readActivePickerRoot(r.state.rootFile, r.cfg)
			if root != "" && root != r.activeRoot {
				r.activeRoot = root
				r.startDirtyStatus(updates, done, true)
				pendingFlush = false
			}
			if r.consumeRefreshRequest() {
				r.refreshActiveRoot(updates, done)
				pendingFlush = false
			}
		}
	}
}

func (r *pickerRuntime) startDirtyStatus(updates chan<- dirtyUpdate, done chan<- dirtyBatchDone, refreshRows bool) {
	r.stopDirtyStatus()
	r.generation++
	generation := r.generation

	if r.cfg.gitDirty && r.cfg.deferGitDirty {
		if refreshRows && r.markPending(r.activeRoot) {
			_ = r.flushCandidates()
		}
		_ = r.setFooter("Checking git status")
	}

	if !r.cfg.gitDirty || !r.cfg.deferGitDirty {
		return
	}

	jobs := r.dirtyJobsForRoot(r.activeRoot)
	statusCtx, cancel := context.WithCancel(r.ctx)
	r.statusCancel = cancel
	r.statusWG.Add(1)
	go func() {
		defer r.statusWG.Done()
		runDirtyStatus(statusCtx, r.cfg.jobs, generation, jobs, updates, done)
	}()
}

func (r *pickerRuntime) stopDirtyStatus() {
	if r.statusCancel == nil {
		return
	}
	r.statusCancel()
	r.statusWG.Wait()
	r.statusCancel = nil
}

func runDirtyStatus(ctx context.Context, jobs int, generation uint64, entries []dirtyJob, updates chan<- dirtyUpdate, done chan<- dirtyBatchDone) {
	if len(entries) == 0 {
		select {
		case done <- dirtyBatchDone{generation: generation}:
		case <-ctx.Done():
		}
		return
	}

	workerCount := jobs
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(entries) {
		workerCount = len(entries)
	}

	var next int
	var nextMu sync.Mutex
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for {
				nextMu.Lock()
				if next >= len(entries) {
					nextMu.Unlock()
					return
				}
				entry := entries[next]
				next++
				nextMu.Unlock()

				dirty, status := readDirtyStatus(ctx, entry.path)
				select {
				case updates <- dirtyUpdate{generation: generation, path: entry.path, dirty: dirty, status: status}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	workers.Wait()
	select {
	case done <- dirtyBatchDone{generation: generation}:
	case <-ctx.Done():
	}
}

func (r *pickerRuntime) applyDirtyUpdate(update dirtyUpdate) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.candidates {
		if r.candidates[i].path != update.path {
			continue
		}
		detail := r.candidates[i].detail
		if detail == nil || !detail.git.present {
			return false
		}
		detail.git.dirty = update.dirty
		detail.git.dirtyStatus = update.status
		return true
	}
	return false
}

func (r *pickerRuntime) markPending(root string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	for i := range r.candidates {
		if !candidateBelongsToRoot(&r.candidates[i], root) || r.candidates[i].detail == nil || !r.candidates[i].detail.git.present {
			continue
		}
		detail := r.candidates[i].detail
		if detail.git.dirtyStatus == dirtyStatusPending && !detail.git.dirty {
			continue
		}
		detail.git.dirty = false
		detail.git.dirtyStatus = dirtyStatusPending
		changed = true
	}
	return changed
}

func (r *pickerRuntime) dirtyJobsForRoot(root string) []dirtyJob {
	r.mu.RLock()
	defer r.mu.RUnlock()
	jobs := make([]dirtyJob, 0, len(r.candidates))
	for i := range r.candidates {
		candidate := &r.candidates[i]
		if candidateBelongsToRoot(candidate, root) && candidate.detail != nil && candidate.detail.git.present {
			jobs = append(jobs, dirtyJob{path: candidate.path})
		}
	}
	return jobs
}

func (r *pickerRuntime) flushCandidates(footerStatus ...string) error {
	r.mu.Lock()
	details := make([]repoDetails, len(r.candidates))
	for i := range r.candidates {
		if r.candidates[i].detail != nil {
			details[i] = *r.candidates[i].detail
		}
	}
	r.candidates = renderCandidates(r.cfg, details)
	snapshot := make([]candidate, len(r.candidates))
	copy(snapshot, r.candidates)
	r.mu.Unlock()

	if err := writeCandidateFileAtomic(r.state.candidatesFile, snapshot); err != nil {
		return err
	}
	action := r.reloadAction()
	if len(footerStatus) > 0 && footerStatus[0] != "" {
		if err := r.writeFooter(footerStatus[0]); err != nil {
			return err
		}
		if err := r.sendAction(action); err != nil {
			return err
		}
		timer := time.NewTimer(pickerReloadSettleTimeout)
		defer timer.Stop()
		select {
		case <-r.ctx.Done():
			return r.ctx.Err()
		case <-timer.C:
		}
		return r.sendAction(r.footerAction())
	}
	err := r.sendAction(action)
	return err
}

func (r *pickerRuntime) refreshActiveRoot(updates chan<- dirtyUpdate, done chan<- dirtyBatchDone) {
	_ = r.setFooter("Refreshing workspaces")
	r.stopDirtyStatus()
	r.generation++

	scanCfg := r.cfg
	if r.activeRoot != activeRootAll {
		scanCfg.roots = []string{r.activeRoot}
	}
	fresh, err := buildCandidates(scanCfg)
	if err != nil {
		_ = r.setFooter("Refresh failed")
		return
	}
	sortCandidates(fresh)

	r.mu.Lock()
	if r.activeRoot == activeRootAll {
		r.candidates = fresh
	} else {
		merged := make([]candidate, 0, len(r.candidates)+len(fresh))
		for i := range r.candidates {
			candidate := &r.candidates[i]
			if !candidateBelongsToRoot(candidate, r.activeRoot) {
				merged = append(merged, *candidate)
			}
		}
		merged = append(merged, fresh...)
		sortCandidates(merged)
		details := make([]repoDetails, len(merged))
		for i := range merged {
			if merged[i].detail != nil {
				details[i] = *merged[i].detail
			}
		}
		r.candidates = renderCandidates(r.cfg, details)
	}
	snapshot := make([]candidate, len(r.candidates))
	copy(snapshot, r.candidates)
	r.mu.Unlock()

	if err := writeCandidateFileAtomic(r.state.candidatesFile, snapshot); err != nil {
		_ = r.setFooter("Refresh failed")
		return
	}
	_ = r.sendAction(r.reloadAction())

	r.startDirtyStatus(updates, done, false)
}

func (r *pickerRuntime) consumeRefreshRequest() bool {
	if r.state.refreshFile == "" {
		return false
	}
	content, err := os.ReadFile(r.state.refreshFile)
	if err != nil || len(content) == 0 {
		return false
	}
	_ = os.WriteFile(r.state.refreshFile, nil, 0o600)
	return true
}

func (r *pickerRuntime) setFooter(status string) error {
	if r.state.footerFile == "" {
		return nil
	}
	if err := r.writeFooter(status); err != nil {
		return err
	}
	err := r.sendAction(r.footerAction())
	return err
}

func (r *pickerRuntime) writeFooter(status string) error {
	footer := createFooterText(r.cfg, r.activeRoot)
	if r.cfg.gitDirty && status != "" {
		footer += " | " + status
	}
	return os.WriteFile(r.state.footerFile, []byte(footer), 0o600)
}

func (r *pickerRuntime) footerAction() string {
	return "transform-footer(cat " + shellSingleQuote(r.state.footerFile) + ")"
}

func (r *pickerRuntime) reloadAction() string {
	command := shellSingleQuote(r.state.filterFile) + " " + shellSingleQuote(r.state.rootFile) + " " + shellSingleQuote(r.state.candidatesFile)
	return "reload(" + command + ")"
}

func (r *pickerRuntime) sendAction(action string) error {
	return postFzfAction(r.ctx, r.state.listenSocket, action)
}

func postFzfAction(ctx context.Context, socketPath, action string) error {
	if socketPath == "" {
		return errors.New("empty fzf listen socket")
	}

	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		err := postFzfActionOnce(attemptCtx, socketPath, action)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err

		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func postFzfActionOnce(ctx context.Context, socketPath, action string) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-closed:
		}
	}()

	var request strings.Builder
	request.Grow(len(action) + 128)
	request.WriteString("POST / HTTP/1.1\r\nHost: localhost\r\nContent-Type: text/plain\r\nContent-Length: ")
	request.WriteString(strconv.Itoa(len(action)))
	request.WriteString("\r\nConnection: close\r\n\r\n")
	request.WriteString(action)
	if _, err := conn.Write([]byte(request.String())); err != nil {
		return err
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	fields := strings.SplitN(strings.TrimSpace(statusLine), " ", 3)
	if len(fields) < 2 {
		return errors.New("invalid fzf action response")
	}
	statusCode, err := strconv.Atoi(fields[1])
	if err != nil {
		return errors.New("invalid fzf action response status")
	}
	if statusCode < 200 || statusCode >= 300 {
		return fmt.Errorf("fzf action returned HTTP status %d", statusCode)
	}
	return nil
}

func writeCandidateFileAtomic(path string, candidates []candidate) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".candidates-*")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := writeSerializedCandidates(file, candidates); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}

func readActivePickerRoot(path string, cfg config) string {
	content, err := os.ReadFile(path)
	if err == nil {
		if root := strings.TrimSpace(string(content)); root != "" {
			return root
		}
	}
	if len(cfg.roots) == 1 {
		return cfg.roots[0]
	}
	return activeRootAll
}

func candidateBelongsToRoot(candidate *candidate, root string) bool {
	if candidate == nil {
		return false
	}
	if root == activeRootAll {
		return true
	}
	if candidate.detail != nil && candidate.detail.child.root != "" {
		return candidate.detail.child.root == root
	}
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(candidate.path)
	return cleanPath == cleanRoot || strings.HasPrefix(cleanPath, cleanRoot+string(os.PathSeparator))
}
