package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

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
