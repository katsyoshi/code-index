package main

import (
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	var result versionJSONResult
	decodeRunJSON(t, []string{"version", "--format", "json"}, &result)
	if result.SchemaVersion != 2 || result.FileSource != fileSource {
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

func TestPathCommandJSONOutput(t *testing.T) {
	root := t.TempDir()
	want := defaultDBPath(root)
	out := strings.TrimSpace(captureRunOutput(t, []string{"path", root}))
	if out != want {
		t.Fatalf("path text = %q, want %q", out, want)
	}
	var result struct {
		Path string `json:"path"`
	}
	decodeRunJSON(t, []string{"path", "--format", "json", root}, &result)
	if result.Path != want {
		t.Fatalf("path JSON = %#v, want %q", result, want)
	}
	if err := run([]string{"path", "--format", "yaml", root}); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("path with unsupported format error = %v", err)
	}
	withoutRoot := strings.TrimSpace(captureRunOutput(t, []string{"path"}))
	if withoutRoot == "" {
		t.Fatal("path without root returned an empty path")
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
	var all helpJSONResult
	decodeRunJSON(t, []string{"help", "--format", "json"}, &all)
	if all.Usage != topLevelUsage() || len(all.Commands) != len(commands) || all.Commands[0].Name != "help" {
		t.Fatalf("help JSON = %#v", all)
	}
	var update helpJSONCommand
	decodeRunJSON(t, []string{"help", "--format", "json", "update"}, &update)
	if update.Name != "update" || !strings.Contains(update.Usage, "code-index update") || update.Summary == "" {
		t.Fatalf("help update JSON = %#v", update)
	}
	if err := run([]string{"help", "--format", "yaml"}); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("help with unsupported format error = %v", err)
	}
	if err := run([]string{"help", "missing"}); err == nil {
		t.Fatal("help missing succeeded, want failure")
	}
}
