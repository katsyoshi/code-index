package main

import (
	"io"
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

func TestVersionCommand(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"version", "extra"}); err == nil {
		t.Fatal("version with extra arg succeeded, want failure")
	}
}

func TestHelpCommand(t *testing.T) {
	out := captureRunOutput(t, []string{"help"})
	if !strings.Contains(out, "Commands:") || !strings.Contains(out, "update") {
		t.Fatalf("help output = %q, want command list", out)
	}
	out = captureRunOutput(t, []string{"help", "update"})
	if !strings.Contains(out, "usage: code-index update") {
		t.Fatalf("help update output = %q, want update usage", out)
	}
	if err := run([]string{"help", "missing"}); err == nil {
		t.Fatal("help missing succeeded, want failure")
	}
}

func TestStatsCommandRejectsExtraArgs(t *testing.T) {
	if err := run([]string{"stats", "extra"}); err == nil {
		t.Fatal("stats with extra arg succeeded, want failure")
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
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("db exists after skipped update or returned unexpected error: %v", err)
	}
}

func TestUpdateCommandCreatesSQLiteIndex(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.go"), []byte("package main\n\nfunc untracked() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root, "main.go")
	db := filepath.Join(t.TempDir(), "index.sqlite")

	if err := run([]string{"update", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(indexLockPath(db)); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists or returned unexpected error: %v", err)
	}
	assertSQLiteValue(t, db, "select count(*) from symbols where name = 'main';", "1")
	assertSQLiteValue(t, db, "select count(*) from symbols where name = 'untracked';", "0")
	assertMetaValue(t, db, "schema_version", schemaVersion)
	assertMetaValue(t, db, "file_source", fileSource)
	assertMetaValue(t, db, "hash_algorithm", contentHashAlgorithm)
	assertMetaValue(t, db, "last_operation", "update")
}

func TestUpdateOutputReportsChangedFileCounts(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root, "main.go")
	db := filepath.Join(t.TempDir(), "index.sqlite")

	if err := run([]string{"update", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	out := captureRunOutput(t, []string{"update", "--db", db, root})
	for _, want := range []string{"added_files:", "updated_files:", "deleted_files:", "symbols:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("update output = %q, want %s", out, want)
		}
	}
	for _, unwanted := range []string{"unchanged:", "lines:", "code_lines:", "comment_lines:", "blank_lines:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("update output = %q, want no %s", out, unwanted)
		}
	}
}

func TestStatusReportsLock(t *testing.T) {
	db := filepath.Join(t.TempDir(), "index.sqlite")
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
	assertMetaValue(t, db, "schema_version", schemaVersion)
	assertMetaValue(t, db, "file_source", fileSource)
	assertMetaValue(t, db, "hash_algorithm", contentHashAlgorithm)
	assertMetaValue(t, db, "last_operation", "init")
	assertSQLiteValue(t, db, "select count(*) from meta where key = 'indexed_at' and value != '';", "1")
	assertSQLiteValue(t, db, "select count(*) from meta where key = 'updated_at' and value != '';", "1")
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
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.go"), []byte("package main\n\nfunc untracked() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root, "main.go")
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
	assertMetaValue(t, db, "schema_version", schemaVersion)
	assertMetaValue(t, db, "file_source", fileSource)
	assertMetaValue(t, db, "hash_algorithm", contentHashAlgorithm)
	assertMetaValue(t, db, "last_operation", "rebuild")
	assertMetaValue(t, db, "vcs_kind", "git")
	assertSQLiteValue(t, db, "select count(*) from symbols where name = 'untracked';", "0")
}

func TestRebuildStoresVCSRevision(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root, "main.go")
	runGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "initial")
	revision := runGitOutput(t, root, "rev-parse", "HEAD")
	ref := runGitOutput(t, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	db := filepath.Join(t.TempDir(), "index.sqlite")

	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
		t.Fatal(err)
	}

	assertMetaValue(t, db, "vcs_revision", revision)
	assertMetaValue(t, db, "vcs_ref", ref)
	assertMetaValue(t, db, "vcs_head", revision)
	assertMetaValue(t, db, "vcs_branch", ref)
	assertMetaValue(t, db, "vcs_dirty", boolText(false))
	if err := run([]string{"status", "--db", db}); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateCommandAppliesFileChanges(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc oldName() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "stale.rb"), []byte("def gone\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root, "main.go", "stale.rb")
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc nextName() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "stale.rb")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "added.py"), []byte("def added():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "added.py")
	if err := os.WriteFile(filepath.Join(root, "untracked.py"), []byte("def local_only():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run([]string{"update", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	assertSQLiteValue(t, db, "select count(*) from symbols where name = 'oldName';", "0")
	assertSQLiteValue(t, db, "select count(*) from symbols where name = 'nextName';", "1")
	assertSQLiteValue(t, db, "select count(*) from symbols where name = 'gone';", "0")
	assertSQLiteValue(t, db, "select count(*) from symbols where name = 'added';", "1")
	assertSQLiteValue(t, db, "select count(*) from symbols where name = 'local_only';", "0")
	assertSQLiteValue(t, db, "select count(*) from files where path = 'stale.rb';", "0")
	assertSQLiteValue(t, db, "select count(*) from files where path = 'added.py';", "1")
	assertSQLiteValue(t, db, "select count(*) from files where path = 'untracked.py';", "0")
	assertMetaValue(t, db, "last_operation", "update")
}

func TestUpdateCommandRemovesStaleSymbolsAcrossCommits(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc oldName() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root, "main.go")
	runGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "initial")
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc nextName() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "main.go")
	runGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "rename function")

	if err := run([]string{"update", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	assertSQLiteValue(t, db, "select count(*) from symbols where name = 'oldName';", "0")
	assertSQLiteValue(t, db, "select count(*) from symbols where name = 'nextName';", "1")
	assertMetaValue(t, db, "last_operation", "update")
}

func TestStatusTreatsIndexedDirtyWorkTreeAsFresh(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not found")
	}
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root, "main.go")
	runGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "initial")
	if err := os.WriteFile(path, []byte("package main\n\nfunc dirtyName() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"update", "--db", db, root}); err != nil {
		t.Fatal(err)
	}

	out := captureRunOutput(t, []string{"status", "--db", db})
	if !strings.Contains(out, "index_stale\tno") {
		t.Fatalf("status output = %q, want index_stale no", out)
	}
	assertMetaValue(t, db, "vcs_dirty", boolText(true))
	assertSQLiteValue(t, db, "select count(*) from meta where key = 'vcs_dirty_hash' and value != '';", "1")

	if err := os.WriteFile(path, []byte("package main\n\nfunc dirtierName() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = captureRunOutput(t, []string{"status", "--db", db})
	if !strings.Contains(out, "index_stale\tyes") {
		t.Fatalf("status output = %q, want index_stale yes", out)
	}
}

func TestUpdateCommandIndexesInitializedDB(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root, "main.go")
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"init", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"update", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	assertSQLiteValue(t, db, "select count(*) from symbols where name = 'main';", "1")
}

func TestRebuildRequiresGitWorkTree(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "index.sqlite")
	err := run([]string{"rebuild", "--db", db, root})
	if err == nil {
		t.Fatal("rebuild succeeded outside a Git work tree, want failure")
	}
	if !strings.Contains(err.Error(), "Git work tree") {
		t.Fatalf("error = %q, want Git work tree", err)
	}
}

func assertSQLiteValue(t *testing.T, db, sql, want string) {
	t.Helper()
	out, err := exec.Command("sqlite3", "-batch", "-noheader", db, sql).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("sqlite value for %q = %q, want %q", sql, got, want)
	}
}

func assertMetaValue(t *testing.T, db, key, want string) {
	t.Helper()
	assertSQLiteValue(t, db, "select value from meta where key = "+quote(key)+";", want)
}

func initGitRepo(t *testing.T, root string, paths ...string) {
	t.Helper()
	runGit(t, root, "init")
	if len(paths) > 0 {
		args := append([]string{"add"}, paths...)
		runGit(t, root, args...)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func runGitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

func captureRunOutput(t *testing.T, args []string) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.Stdout = old
	}()
	os.Stdout = w
	runErr := run(args)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, readErr := io.ReadAll(r)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	return string(out)
}
