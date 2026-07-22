package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
		"schema_version":          float64(3),
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
	components, ok := result["components"].([]any)
	if !ok || len(components) != 5 {
		t.Fatalf("status JSON components = %#v, want five components", result["components"])
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
