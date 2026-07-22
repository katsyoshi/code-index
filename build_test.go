package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCommandsJSONOutput(t *testing.T) {
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

	initDB := filepath.Join(t.TempDir(), "init.sqlite")
	var initialized fullBuildJSONResult
	decodeRunJSON(t, []string{"init", "--db", initDB, "--format", "json", root}, &initialized)
	if initialized.Operation != "init" || initialized.Skipped || initialized.Files == nil || *initialized.Files != 0 || initialized.FTS5 == nil || *initialized.FTS5 != hasFTS5() {
		t.Fatalf("init JSON = %#v", initialized)
	}

	db := filepath.Join(t.TempDir(), "index.sqlite")
	var rebuilt fullBuildJSONResult
	decodeRunJSON(t, []string{"rebuild", "--db", db, "--format", "json", root}, &rebuilt)
	if rebuilt.Operation != "rebuild" || rebuilt.Skipped || rebuilt.Files == nil || *rebuilt.Files != 1 || rebuilt.Symbols == nil || *rebuilt.Symbols != 1 || rebuilt.Lines == nil || *rebuilt.Lines != 3 || rebuilt.Reason != nil {
		t.Fatalf("rebuild JSON = %#v", rebuilt)
	}

	if err := os.WriteFile(path, []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var updated updateJSONResult
	decodeRunJSON(t, []string{"update", "--db", db, "--format", "json", root}, &updated)
	if updated.Operation != "update" || updated.Skipped || updated.AddedFiles == nil || *updated.AddedFiles != 0 || updated.UpdatedFiles == nil || *updated.UpdatedFiles != 1 || updated.DeletedFiles == nil || *updated.DeletedFiles != 0 || updated.Symbols == nil || *updated.Symbols != 1 || updated.Reason != nil {
		t.Fatalf("update JSON = %#v", updated)
	}

	invalidFormatArgs := [][]string{
		{"init", "--db", filepath.Join(t.TempDir(), "invalid.sqlite"), "--format", "yaml", root},
		{"rebuild", "--db", filepath.Join(t.TempDir(), "invalid.sqlite"), "--format", "yaml", root},
		{"update", "--db", db, "--format", "yaml", root},
	}
	for _, args := range invalidFormatArgs {
		if err := run(args); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
			t.Fatalf("%s with unsupported format error = %v", args[0], err)
		}
	}
}

func TestEncodingSkipsAreStoredAndTransitionDuringUpdate(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	root := t.TempDir()
	path := filepath.Join(root, "legacy.rb")
	if err := os.WriteFile(path, []byte{0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root, "legacy.rb")
	db := filepath.Join(t.TempDir(), "index.sqlite")

	var rebuilt fullBuildJSONResult
	decodeRunJSON(t, []string{"rebuild", "-v", "--db", db, "--format", "json", root}, &rebuilt)
	if rebuilt.Files == nil || *rebuilt.Files != 0 || rebuilt.EncodingSkippedFiles == nil || *rebuilt.EncodingSkippedFiles != 1 || len(rebuilt.Diagnostics) != 1 || rebuilt.Diagnostics[0].Reason != skipReasonEncodingUnknown {
		t.Fatalf("rebuild encoding result = %#v", rebuilt)
	}
	var skipped []filesJSONRow
	decodeRunJSON(t, []string{"files", "--db", db, "--status", "skipped", "--list", "--format", "json"}, &skipped)
	if len(skipped) != 1 || skipped[0].Path != "legacy.rb" || skipped[0].Status != indexStatusSkipped || skipped[0].SkipReason == nil || *skipped[0].SkipReason != skipReasonEncodingUnknown {
		t.Fatalf("skipped files = %#v", skipped)
	}
	var indexed []filesJSONRow
	decodeRunJSON(t, []string{"files", "--db", db, "--list", "--format", "json"}, &indexed)
	if len(indexed) != 0 {
		t.Fatalf("indexed files = %#v", indexed)
	}

	if err := os.WriteFile(path, []byte("puts :ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var added updateJSONResult
	decodeRunJSON(t, []string{"update", "--db", db, "--format", "json", root}, &added)
	if added.AddedFiles == nil || *added.AddedFiles != 1 || added.DeletedFiles == nil || *added.DeletedFiles != 0 {
		t.Fatalf("skipped to indexed update = %#v", added)
	}

	if err := os.WriteFile(path, []byte{0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	var deleted updateJSONResult
	decodeRunJSON(t, []string{"update", "--db", db, "--format", "json", root}, &deleted)
	if deleted.DeletedFiles == nil || *deleted.DeletedFiles != 1 || deleted.EncodingSkippedFiles == nil || *deleted.EncodingSkippedFiles != 1 {
		t.Fatalf("indexed to skipped update = %#v", deleted)
	}
}

func TestUpdateCommandRequiresExistingIndex(t *testing.T) {
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

	err := run([]string{"update", "--db", db, root})
	if err == nil {
		t.Fatal("update without existing index succeeded, want failure")
	}
	if !strings.Contains(err.Error(), "run init or rebuild first") {
		t.Fatalf("error = %q, want init or rebuild guidance", err)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("db exists after failed update or returned unexpected error: %v", err)
	}
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

	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
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
	assertMetaValue(t, db, "config_max_bytes", int64Text(defaultMaxBytes))
	assertMetaValue(t, db, "config_ignore_dirs", stringListText(ignoredDirNames(nil)))
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
	assertMetaValue(t, db, "config_max_bytes", int64Text(defaultMaxBytes))
	assertMetaValue(t, db, "config_ignore_dirs", stringListText(ignoredDirNames(nil)))
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

func TestUpdateRejectsDifferentRootUnlessAdopted(t *testing.T) {
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
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	otherRoot := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(root, otherRoot); err != nil {
		t.Fatal(err)
	}

	err := run([]string{"update", "--db", db, otherRoot})
	if err == nil {
		t.Fatal("update with different root succeeded, want failure")
	}
	if !strings.Contains(err.Error(), "different checkout") || !strings.Contains(err.Error(), "update --adopt") {
		t.Fatalf("error = %q, want checkout mismatch with adopt guidance", err)
	}
	if err := run([]string{"update", "--db", db, "--adopt", otherRoot}); err != nil {
		t.Fatal(err)
	}
	assertMetaValue(t, db, "root", otherRoot)
}

func TestUpdateRejectsUnknownHistoryUnlessAdopted(t *testing.T) {
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
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	assertSQLiteValue(t, db, "update meta set value = '0000000000000000000000000000000000000000' where key in ('vcs_head', 'vcs_revision'); select changes();", "2")

	err := run([]string{"update", "--db", db, root})
	if err == nil {
		t.Fatal("update with unknown history succeeded, want failure")
	}
	if !strings.Contains(err.Error(), "unknown Git history") || !strings.Contains(err.Error(), "update --adopt") {
		t.Fatalf("error = %q, want history mismatch with adopt guidance", err)
	}
	if err := run([]string{"update", "--db", db, "--adopt", root}); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateAdoptDoesNotBypassSchemaMismatch(t *testing.T) {
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
	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	assertSQLiteValue(t, db, "update meta set value = '0' where key = 'schema_version'; select changes();", "1")

	err := run([]string{"update", "--db", db, "--adopt", root})
	if err == nil {
		t.Fatal("update --adopt with schema mismatch succeeded, want failure")
	}
	if !strings.Contains(err.Error(), "schema is incompatible") || !strings.Contains(err.Error(), "run rebuild") {
		t.Fatalf("error = %q, want schema mismatch with rebuild guidance", err)
	}
}

func TestUpdateRejectsConfigMismatch(t *testing.T) {
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
	if err := run([]string{"rebuild", "--db", db, "--max-bytes", "12345", root}); err != nil {
		t.Fatal(err)
	}
	assertMetaValue(t, db, "config_max_bytes", "12345")

	err := run([]string{"update", "--db", db, root})
	if err == nil {
		t.Fatal("update with max-bytes mismatch succeeded, want failure")
	}
	if !strings.Contains(err.Error(), "max bytes setting is incompatible") || !strings.Contains(err.Error(), "run rebuild") {
		t.Fatalf("error = %q, want max bytes mismatch with rebuild guidance", err)
	}
	out := captureRunOutput(t, []string{"status", "--db", db, "--root", root})
	for _, want := range []string{
		"update_compatible\tno",
		"update_rebuild_required\tyes",
		"update_blocker\tconfig_max_bytes",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output = %q, want %s", out, want)
		}
	}
}

func TestUpdateRejectsFTS5Mismatch(t *testing.T) {
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
	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	mismatched := boolText(!hasFTS5())
	assertSQLiteValue(t, db, "update meta set value = "+quote(mismatched)+" where key = 'fts5'; select changes();", "1")

	err := run([]string{"update", "--db", db, root})
	if err == nil {
		t.Fatal("update with fts5 mismatch succeeded, want failure")
	}
	if !strings.Contains(err.Error(), "FTS5 setting is incompatible") || !strings.Contains(err.Error(), "run rebuild") {
		t.Fatalf("error = %q, want FTS5 mismatch with rebuild guidance", err)
	}
	out := captureRunOutput(t, []string{"status", "--db", db, "--root", root})
	for _, want := range []string{
		"update_compatible\tno",
		"update_rebuild_required\tyes",
		"update_blocker\tfts5",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output = %q, want %s", out, want)
		}
	}
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
