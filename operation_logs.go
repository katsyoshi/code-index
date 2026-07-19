package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	buildRunStatusSucceeded = "succeeded"
	buildRunStatusFailed    = "failed"
	buildRunStatusSkipped   = "skipped"
	maxStoredBuildRuns      = 1000
)

type buildRun struct {
	ID           int64   `json:"id"`
	Operation    string  `json:"operation"`
	Status       string  `json:"status"`
	Root         string  `json:"root"`
	DB           string  `json:"db"`
	StartedAt    string  `json:"started_at"`
	FinishedAt   string  `json:"finished_at"`
	ErrorMessage *string `json:"error"`
}

type buildRunRecorder struct {
	operation string
	status    string
	root      string
	db        string
	startedAt time.Time
}

func beginBuildRun(operation, db, root string) *buildRunRecorder {
	return &buildRunRecorder{
		operation: operation,
		status:    buildRunStatusSucceeded,
		root:      root,
		db:        db,
		startedAt: time.Now().UTC(),
	}
}

func (r *buildRunRecorder) skip() {
	r.status = buildRunStatusSkipped
}

func (r *buildRunRecorder) finish(operationErr *error) {
	status := r.status
	errorMessage := ""
	if *operationErr != nil {
		status = buildRunStatusFailed
		errorMessage = (*operationErr).Error()
	}
	run := buildRun{
		Operation:    r.operation,
		Status:       status,
		Root:         r.root,
		DB:           r.db,
		StartedAt:    r.startedAt.Format(time.RFC3339Nano),
		FinishedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		ErrorMessage: stringPointer(errorMessage),
	}
	if err := recordBuildRun(run); err != nil {
		fmt.Fprintf(os.Stderr, "warning: record operation log: %v\n", err)
	}
}

func operationLogPath(db string) string {
	return db + ".logs.sqlite"
}

func recordBuildRun(run buildRun) error {
	path := operationLogPath(run.DB)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	errorSQL := "null"
	if run.ErrorMessage != nil {
		errorSQL = quote(*run.ErrorMessage)
	}
	sql := mustEmbeddedSQL("operation_logs_schema.sql") + "\n" + formatEmbeddedSQL(
		"build_run_insert.sql",
		quote(run.Operation),
		quote(run.Status),
		quote(run.Root),
		quote(run.DB),
		quote(run.StartedAt),
		quote(run.FinishedAt),
		errorSQL,
		maxStoredBuildRuns,
	)
	cmd := exec.Command("sqlite3", "-batch", path, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3 operation log failed: %w: %s", err, out)
	}
	return nil
}

func loadBuildRuns(db string, limit int) ([]buildRun, error) {
	path := operationLogPath(db)
	if !fileExists(path) {
		return []buildRun{}, nil
	}
	rows := make([]buildRun, 0)
	if err := sqliteJSONQuery(path, formatEmbeddedSQL("build_runs_select.sql", limit), &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
