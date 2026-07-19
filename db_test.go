package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
