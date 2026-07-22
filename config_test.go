package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectConfigResolvesDBAndBuildSettingsWithoutMergingUserConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, projectConfigName)
	config := "db = \".index/code.sqlite\"\nmax_bytes = 12_345\nignore_dirs = [\n  \"generated\",\n]\n"
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	userConfigDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", userConfigDir)
	userConfigPath := filepath.Join(userConfigDir, "code-index", "config.toml")
	if err := os.MkdirAll(filepath.Dir(userConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userConfigPath, []byte("unknown = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.scope != configScopeProject || resolved.path != configPath {
		t.Fatalf("resolved config = %#v, want project config", resolved)
	}
	if resolved.db != filepath.Join(root, ".index", "code.sqlite") {
		t.Fatalf("resolved db = %q", resolved.db)
	}
	if resolved.build.maxBytes != 12_345 {
		t.Fatalf("max bytes = %d, want 12345", resolved.build.maxBytes)
	}
	containsGenerated := false
	for _, name := range resolved.build.ignoreDirs {
		containsGenerated = containsGenerated || name == "generated"
	}
	if !containsGenerated {
		t.Fatalf("multiline ignore_dirs was not applied: %#v", resolved.build.ignoreDirs)
	}
}

func TestUserConfigDoesNotAllowDB(t *testing.T) {
	root := t.TempDir()
	userConfigDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", userConfigDir)
	path := filepath.Join(userConfigDir, "code-index", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("db = \"index.sqlite\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveConfig(root); err == nil || !strings.Contains(err.Error(), "only allowed in project config") {
		t.Fatalf("resolveConfig error = %v", err)
	}
}

func TestProjectConfigRejectsDBOutsideRootAndUnknownKeys(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := filepath.Join(root, projectConfigName)
	if err := os.WriteFile(path, []byte("db = \"../index.sqlite\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveConfig(root); err == nil || !strings.Contains(err.Error(), "stay within") {
		t.Fatalf("outside-root db error = %v", err)
	}
	if err := os.WriteFile(path, []byte("max_byte = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveConfig(root); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("unknown-key error = %v", err)
	}
}

func TestEncodingFallbacksAreProjectOnlyAndOrdered(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := filepath.Join(root, projectConfigName)
	config := "[encoding]\nfallbacks = [\"Windows-31J\", \"EUC-JP\"]\n"
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Windows-31J", "EUC-JP"}
	if len(resolved.build.encodingFallbacks) != len(want) || resolved.build.encodingFallbacks[0] != want[0] || resolved.build.encodingFallbacks[1] != want[1] {
		t.Fatalf("encoding fallbacks = %#v", resolved.build.encodingFallbacks)
	}

	if err := os.WriteFile(path, []byte("[encoding]\nfallbacks = [\"EUC-JP\", \"euc-jp\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveConfig(root); err == nil || !strings.Contains(err.Error(), "duplicate encoding") {
		t.Fatalf("duplicate fallback error = %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "code-index", "config.toml")
	if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveConfig(root); err == nil || !strings.Contains(err.Error(), "only allowed in project config") {
		t.Fatalf("user encoding error = %v", err)
	}
}

func TestResolveRootOrCurrentUsesGitRoot(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	got, err := resolveRootOrCurrent("")
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("auto root = %q, want %q", got, root)
	}
}

func TestRebuildUsesProjectConfigAndCommandLineOverrides(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root, "main.go")
	config := "db = \".index/code.sqlite\"\nmax_bytes = 12345\nignore_dirs = [\"generated\"]\n[encoding]\nfallbacks = [\"Windows-31J\"]\n"
	if err := os.WriteFile(filepath.Join(root, projectConfigName), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := run([]string{"rebuild", root}); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(root, ".index", "code.sqlite")
	assertMetaValue(t, db, "config_max_bytes", "12345")
	ignoreDirs := ignoredDirNames([]string{"generated"})
	assertMetaValue(t, db, "config_ignore_dirs", stringListText(ignoreDirs))
	assertMetaValue(t, db, "config_encoding_fallbacks", stringListText([]string{"Windows-31J"}))

	err := run([]string{"update", "--max-bytes", "54321", root})
	if err == nil || !strings.Contains(err.Error(), "max bytes setting is incompatible") {
		t.Fatalf("update CLI override error = %v", err)
	}
	changedConfig := "db = \".index/code.sqlite\"\nmax_bytes = 12345\nignore_dirs = [\"generated\"]\n[encoding]\nfallbacks = [\"EUC-JP\"]\n"
	if err := os.WriteFile(filepath.Join(root, projectConfigName), []byte(changedConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"update", root})
	if err == nil || !strings.Contains(err.Error(), "encoding fallbacks setting is incompatible") {
		t.Fatalf("update encoding fallback error = %v", err)
	}
}
