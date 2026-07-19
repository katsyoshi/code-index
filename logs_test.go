package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogsCommandReturnsEmptyResultsWithoutSidecar(t *testing.T) {
	db := filepath.Join(t.TempDir(), "index.sqlite")
	var runs []buildRun
	decodeRunJSON(t, []string{"logs", "--db", db, "--format", "json"}, &runs)
	if runs == nil || len(runs) != 0 {
		t.Fatalf("logs JSON = %#v, want empty array", runs)
	}
	out := captureRunOutput(t, []string{"logs", "--db", db})
	if !strings.HasPrefix(out, "id\toperation\tstatus\t") {
		t.Fatalf("logs text = %q, want header", out)
	}
}

func TestBuildRunsRecordSuccessSkipAndFailure(t *testing.T) {
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

	lock, err := acquireIndexLock(db, "rebuild", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"rebuild", "--db", db, root}); err != nil {
		lock.release()
		t.Fatal(err)
	}
	lock.release()

	err = run([]string{"update", "--db", db, "--max-bytes", "12345", root})
	if err == nil {
		t.Fatal("update with incompatible config succeeded, want failure")
	}

	runs, err := loadBuildRuns(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("build runs = %#v, want three rows", runs)
	}
	want := []struct {
		operation string
		status    string
	}{
		{"update", buildRunStatusFailed},
		{"rebuild", buildRunStatusSkipped},
		{"rebuild", buildRunStatusSucceeded},
	}
	for index, expected := range want {
		run := runs[index]
		if run.Operation != expected.operation || run.Status != expected.status || run.Root != root || run.DB != db || run.StartedAt == "" || run.FinishedAt == "" {
			t.Fatalf("build run %d = %#v, want operation=%s status=%s", index, run, expected.operation, expected.status)
		}
	}
	if runs[0].ErrorMessage == nil || !strings.Contains(*runs[0].ErrorMessage, "max bytes setting is incompatible") {
		t.Fatalf("failed build run error = %#v", runs[0].ErrorMessage)
	}
	if runs[1].ErrorMessage != nil || runs[2].ErrorMessage != nil {
		t.Fatalf("non-failed build run errors = %#v, %#v", runs[1].ErrorMessage, runs[2].ErrorMessage)
	}

	var limited []buildRun
	decodeRunJSON(t, []string{"logs", "--db", db, "--limit", "2", "--format", "json"}, &limited)
	if len(limited) != 2 || limited[0].Status != buildRunStatusFailed || limited[1].Status != buildRunStatusSkipped {
		t.Fatalf("limited logs JSON = %#v", limited)
	}
	out := captureRunOutput(t, []string{"logs", "--db", db, "--limit", "1"})
	if !strings.Contains(out, "id\toperation\tstatus") || !strings.Contains(out, "update\tfailed") {
		t.Fatalf("logs text = %q", out)
	}
}

func TestInitRecordsBuildRun(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	root := t.TempDir()
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"init", "--db", db, root}); err != nil {
		t.Fatal(err)
	}
	runs, err := loadBuildRuns(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Operation != "init" || runs[0].Status != buildRunStatusSucceeded {
		t.Fatalf("init build runs = %#v", runs)
	}
}

func TestLogsCommandRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"logs", "extra"},
		{"logs", "--limit", "0"},
		{"logs", "--format", "yaml"},
	} {
		if err := run(args); err == nil {
			t.Fatalf("%v succeeded, want failure", args)
		}
	}
}
