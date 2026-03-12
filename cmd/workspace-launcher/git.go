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
	"strconv"
	"strings"
	"sync"
)

type resettableZlibReader interface {
	io.ReadCloser
	Reset(io.Reader, []byte) error
}

var (
	commitObjectReaderPool = sync.Pool{
		New: func() any {
			return bufio.NewReaderSize(strings.NewReader(""), 1024)
		},
	}
	zlibReaderPool sync.Pool
)

func inspectGitMeta(dir string, gitIsDir, wantBranch, wantEpoch, wantDirty bool) gitMeta {
	meta := gitMeta{
		present:     true,
		branchLabel: "-",
	}

	if !wantBranch && !wantEpoch {
		meta.isWorktree = !gitIsDir
		if !gitIsDir {
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
	if !gitIsDir {
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
					if epoch, readErr := readCommitEpoch(layout, hash); readErr == nil && epoch > 0 {
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

func classifyLinkedGitDir(gitDir string, isWorktree bool) (bool, bool) {
	if !isWorktree {
		return false, false
	}

	cleanParts := strings.Split(filepath.Clean(gitDir), string(filepath.Separator))
	gitIndex := -1
	for i := len(cleanParts) - 1; i >= 0; i-- {
		if cleanParts[i] == ".git" {
			gitIndex = i
			break
		}
	}
	if gitIndex >= 0 && gitIndex+1 < len(cleanParts) && cleanParts[gitIndex+1] == "modules" {
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
	for _, baseDir := range []string{layout.gitDir, layout.commonDir} {
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
	for _, baseDir := range []string{layout.gitDir, layout.commonDir} {
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

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[1] == refName {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("ref not found")
}

func readCommitEpoch(layout gitLayout, hash string) (int64, error) {
	if len(hash) < 40 {
		return 0, errors.New("invalid commit hash")
	}

	objectDirs := []string{
		filepath.Join(layout.gitDir, "objects"),
		filepath.Join(layout.commonDir, "objects"),
	}
	for _, objectDir := range objectDirs {
		epoch, err := readCommitEpochFromObjects(objectDir, hash)
		if err == nil {
			return epoch, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
	}
	return 0, os.ErrNotExist
}

func readCommitEpochFromObjects(objectDir, hash string) (int64, error) {
	objectPath := filepath.Join(objectDir, hash[:2], hash[2:])
	file, err := os.Open(objectPath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	reader, err := acquireZlibReader(file)
	if err != nil {
		return 0, err
	}
	defer releaseZlibReader(reader)

	buf := acquireCommitObjectReader(reader)
	defer releaseCommitObjectReader(buf)
	if _, err := buf.ReadBytes(0); err != nil {
		return 0, errors.New("invalid object header")
	}

	for {
		line, err := buf.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if strings.HasPrefix(line, "committer ") {
			epochText, parseErr := parseCommitterEpoch(line)
			if parseErr != nil {
				return 0, parseErr
			}
			epoch, parseErr := strconv.ParseInt(epochText, 10, 64)
			if parseErr != nil {
				return 0, parseErr
			}
			return epoch, nil
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	return 0, errors.New("committer line not found")
}

func acquireZlibReader(r io.Reader) (resettableZlibReader, error) {
	if pooled := zlibReaderPool.Get(); pooled != nil {
		reader := pooled.(resettableZlibReader)
		if err := reader.Reset(r, nil); err == nil {
			return reader, nil
		}
		_ = reader.Close()
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
	zlibReaderPool.Put(reader)
}

func acquireCommitObjectReader(r io.Reader) *bufio.Reader {
	reader := commitObjectReaderPool.Get().(*bufio.Reader)
	reader.Reset(r)
	return reader
}

func releaseCommitObjectReader(reader *bufio.Reader) {
	reader.Reset(strings.NewReader(""))
	commitObjectReaderPool.Put(reader)
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
