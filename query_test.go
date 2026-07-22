package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatsCommandRejectsExtraArgs(t *testing.T) {
	if err := run([]string{"stats", "extra"}); err == nil {
		t.Fatal("stats with extra arg succeeded, want failure")
	}
}

func TestStatsCommandJSONOutputUsesNativeTypesAndNulls(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	root := t.TempDir()
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := run([]string{"init", "--db", db, root}); err != nil {
		t.Fatal(err)
	}

	var result statsJSONResult
	decodeRunJSON(t, []string{"stats", "--db", db, "--format", "json"}, &result)
	if result.Root == nil || *result.Root != root || result.SchemaVersion == nil || *result.SchemaVersion != 2 || result.FileSource == nil || *result.FileSource != fileSource {
		t.Fatalf("stats JSON metadata = %#v", result)
	}
	if result.Files != 0 || result.Symbols != 0 || result.Lines != 0 || result.CodeLines != 0 || result.CommentLines != 0 || result.BlankLines != 0 {
		t.Fatalf("stats JSON counts = %#v", result)
	}
	if result.FTS5 == nil || *result.FTS5 != hasFTS5() {
		t.Fatalf("stats JSON fts5 = %#v, want %t", result.FTS5, hasFTS5())
	}
	if result.VCSDirty != nil {
		t.Fatalf("stats JSON vcs_dirty = %#v, want null", result.VCSDirty)
	}
	if err := run([]string{"stats", "--db", db, "--format", "yaml"}); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("stats with unsupported format error = %v", err)
	}
}

func TestComponentsRecordCompletedBuildState(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 command not found")
	}
	root := t.TempDir()
	db := filepath.Join(t.TempDir(), "index.sqlite")
	if err := createEmptyIndexDB(db, root, "init", false, defaultBuildConfig()); err != nil {
		t.Fatal(err)
	}
	components, known, err := loadComponents(db)
	if err != nil {
		t.Fatal(err)
	}
	if !known || len(components) != 5 {
		t.Fatalf("components = %#v, known = %t", components, known)
	}
	wantNames := []string{"files", "lines", "symbols", "metrics", "fts"}
	for index, component := range components {
		if component.Name != wantNames[index] {
			t.Fatalf("component %d = %#v, want name %q", index, component, wantNames[index])
		}
		wantStatus := "ready"
		if component.Name == "fts" {
			wantStatus = "unavailable"
		}
		if component.Status != wantStatus || component.UpdatedAt == "" {
			t.Fatalf("component %s = %#v, want status %q and timestamp", component.Name, component, wantStatus)
		}
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
		"components\t1\tname\tTEXT\tno\tprimary(1)",
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
	var listedDefinitions []defsJSONRow
	decodeRunJSON(t, []string{"defs", "--db", db, "--list", "--language", "go", "--kind", "function", "--format", "json"}, &listedDefinitions)
	if len(listedDefinitions) != 1 || listedDefinitions[0].Name != "main" {
		t.Fatalf("defs --list JSON = %#v", listedDefinitions)
	}
	var noDefinitions []defsJSONRow
	decodeRunJSON(t, []string{"defs", "--db", db, "--list", "--kind", "class", "--format", "json"}, &noDefinitions)
	if noDefinitions == nil || len(noDefinitions) != 0 {
		t.Fatalf("empty defs --list JSON = %#v, want []", noDefinitions)
	}
	defsText := captureRunOutput(t, []string{"defs", "--db", db, "--list"})
	if !strings.Contains(defsText, "path\tline\tkind\tname\tlanguage\tsignature") || !strings.Contains(defsText, "main.go\t3\tfunction\tmain") {
		t.Fatalf("defs --list text = %q", defsText)
	}

	var outline []defsJSONRow
	decodeRunJSON(t, []string{"outline", "--db", db, "--format", "json", "main.go"}, &outline)
	if len(outline) != 1 || outline[0].Path != "main.go" || outline[0].Line != 3 || outline[0].Name != "main" {
		t.Fatalf("outline JSON = %#v", outline)
	}
	outlineText := captureRunOutput(t, []string{"outline", "--db", db, "main.go"})
	if !strings.Contains(outlineText, "path\tline\tkind\tname\tlanguage\tsignature") || !strings.Contains(outlineText, "main.go\t3\tfunction\tmain") {
		t.Fatalf("outline text = %q", outlineText)
	}
	var missingOutline []defsJSONRow
	decodeRunJSON(t, []string{"outline", "--db", db, "--format", "json", "missing.go"}, &missingOutline)
	if missingOutline == nil || len(missingOutline) != 0 {
		t.Fatalf("missing outline JSON = %#v, want []", missingOutline)
	}

	var files []filesJSONRow
	decodeRunJSON(t, []string{"files", "--db", db, "--format", "json", "main"}, &files)
	if len(files) != 1 || files[0].Path != "main.go" || files[0].Size != int64(len(content)) || files[0].Language == nil || *files[0].Language != "go" {
		t.Fatalf("files JSON = %#v", files)
	}
	var listedFiles []filesJSONRow
	decodeRunJSON(t, []string{"files", "--db", db, "--list", "--language", "go", "--format", "json"}, &listedFiles)
	if len(listedFiles) != 1 || listedFiles[0].Path != "main.go" {
		t.Fatalf("files --list JSON = %#v", listedFiles)
	}
	filesText := captureRunOutput(t, []string{"files", "--db", db, "--list"})
	if !strings.Contains(filesText, "path\tlanguage\tsize") || !strings.Contains(filesText, "main.go\tgo") {
		t.Fatalf("files --list text = %q", filesText)
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
		{"outline", "--db", db, "--format", "yaml", "main.go"},
		{"files", "--db", db, "--format", "yaml", "main"},
		{"show", "--db", db, "--line", "1", "--format", "yaml", "main.go"},
		{"metrics", "--db", db, "--format", "yaml"},
	}
	for _, args := range invalidFormatArgs {
		if err := run(args); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
			t.Fatalf("%s with unsupported format error = %v", args[0], err)
		}
	}
	if err := run([]string{"outline", "--db", db}); err == nil {
		t.Fatal("outline without path succeeded, want failure")
	}
	invalidListArgs := [][]string{
		{"defs", "--db", db, "--list", "main"},
		{"defs", "--db", db},
		{"files", "--db", db, "--list", "main"},
		{"files", "--db", db},
	}
	for _, args := range invalidListArgs {
		if err := run(args); err == nil {
			t.Fatalf("%s with invalid list/query arguments succeeded", args[0])
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
