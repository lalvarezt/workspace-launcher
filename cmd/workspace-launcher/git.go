package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

type resettableZlibReader interface {
	io.ReadCloser
	Reset(io.Reader, []byte) error
}

// gitEpochCache shares immutable commit timestamps across repositories that
// point at the same commit object, such as linked worktrees.
type gitEpochCache struct {
	mu       sync.RWMutex
	epochs   map[string]int64
	inflight map[string]*gitEpochLoad
}

type gitEpochLoad struct {
	done  chan struct{}
	epoch int64
	err   error
}

func (c *gitEpochCache) load(hash string, read func() (int64, error)) (int64, error) {
	if c == nil {
		return read()
	}
	c.mu.RLock()
	epoch, ok := c.epochs[hash]
	c.mu.RUnlock()
	if ok {
		return epoch, nil
	}

	c.mu.Lock()
	if epoch, ok := c.epochs[hash]; ok {
		c.mu.Unlock()
		return epoch, nil
	}
	if call, ok := c.inflight[hash]; ok {
		c.mu.Unlock()
		<-call.done
		return call.epoch, call.err
	}
	if c.inflight == nil {
		c.inflight = make(map[string]*gitEpochLoad)
	}
	call := &gitEpochLoad{done: make(chan struct{})}
	c.inflight[hash] = call
	c.mu.Unlock()

	epoch, err := read()

	c.mu.Lock()
	delete(c.inflight, hash)
	if err == nil && epoch > 0 {
		if c.epochs == nil {
			c.epochs = make(map[string]int64)
		}
		c.epochs[hash] = epoch
	}
	call.epoch = epoch
	call.err = err
	close(call.done)
	c.mu.Unlock()
	return epoch, err
}

var (
	zlibInputReaderPool = sync.Pool{
		New: func() any {
			return bufio.NewReaderSize(nil, 1024)
		},
	}
	commitObjectReaderPool = sync.Pool{
		New: func() any {
			return bufio.NewReaderSize(nil, 1024)
		},
	}
	packedRefsReaderPool = sync.Pool{
		New: func() any {
			return bufio.NewReaderSize(nil, 4096)
		},
	}
	zlibReaderPool = make(chan resettableZlibReader, max(runtime.NumCPU(), 1))
)

func inspectGitMeta(dir string, gitIsDir, wantBranch, wantEpoch, wantDirty bool) gitMeta {
	return inspectGitMetaWithCache(dir, gitIsDir, wantBranch, wantEpoch, wantDirty, nil)
}

func inspectGitMetaWithCache(dir string, gitIsDir, wantBranch, wantEpoch, wantDirty bool, cache *gitEpochCache) gitMeta {
	return inspectGitMetaWithKnownDir(dir, gitIsDir, "", wantBranch, wantEpoch, wantDirty, cache)
}

func inspectGitMetaWithKnownDir(dir string, gitIsDir bool, knownGitDir string, wantBranch, wantEpoch, wantDirty bool, cache *gitEpochCache) gitMeta {
	meta := gitMeta{
		present:     true,
		branchLabel: "-",
	}

	if !wantBranch && !wantEpoch {
		meta.isWorktree = !gitIsDir
		if knownGitDir != "" {
			meta.isWorktree = true
			meta.isSubmodule, meta.isLocked = classifyLinkedGitDir(knownGitDir, true)
			if meta.isSubmodule {
				meta.isWorktree = false
			}
		} else if !gitIsDir {
			gitDir, isWorktree, err := inspectDotGit(dir)
			if err == nil {
				meta.isWorktree = isWorktree
				meta.isSubmodule, meta.isLocked = classifyLinkedGitDir(gitDir, isWorktree)
				if meta.isSubmodule {
					meta.isWorktree = false
				}
			}
		}
		if wantDirty {
			if dirty, dirtyErr := gitIsDirty(dir); dirtyErr == nil {
				meta.dirty = dirty
			}
		}
		return meta
	}

	gitDir := filepath.Join(dir, ".git")
	isWorktree := false
	if knownGitDir != "" {
		gitDir = knownGitDir
		isWorktree = true
	} else if !gitIsDir {
		var err error
		gitDir, isWorktree, err = inspectDotGit(dir)
		if err != nil {
			if wantDirty {
				if dirty, dirtyErr := gitIsDirty(dir); dirtyErr == nil {
					meta.dirty = dirty
				}
			}
			if wantEpoch {
				if epoch, epochErr := gitLastCommitEpochSlow(dir); epochErr == nil && epoch > 0 {
					meta.epoch = epoch
				}
			}
			return meta
		}
	}

	meta.isWorktree = isWorktree
	meta.isSubmodule, meta.isLocked = classifyLinkedGitDir(gitDir, isWorktree)
	if meta.isSubmodule {
		meta.isWorktree = false
	}
	if wantEpoch {
		layout := gitLayout{gitDir: gitDir, commonDir: gitDir}
		var layoutErr error
		if isWorktree {
			layout, layoutErr = finalizeGitLayout(gitDir)
		}
		if layoutErr == nil {
			head, headErr := readHeadFile(layout.gitDir)
			if headErr == nil {
				if wantBranch {
					meta.branchLabel = formatHeadLabel(head)
				}
				if hash, resolveErr := resolveHeadHashFromHead(layout, head); resolveErr == nil {
					meta.headHash = hash
					var epoch int64
					var readErr error
					if meta.isWorktree {
						epoch, readErr = readCommitEpochWithCache(layout, hash, cache)
					} else {
						epoch, readErr = readCommitEpoch(layout, hash)
					}
					if readErr == nil && epoch > 0 {
						meta.epoch = epoch
					}
				}
			}
		}
	} else {
		head, headErr := readHeadFile(gitDir)
		if headErr == nil && wantBranch {
			meta.branchLabel = formatHeadLabel(head)
		}
	}

	if wantEpoch && meta.epoch <= 0 {
		if epoch, epochErr := gitLastCommitEpochSlow(dir); epochErr == nil && epoch > 0 {
			meta.epoch = epoch
		}
	}
	if wantDirty {
		if dirty, dirtyErr := gitIsDirty(dir); dirtyErr == nil {
			meta.dirty = dirty
		}
	}

	return meta
}

func readCommitEpochWithCache(layout gitLayout, hash string, cache *gitEpochCache) (int64, error) {
	return cache.load(hash, func() (int64, error) {
		return readCommitEpoch(layout, hash)
	})
}

func classifyLinkedGitDir(gitDir string, isWorktree bool) (bool, bool) {
	if !isWorktree {
		return false, false
	}

	cleanGitDir := filepath.Clean(gitDir)
	separator := string(filepath.Separator)
	gitComponent := separator + ".git"
	for searchEnd := len(cleanGitDir); searchEnd > 0; {
		markerIndex := strings.LastIndex(cleanGitDir[:searchEnd], gitComponent)
		if markerIndex < 0 {
			break
		}
		componentEnd := markerIndex + len(gitComponent)
		if componentEnd == len(cleanGitDir) || strings.HasPrefix(cleanGitDir[componentEnd:], separator) {
			if componentEnd < len(cleanGitDir) && strings.HasPrefix(cleanGitDir[componentEnd+len(separator):], "modules"+separator) {
				return true, false
			}
			return false, false
		}
		searchEnd = markerIndex + len(gitComponent) - 1
	}
	if strings.HasPrefix(cleanGitDir, ".git"+separator) && strings.HasPrefix(strings.TrimPrefix(cleanGitDir, ".git"+separator), "modules"+separator) {
		return true, false
	}
	if _, err := os.Stat(filepath.Join(gitDir, "locked")); err == nil {
		return false, true
	}
	return false, false
}

func gitLastCommitEpochFast(dir string) (int64, error) {
	layout, err := resolveGitLayout(dir)
	if err == nil {
		head, headErr := readHeadFile(layout.gitDir)
		if headErr == nil {
			headHash, resolveErr := resolveHeadHashFromHead(layout, head)
			if resolveErr == nil {
				if epoch, readErr := readCommitEpoch(layout, headHash); readErr == nil && epoch > 0 {
					return epoch, nil
				}
			}
		}
	}
	return gitLastCommitEpochSlow(dir)
}

func gitLastCommitEpochSlow(dir string) (int64, error) {
	cmd := exec.Command("git", "-C", dir, "-c", "log.showSignature=false", "log", "-1", "--format=%ct")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return 0, errors.New("empty git epoch")
	}
	epoch, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return epoch, nil
}

func resolveGitLayout(dir string) (gitLayout, error) {
	gitDir, _, err := inspectDotGit(dir)
	if err != nil {
		return gitLayout{}, err
	}
	return finalizeGitLayout(filepath.Clean(gitDir))
}

func inspectDotGit(dir string) (string, bool, error) {
	gitPath := filepath.Join(dir, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", false, err
	}
	if info.IsDir() {
		return gitPath, false, nil
	}

	content, err := os.ReadFile(gitPath)
	if err != nil {
		return "", false, err
	}
	return parseGitDirFile(dir, content)
}

func parseGitDirFile(dir string, content []byte) (string, bool, error) {
	line := strings.TrimSpace(string(content))
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return "", false, errors.New("unsupported .git file format")
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	return filepath.Clean(gitDir), true, nil
}

func finalizeGitLayout(gitDir string) (gitLayout, error) {
	layout := gitLayout{
		gitDir:    gitDir,
		commonDir: gitDir,
	}
	content, err := readTrimmedSmallFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return layout, nil
		}
		return gitLayout{}, err
	}
	commonDir := content
	if commonDir == "" {
		return layout, nil
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	layout.commonDir = filepath.Clean(commonDir)
	return layout, nil
}

func readHeadFile(gitDir string) (string, error) {
	head, err := readTrimmedSmallFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", err
	}
	if head == "" {
		return "", errors.New("empty HEAD")
	}
	return head, nil
}

func resolveHeadHashFromHead(layout gitLayout, head string) (string, error) {
	if !strings.HasPrefix(head, "ref: ") {
		return head, nil
	}

	refName := strings.TrimSpace(strings.TrimPrefix(head, "ref: "))
	refPathSuffix := filepath.FromSlash(refName)
	baseDirs := [...]string{layout.commonDir, layout.gitDir}
	for i, baseDir := range baseDirs {
		if i > 0 && baseDir == baseDirs[0] {
			continue
		}
		refPath := filepath.Join(baseDir, refPathSuffix)
		hash, err := readTrimmedSmallFile(refPath)
		if err == nil {
			if hash != "" {
				return hash, nil
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}

	return lookupPackedRef(layout, refName)
}

func formatHeadLabel(head string) string {
	head = strings.TrimSpace(head)
	if head == "" {
		return "-"
	}
	if refName, ok := strings.CutPrefix(head, "ref: "); ok {
		return formatRefLabel(strings.TrimSpace(refName))
	}
	if len(head) > 7 {
		head = head[:7]
	}
	return "detached@" + head
}

func formatRefLabel(refName string) string {
	refName = strings.TrimSpace(refName)
	if refName == "" {
		return "-"
	}
	switch {
	case strings.HasPrefix(refName, "refs/heads/"):
		return strings.TrimPrefix(refName, "refs/heads/")
	case strings.HasPrefix(refName, "refs/remotes/"):
		return strings.TrimPrefix(refName, "refs/remotes/")
	case strings.HasPrefix(refName, "refs/"):
		tail := strings.TrimPrefix(refName, "refs/")
		if strings.Count(tail, "/") <= 1 {
			return path.Base(tail)
		}
		return tail
	default:
		return path.Base(refName)
	}
}

func lookupPackedRef(layout gitLayout, refName string) (string, error) {
	baseDirs := [...]string{layout.commonDir, layout.gitDir}
	for i, baseDir := range baseDirs {
		if i > 0 && baseDir == baseDirs[0] {
			continue
		}
		hash, err := lookupPackedRefFile(filepath.Join(baseDir, "packed-refs"), refName)
		if err == nil {
			return hash, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", errors.New("ref not found")
}

func lookupPackedRefFile(path, refName string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	reader := acquirePackedRefsReader(file)
	defer releasePackedRefsReader(reader)
	for {
		line, readErr := readBufferedLine(reader)
		if len(line) == 0 || line[0] == '#' || line[0] == '^' {
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return "", readErr
			}
			continue
		}
		separator := bytes.IndexByte(line, ' ')
		if separator > 0 && string(line[separator+1:]) == refName {
			return string(line[:separator]), nil
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return "", errors.New("ref not found")
}

func readCommitEpoch(layout gitLayout, hash string) (int64, error) {
	if len(hash) < 40 {
		return 0, errors.New("invalid commit hash")
	}

	epoch, err := readCommitEpochFromObjects(filepath.Join(layout.commonDir, "objects"), hash)
	if err == nil {
		return epoch, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	if layout.gitDir == layout.commonDir {
		return 0, os.ErrNotExist
	}

	return readCommitEpochFromObjects(filepath.Join(layout.gitDir, "objects"), hash)
}

func readCommitEpochFromObjects(objectDir, hash string) (int64, error) {
	objectPath := filepath.Join(objectDir, hash[:2], hash[2:])
	file, err := os.Open(objectPath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	input := acquireZlibInputReader(file)
	defer releaseZlibInputReader(input)

	reader, err := acquireZlibReader(input)
	if err != nil {
		return 0, err
	}
	defer releaseZlibReader(reader)

	buf := acquireCommitObjectReader(reader)
	defer releaseCommitObjectReader(buf)
	if _, err := buf.ReadSlice(0); err != nil {
		return 0, errors.New("invalid object header")
	}

	for {
		line, err := readBufferedLine(buf)
		if err != nil && err != io.EOF {
			return 0, err
		}
		if bytes.HasPrefix(line, []byte("committer ")) {
			epochText, parseErr := parseCommitterEpochBytes(line)
			if parseErr != nil {
				return 0, parseErr
			}
			epoch, parseErr := strconv.ParseInt(string(epochText), 10, 64)
			if parseErr != nil {
				return 0, parseErr
			}
			return epoch, nil
		}
		if err == io.EOF {
			break
		}
	}

	return 0, errors.New("committer line not found")
}

func readBufferedLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if err != bufio.ErrBufferFull {
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}
		return line, err
	}

	fullLine := append([]byte(nil), line...)
	for err == bufio.ErrBufferFull {
		line, err = reader.ReadSlice('\n')
		fullLine = append(fullLine, line...)
	}
	if len(fullLine) > 0 && fullLine[len(fullLine)-1] == '\n' {
		fullLine = fullLine[:len(fullLine)-1]
	}
	return fullLine, err
}

func parseCommitterEpochBytes(line []byte) ([]byte, error) {
	line = bytes.TrimSpace(line)
	lastSpace := bytes.LastIndexByte(line, ' ')
	if lastSpace < 0 {
		return nil, errors.New("invalid committer line")
	}
	prevSpace := bytes.LastIndexByte(line[:lastSpace], ' ')
	if prevSpace < 0 || prevSpace+1 >= lastSpace {
		return nil, errors.New("invalid committer line")
	}
	return line[prevSpace+1 : lastSpace], nil
}

func acquireZlibInputReader(r io.Reader) *bufio.Reader {
	reader := zlibInputReaderPool.Get().(*bufio.Reader)
	reader.Reset(r)
	return reader
}

func releaseZlibInputReader(reader *bufio.Reader) {
	reader.Reset(nil)
	zlibInputReaderPool.Put(reader)
}

func acquireZlibReader(r io.Reader) (resettableZlibReader, error) {
	select {
	case reader := <-zlibReaderPool:
		if err := reader.Reset(r, nil); err == nil {
			return reader, nil
		}
		_ = reader.Close()
	default:
	}

	reader, err := zlib.NewReader(r)
	if err != nil {
		return nil, err
	}

	resettable, ok := reader.(resettableZlibReader)
	if !ok {
		_ = reader.Close()
		return nil, errors.New("zlib reader does not support reset")
	}
	return resettable, nil
}

func releaseZlibReader(reader resettableZlibReader) {
	_ = reader.Close()
	select {
	case zlibReaderPool <- reader:
	default:
	}
}

func acquireCommitObjectReader(r io.Reader) *bufio.Reader {
	reader := commitObjectReaderPool.Get().(*bufio.Reader)
	reader.Reset(r)
	return reader
}

func releaseCommitObjectReader(reader *bufio.Reader) {
	reader.Reset(nil)
	commitObjectReaderPool.Put(reader)
}

func acquirePackedRefsReader(r io.Reader) *bufio.Reader {
	reader := packedRefsReaderPool.Get().(*bufio.Reader)
	reader.Reset(r)
	return reader
}

func releasePackedRefsReader(reader *bufio.Reader) {
	reader.Reset(nil)
	packedRefsReaderPool.Put(reader)
}

func readTrimmedSmallFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var buf [512]byte
	n, err := file.Read(buf[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	if n == len(buf) {
		var extra [1]byte
		if extraN, extraErr := file.Read(extra[:]); extraErr == nil || (extraErr == io.EOF && extraN > 0) {
			return "", errors.New("git metadata file too large")
		}
	}
	return strings.TrimSpace(string(buf[:n])), nil
}

func parseCommitterEpoch(line string) (string, error) {
	line = strings.TrimSpace(line)
	lastSpace := strings.LastIndexByte(line, ' ')
	if lastSpace < 0 {
		return "", errors.New("invalid committer line")
	}
	prevSpace := strings.LastIndexByte(line[:lastSpace], ' ')
	if prevSpace < 0 || prevSpace+1 >= lastSpace {
		return "", errors.New("invalid committer line")
	}
	return line[prevSpace+1 : lastSpace], nil
}

func gitIsDirty(dir string) (bool, error) {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=normal")
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(output)) > 0, nil
}
