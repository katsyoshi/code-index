package main

import (
	"encoding/json"
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
	var result versionJSONResult
	decodeRunJSON(t, []string{"version", "--format", "json"}, &result)
	if result.SchemaVersion != 1 || result.FileSource != fileSource {
		t.Fatalf("version JSON = %#v", result)
	}
	if result.Commit != nil && *result.Commit == "unknown" {
		t.Fatalf("version JSON commit = %q, want null for unknown", *result.Commit)
	}
	if err := run([]string{"version", "--format", "yaml"}); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("version with unsupported format error = %v", err)
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

func TestSchemaCommandShowsUserTablesAndColumns(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	root := t.TempDir()
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"init", "--db", db, root}); err != nil {
		t.Fatal(err)
	}

	out := captureRunOutput(t, []string{"schema", "--db", db})
	for _, want := range []string{
		"table_name\tordinal\tcolumn_name\ttype\tnullable\tkey",
		"files\t1\tid\tINTEGER\tno\tprimary(1)",
		"lines\t2\tline\tINTEGER\tno\tprimary(2)",
		"symbols\t6\tname\tTEXT\tno\t-",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("schema output = %q, want %q", out, want)
		}
	}
	if strings.Contains(out, "files_fts_data") {
		t.Fatalf("schema output includes FTS shadow table: %q", out)
	}

	jsonOut := captureRunOutput(t, []string{"schema", "--db", db, "--format", "json"})
	var rows []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &rows); err != nil {
		t.Fatalf("schema JSON = %q: %v", jsonOut, err)
	}
	if len(rows) == 0 {
		t.Fatal("schema JSON is empty")
	}
	findRow := func(table, column string) map[string]any {
		t.Helper()
		for _, row := range rows {
			if row["table_name"] == table && row["column_name"] == column {
				return row
			}
		}
		t.Fatalf("schema JSON has no %s.%s row: %q", table, column, jsonOut)
		return nil
	}
	line := findRow("lines", "line")
	if line["ordinal"] != float64(2) || line["type"] != "INTEGER" || line["nullable"] != false || line["key"] != "primary(2)" {
		t.Fatalf("schema JSON lines.line = %#v", line)
	}
	name := findRow("symbols", "name")
	if name["nullable"] != false || name["key"] != nil {
		t.Fatalf("schema JSON symbols.name = %#v", name)
	}
	for _, row := range rows {
		if strings.HasPrefix(row["table_name"].(string), "files_fts_") {
			t.Fatalf("schema JSON includes FTS shadow table: %q", jsonOut)
		}
	}
	if err := run([]string{"schema", "--db", db, "--format", "yaml"}); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("schema with unsupported format error = %v", err)
	}
	if err := run([]string{"schema", "--db", db, "extra"}); err == nil {
		t.Fatal("schema with extra arg succeeded, want failure")
	}
}

func TestQueryCommandsJSONOutput(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not found")
	}
	root := t.TempDir()
	content := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root, "main.go")
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
		t.Fatal(err)
	}

	var definitions []defsJSONRow
	decodeRunJSON(t, []string{"defs", "--db", db, "--format", "json", "main"}, &definitions)
	if len(definitions) != 1 || definitions[0].Path != "main.go" || definitions[0].Line != 3 || definitions[0].Name != "main" || definitions[0].Language == nil || *definitions[0].Language != "go" {
		t.Fatalf("defs JSON = %#v", definitions)
	}

	var files []filesJSONRow
	decodeRunJSON(t, []string{"files", "--db", db, "--format", "json", "main"}, &files)
	if len(files) != 1 || files[0].Path != "main.go" || files[0].Size != int64(len(content)) || files[0].Language == nil || *files[0].Language != "go" {
		t.Fatalf("files JSON = %#v", files)
	}
	var noFiles []filesJSONRow
	decodeRunJSON(t, []string{"files", "--db", db, "--format", "json", "missing"}, &noFiles)
	if noFiles == nil || len(noFiles) != 0 {
		t.Fatalf("empty files JSON = %#v, want []", noFiles)
	}

	var shown []showJSONRow
	decodeRunJSON(t, []string{"show", "--db", db, "--line", "4", "--context", "0", "--format", "json", "main.go"}, &shown)
	if len(shown) != 1 || shown[0].Path != "main.go" || shown[0].Line != 4 || shown[0].Text != "\tprintln(\"hello\")" {
		t.Fatalf("show JSON = %#v", shown)
	}

	var summary []metricsSummaryJSONRow
	decodeRunJSON(t, []string{"metrics", "--db", db, "--format", "json"}, &summary)
	if len(summary) != 1 || summary[0].Language != "go" || summary[0].Files != 1 || summary[0].Lines != 5 || summary[0].Symbols != 1 {
		t.Fatalf("metrics summary JSON = %#v", summary)
	}
	var fileMetrics []metricsFileJSONRow
	decodeRunJSON(t, []string{"metrics", "--db", db, "--format", "json", "main"}, &fileMetrics)
	if len(fileMetrics) != 1 || fileMetrics[0].Path != "main.go" || fileMetrics[0].Lines != 5 || fileMetrics[0].Symbols != 1 {
		t.Fatalf("metrics files JSON = %#v", fileMetrics)
	}

	invalidFormatArgs := [][]string{
		{"defs", "--db", db, "--format", "yaml", "main"},
		{"files", "--db", db, "--format", "yaml", "main"},
		{"show", "--db", db, "--line", "1", "--format", "yaml", "main.go"},
		{"metrics", "--db", db, "--format", "yaml"},
	}
	for _, args := range invalidFormatArgs {
		if err := run(args); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
			t.Fatalf("%s with unsupported format error = %v", args[0], err)
		}
	}
}

func TestSQLCommandJSONOutputPreservesDynamicTypes(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	root := t.TempDir()
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"init", "--db", db, root}); err != nil {
		t.Fatal(err)
	}

	var rows []struct {
		IntegerValue int64   `json:"integer_value"`
		RealValue    float64 `json:"real_value"`
		Missing      *string `json:"missing"`
		TextValue    string  `json:"text_value"`
	}
	decodeRunJSON(t, []string{
		"sql", "--db", db, "--format", "json",
		"select 9007199254740993 as integer_value, 1.5 as real_value, null as missing, 'a' || char(9) || 'b' as text_value",
	}, &rows)
	if len(rows) != 1 || rows[0].IntegerValue != 9007199254740993 || rows[0].RealValue != 1.5 || rows[0].Missing != nil || rows[0].TextValue != "a\tb" {
		t.Fatalf("sql JSON = %#v", rows)
	}

	var empty []map[string]any
	decodeRunJSON(t, []string{"sql", "--db", db, "--format", "json", "select 1 as value where 0"}, &empty)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty sql JSON = %#v, want []", empty)
	}
	if err := run([]string{"sql", "--db", db, "--format", "yaml", "select 1"}); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("sql with unsupported format error = %v", err)
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

func TestStatusReportsUpdateCompatibility(t *testing.T) {
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

	out := captureRunOutput(t, []string{"status", "--db", db, "--root", root})
	for _, want := range []string{
		"update_compatible\tyes",
		"update_requires_adopt\tno",
		"update_rebuild_required\tno",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output = %q, want %s", out, want)
		}
	}
	otherRoot := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(root, otherRoot); err != nil {
		t.Fatal(err)
	}
	out = captureRunOutput(t, []string{"status", "--db", db, "--root", otherRoot})
	for _, want := range []string{
		"update_compatible\tno",
		"update_requires_adopt\tyes",
		"update_rebuild_required\tno",
		"update_blocker\tdifferent_checkout",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output = %q, want %s", out, want)
		}
	}
}

func TestStatusJSONUsesNativeTypesAndNulls(t *testing.T) {
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

	out := captureRunOutput(t, []string{"status", "--db", db, "--root", root, "--format", "json"})
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("status JSON = %q: %v", out, err)
	}
	for key, want := range map[string]any{
		"db":                      db,
		"exists":                  true,
		"locked":                  false,
		"schema_version":          float64(1),
		"config_max_bytes":        float64(defaultMaxBytes),
		"fts5":                    hasFTS5(),
		"current_vcs_dirty":       false,
		"update_compatible":       true,
		"update_requires_adopt":   false,
		"update_rebuild_required": false,
		"index_stale":             false,
	} {
		if got := result[key]; got != want {
			t.Fatalf("status JSON field %s = %#v, want %#v; output = %q", key, got, want, out)
		}
	}
	if result["update_blocker"] != nil || result["lock"] != nil {
		t.Fatalf("status JSON optional fields are not null: %q", out)
	}
	ignoreDirs, ok := result["config_ignore_dirs"].([]any)
	if !ok || len(ignoreDirs) == 0 {
		t.Fatalf("status JSON config_ignore_dirs = %#v, want non-empty array", result["config_ignore_dirs"])
	}
	if err := run([]string{"status", "--db", db, "--format", "yaml"}); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("status with unsupported format error = %v", err)
	}
}

func TestStatusJSONReportsLockWithoutDatabase(t *testing.T) {
	db := filepath.Join(t.TempDir(), "index.sqlite")
	lock, err := acquireIndexLock(db, "rebuild", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	out := captureRunOutput(t, []string{"status", "--db", db, "--format", "json"})
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("status JSON = %q: %v", out, err)
	}
	if result["exists"] != false || result["locked"] != true {
		t.Fatalf("status JSON lock state = %q", out)
	}
	if result["lock_operation"] != "rebuild" || result["lock_root"] != "/repo" {
		t.Fatalf("status JSON lock details = %q", out)
	}
	if _, ok := result["lock_pid"].(float64); !ok {
		t.Fatalf("status JSON lock_pid = %#v, want number", result["lock_pid"])
	}
	if result["schema_version"] != nil || result["index_stale"] != nil {
		t.Fatalf("status JSON unavailable fields are not null: %q", out)
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
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"init", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package main\n\nfunc dirtyName() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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

func decodeRunJSON(t *testing.T, args []string, destination any) {
	t.Helper()
	out := captureRunOutput(t, args)
	if err := json.Unmarshal([]byte(out), destination); err != nil {
		t.Fatalf("%s JSON = %q: %v", args[0], out, err)
	}
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
