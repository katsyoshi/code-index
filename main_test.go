package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeFileMetricsGo(t *testing.T) {
	lines := []string{
		"package main",
		"",
		"// comment",
		"func main() {",
		"  /* block",
		"     comment */",
		"}",
	}

	got := computeFileMetrics("go", lines, 1)
	want := fileMetrics{
		lineCount:    7,
		blankLines:   1,
		commentLines: 3,
		codeLines:    3,
		symbolCount:  1,
	}
	if got != want {
		t.Fatalf("computeFileMetrics() = %+v, want %+v", got, want)
	}
}

func TestComputeFileMetricsInlineBlockComment(t *testing.T) {
	lines := []string{
		"let value = 1 /* starts here",
		"   still comment",
		"*/",
		"// done",
	}

	got := computeFileMetrics("javascript", lines, 0)
	want := fileMetrics{
		lineCount:    4,
		commentLines: 3,
		codeLines:    1,
	}
	if got != want {
		t.Fatalf("computeFileMetrics() = %+v, want %+v", got, want)
	}
}

func TestInstallBuiltDBReplacesDBAndRemovesSidecars(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "index.sqlite")
	tmpDB := filepath.Join(dir, ".index.sqlite.tmp")
	if err := os.WriteFile(db, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db+"-wal", []byte("old wal"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpDB, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpDB+"-wal", []byte("tmp wal"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := installBuiltDB(tmpDB, db); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("db content = %q, want new", got)
	}
	for _, path := range []string{db + "-wal", tmpDB, tmpDB + "-wal"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or returned unexpected error: %v", path, err)
		}
	}
}

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
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("db exists after skipped rebuild or returned unexpected error: %v", err)
	}
}

func TestStatusReportsLock(t *testing.T) {
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := os.WriteFile(db, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireIndexLock(db, "rebuild", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	if err := run([]string{"status", "--db", db}); err != nil {
		t.Fatal(err)
	}
}

func TestInitCommandCreatesEmptySQLiteIndexAndFailsIfExists(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "index.sqlite")

	if err := run([]string{"init", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sqlite3", "-batch", db, "select count(*) from files;").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "0" {
		t.Fatalf("file count = %q, want 0", out)
	}
	if _, err := os.Stat(indexLockPath(db)); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists or returned unexpected error: %v", err)
	}
	if err := run([]string{"init", "--db", db, root}); err == nil {
		t.Fatal("second init succeeded, want failure")
	}
}

func TestRebuildCommandCreatesSQLiteIndex(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "index.sqlite")

	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(indexLockPath(db)); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists or returned unexpected error: %v", err)
	}

	out, err := exec.Command("sqlite3", "-batch", db, "select count(*) from symbols where name = 'main';").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "1" {
		t.Fatalf("main symbol count = %q, want 1", out)
	}
}
