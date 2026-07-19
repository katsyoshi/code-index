package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexLockPreventsConcurrentBuilds(t *testing.T) {
	db := filepath.Join(t.TempDir(), "index.sqlite")
	lock, err := acquireIndexLock(db, "rebuild", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	_, err = acquireIndexLock(db, "rebuild", "/repo")
	if err == nil {
		t.Fatal("second lock acquisition succeeded, want failure")
	}
	if !isIndexLocked(err) {
		t.Fatalf("lock error isIndexLocked = false, err = %v", err)
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("lock error = %q, want already in progress", err)
	}
	lock.release()
	if _, err := os.Stat(indexLockPath(db)); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists or returned unexpected error: %v", err)
	}
}

func TestIndexLockReclaimsStaleLock(t *testing.T) {
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := os.WriteFile(indexLockPath(db), []byte("operation=rebuild\nroot=/repo\npid=123\nstarted_at=2026-01-01T00:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := indexLockProcessRunning
	indexLockProcessRunning = func(pid int) (bool, error) {
		if pid != 123 {
			t.Fatalf("pid = %d, want 123", pid)
		}
		return false, nil
	}
	defer func() {
		indexLockProcessRunning = old
	}()

	lock, err := acquireIndexLock(db, "update", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	info, locked, err := readIndexLock(db)
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("lock was not recreated")
	}
	if info.operation != "update" {
		t.Fatalf("operation = %q, want update", info.operation)
	}
}

func TestQueryLockNoticeUsesPreviousIndex(t *testing.T) {
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := os.WriteFile(db, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireIndexLock(db, "rebuild", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	notice, err := queryLockNotice(db)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice, "rebuild") || !strings.Contains(notice, "using previous index") {
		t.Fatalf("notice = %q, want rebuild warning with previous index", notice)
	}
}

func TestQueryLockNoticeIgnoresStaleLock(t *testing.T) {
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := os.WriteFile(db, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexLockPath(db), []byte("operation=rebuild\nroot=/repo\npid=123\nstarted_at=2026-01-01T00:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := indexLockProcessRunning
	indexLockProcessRunning = func(pid int) (bool, error) {
		return false, nil
	}
	defer func() {
		indexLockProcessRunning = old
	}()

	notice, err := queryLockNotice(db)
	if err != nil {
		t.Fatal(err)
	}
	if notice != "" {
		t.Fatalf("notice = %q, want empty", notice)
	}
	if _, err := os.Stat(indexLockPath(db)); !os.IsNotExist(err) {
		t.Fatalf("stale lock still exists or returned unexpected error: %v", err)
	}
}

func TestQueryLockNoticeFailsWithoutPreviousIndex(t *testing.T) {
	db := filepath.Join(t.TempDir(), "index.sqlite")
	lock, err := acquireIndexLock(db, "init", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	_, err = queryLockNotice(db)
	if err == nil {
		t.Fatal("queryLockNotice succeeded, want failure")
	}
	if !strings.Contains(err.Error(), "no previous index") {
		t.Fatalf("error = %q, want no previous index", err)
	}
}

func TestRebuildSkipsWhenLocked(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "index.sqlite")
	lock, err := acquireIndexLock(db, "rebuild", root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	var result fullBuildJSONResult
	decodeRunJSON(t, []string{"rebuild", "--db", db, "--format", "json", root}, &result)
	if result.Operation != "rebuild" || !result.Skipped || result.Reason == nil || *result.Reason != "locked" || result.Files != nil || result.FTS5 != nil {
		t.Fatalf("skipped rebuild JSON = %#v", result)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("db exists after skipped rebuild or returned unexpected error: %v", err)
	}
}

func TestUpdateSkipsWhenLockedWithoutExistingDB(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "index.sqlite")
	lock, err := acquireIndexLock(db, "init", root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	if err := run([]string{"update", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	var result updateJSONResult
	decodeRunJSON(t, []string{"update", "--db", db, "--format", "json", root}, &result)
	if result.Operation != "update" || !result.Skipped || result.Reason == nil || *result.Reason != "locked" || result.AddedFiles != nil || result.FTS5 != nil {
		t.Fatalf("skipped update JSON = %#v", result)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("db exists after skipped update or returned unexpected error: %v", err)
	}
}
