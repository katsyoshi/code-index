package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type indexLock struct {
	path string
}

type indexLockInfo struct {
	operation string
	root      string
	pid       string
	startedAt string
}

var errIndexLocked = errors.New("index locked")

var indexLockProcessRunning = localProcessRunning

func indexLockPath(db string) string {
	return db + ".lock"
}

func acquireIndexLock(db, operation, root string) (*indexLock, error) {
	path := indexLockPath(db)
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				info, ok, readErr := readIndexLock(db)
				if readErr != nil || !ok {
					return nil, fmt.Errorf("%w: index operation already in progress: %s", errIndexLocked, path)
				}
				if isStaleIndexLock(info) {
					if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
						return nil, fmt.Errorf("%w: stale index lock could not be removed: %s", errIndexLocked, path)
					}
					continue
				}
				return nil, fmt.Errorf("%w: index %s already in progress for %s (pid %s, lock: %s)", errIndexLocked, info.operationName(), db, info.pidText(), path)
			}
			return nil, err
		}
		return writeIndexLockFile(file, path, operation, root)
	}
	return nil, fmt.Errorf("%w: index operation already in progress: %s", errIndexLocked, path)
}

func writeIndexLockFile(file *os.File, path, operation, root string) (*indexLock, error) {
	ok := false
	defer func() {
		if !ok {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if _, err := fmt.Fprintf(
		file,
		"operation=%s\nroot=%s\npid=%d\nstarted_at=%s\n",
		operation,
		root,
		os.Getpid(),
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	ok = true
	return &indexLock{path: path}, nil
}

func isIndexLocked(err error) bool {
	return errors.Is(err, errIndexLocked)
}

func (l *indexLock) release() {
	if l != nil {
		_ = os.Remove(l.path)
	}
}

func readIndexLock(db string) (indexLockInfo, bool, error) {
	data, err := os.ReadFile(indexLockPath(db))
	if errors.Is(err, os.ErrNotExist) {
		return indexLockInfo{}, false, nil
	}
	if err != nil {
		return indexLockInfo{}, false, err
	}
	return parseIndexLock(string(data)), true, nil
}

func readActiveIndexLock(db string) (indexLockInfo, bool, error) {
	info, locked, err := readIndexLock(db)
	if err != nil || !locked {
		return info, locked, err
	}
	if !isStaleIndexLock(info) {
		return info, true, nil
	}
	if err := os.Remove(indexLockPath(db)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return info, true, err
	}
	return indexLockInfo{}, false, nil
}

func parseIndexLock(text string) indexLockInfo {
	var info indexLockInfo
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "operation":
			info.operation = value
		case "root":
			info.root = value
		case "pid":
			info.pid = value
		case "started_at":
			info.startedAt = value
		}
	}
	return info
}

func (info indexLockInfo) operationName() string {
	if info.operation != "" {
		return info.operation
	}
	return "operation"
}

func (info indexLockInfo) pidText() string {
	if info.pid != "" {
		return info.pid
	}
	return "unknown"
}

func isStaleIndexLock(info indexLockInfo) bool {
	pid, err := strconv.Atoi(info.pid)
	if err != nil || pid <= 0 {
		return false
	}
	running, err := indexLockProcessRunning(pid)
	return err == nil && !running
}

func localProcessRunning(pid int) (bool, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return true, err
}

func queryLockNotice(db string) (string, error) {
	info, locked, err := readActiveIndexLock(db)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(db); err != nil {
		if locked && errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("index %s in progress: %s; no previous index is available yet", info.operationName(), db)
		}
		return "", fmt.Errorf("index not found: %s; run rebuild first, or pass --db", db)
	}
	if locked {
		return fmt.Sprintf("warning: index %s in progress; using previous index: %s\n", info.operationName(), db), nil
	}
	return "", nil
}

func printLockSkipped(db string) {
	info, locked, err := readActiveIndexLock(db)
	if err != nil || !locked {
		fmt.Fprintf(os.Stderr, "index operation already in progress; skipped: %s\n", db)
		return
	}
	fmt.Fprintf(os.Stderr, "index %s already in progress; skipped: %s\n", info.operationName(), db)
}
