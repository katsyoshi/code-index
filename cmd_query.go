package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type schemaJSONRow struct {
	TableName  string  `json:"table_name"`
	Ordinal    int     `json:"ordinal"`
	ColumnName string  `json:"column_name"`
	Type       string  `json:"type"`
	Nullable   bool    `json:"nullable"`
	Key        *string `json:"key"`
}

type defsJSONRow struct {
	Path      string  `json:"path"`
	Line      int     `json:"line"`
	Kind      string  `json:"kind"`
	Name      string  `json:"name"`
	Language  *string `json:"language"`
	Signature string  `json:"signature"`
}

type filesJSONRow struct {
	Path     string  `json:"path"`
	Language *string `json:"language"`
	Size     int64   `json:"size"`
}

type showJSONRow struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type metricsSummaryJSONRow struct {
	Language string `json:"language"`
	Files    int    `json:"files"`
	Lines    int    `json:"lines"`
	Code     int    `json:"code"`
	Comments int    `json:"comments"`
	Blank    int    `json:"blank"`
	Symbols  int    `json:"symbols"`
}

type metricsFileJSONRow struct {
	Path     string  `json:"path"`
	Language *string `json:"language"`
	Lines    int     `json:"lines"`
	Code     int     `json:"code"`
	Comments int     `json:"comments"`
	Blank    int     `json:"blank"`
	Symbols  int     `json:"symbols"`
}

type statsJSONRaw struct {
	Root          *string `json:"root"`
	SchemaVersion *int64  `json:"schema_version"`
	FileSource    *string `json:"file_source"`
	IndexedAt     *string `json:"indexed_at"`
	UpdatedAt     *string `json:"updated_at"`
	LastOperation *string `json:"last_operation"`
	VCSKind       *string `json:"vcs_kind"`
	VCSRevision   *string `json:"vcs_revision"`
	VCSRef        *string `json:"vcs_ref"`
	VCSHead       *string `json:"vcs_head"`
	VCSBranch     *string `json:"vcs_branch"`
	VCSDirty      *int    `json:"vcs_dirty"`
	Files         int64   `json:"files"`
	Symbols       int64   `json:"symbols"`
	Lines         int64   `json:"lines"`
	CodeLines     int64   `json:"code_lines"`
	CommentLines  int64   `json:"comment_lines"`
	BlankLines    int64   `json:"blank_lines"`
	HashAlgorithm *string `json:"hash_algorithm"`
	FTS5          *int    `json:"fts5"`
}

type statsJSONResult struct {
	Root          *string `json:"root"`
	SchemaVersion *int64  `json:"schema_version"`
	FileSource    *string `json:"file_source"`
	IndexedAt     *string `json:"indexed_at"`
	UpdatedAt     *string `json:"updated_at"`
	LastOperation *string `json:"last_operation"`
	VCSKind       *string `json:"vcs_kind"`
	VCSRevision   *string `json:"vcs_revision"`
	VCSRef        *string `json:"vcs_ref"`
	VCSHead       *string `json:"vcs_head"`
	VCSBranch     *string `json:"vcs_branch"`
	VCSDirty      *bool   `json:"vcs_dirty"`
	Files         int64   `json:"files"`
	Symbols       int64   `json:"symbols"`
	Lines         int64   `json:"lines"`
	CodeLines     int64   `json:"code_lines"`
	CommentLines  int64   `json:"comment_lines"`
	BlankLines    int64   `json:"blank_lines"`
	HashAlgorithm *string `json:"hash_algorithm"`
	FTS5          *bool   `json:"fts5"`
}

func cmdDefs(args []string) error {
	fs := flag.NewFlagSet("defs", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	kind := fs.String("kind", "", "symbol kind filter")
	language := fs.String("language", "", "language filter")
	limit := fs.Int("limit", 50, "maximum rows")
	list := fs.Bool("list", false, "list definitions without a query")
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*list && fs.NArg() != 0) || (!*list && fs.NArg() != 1) {
		return errors.New(commandUsage("defs"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	where := "1 = 1"
	if !*list {
		query := fs.Arg(0)
		where = "(name = " + quote(query) + " collate nocase or name like " + quote(query+"%") + " collate nocase or signature like " + quote("%"+query+"%") + " collate nocase or path like " + quote("%"+query+"%") + " collate nocase)"
	}
	if *kind != "" {
		where += " and kind = " + quote(*kind)
	}
	if *language != "" {
		where += " and language = " + quote(*language)
	}
	var sql string
	if *list {
		sql = formatEmbeddedSQL("defs_list.sql", where, *limit)
	} else {
		query := fs.Arg(0)
		sql = formatEmbeddedSQL("defs.sql", where, quote(query), quote(query+"%"), *limit)
	}
	dbPath := requiredDB(*db, *root)
	if format == outputFormatText {
		return runSQLitePrint(dbPath, sql)
	}
	rows := make([]defsJSONRow, 0)
	if err := sqliteJSONQuery(dbPath, sql, &rows); err != nil {
		return err
	}
	return writeJSON(os.Stdout, rows)
}

func cmdFiles(args []string) error {
	fs := flag.NewFlagSet("files", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	language := fs.String("language", "", "language filter")
	limit := fs.Int("limit", 100, "maximum rows")
	list := fs.Bool("list", false, "list indexed files without a query")
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*list && fs.NArg() != 0) || (!*list && fs.NArg() != 1) {
		return errors.New(commandUsage("files"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	where := "1 = 1"
	if !*list {
		query := fs.Arg(0)
		where = "path like " + quote("%"+query+"%") + " collate nocase"
	}
	if *language != "" {
		where += " and language = " + quote(*language)
	}
	sql := formatEmbeddedSQL("files.sql", where, *limit)
	dbPath := requiredDB(*db, *root)
	if format == outputFormatText {
		return runSQLitePrint(dbPath, sql)
	}
	rows := make([]filesJSONRow, 0)
	if err := sqliteJSONQuery(dbPath, sql, &rows); err != nil {
		return err
	}
	return writeJSON(os.Stdout, rows)
}

func cmdSQL(args []string) error {
	fs := flag.NewFlagSet("sql", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	var query string
	if fs.NArg() == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		query = string(data)
	} else if fs.NArg() == 1 {
		query = fs.Arg(0)
	} else {
		return errors.New(commandUsage("sql"))
	}
	if err := validateReadOnlySQL(query); err != nil {
		return err
	}
	dbPath := requiredDB(*db, *root)
	if format == outputFormatText {
		return runSQLitePrint(dbPath, query)
	}
	rows := make([]map[string]any, 0)
	if err := sqliteJSONQuery(dbPath, query, &rows); err != nil {
		return err
	}
	return writeJSON(os.Stdout, rows)
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	line := fs.Int("line", 0, "1-based line number")
	context := fs.Int("context", 3, "context lines")
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || *line <= 0 {
		return errors.New(commandUsage("show"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	path := strings.TrimPrefix(filepath.ToSlash(fs.Arg(0)), "/")
	start := *line - *context
	if start < 1 {
		start = 1
	}
	end := *line + *context
	sql := formatEmbeddedSQL("show.sql", quote(path), quote("%"+path), quote(path), start, end)
	dbPath := requiredDB(*db, *root)
	if format == outputFormatText {
		return runSQLitePrint(dbPath, sql)
	}
	rows := make([]showJSONRow, 0)
	if err := sqliteJSONQuery(dbPath, sql, &rows); err != nil {
		return err
	}
	return writeJSON(os.Stdout, rows)
}

func cmdStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(commandUsage("stats"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	dbPath := requiredDB(*db, *root)
	if format == outputFormatText {
		return runSQLitePrint(dbPath, mustEmbeddedSQL("stats.sql"))
	}
	rows := make([]statsJSONRaw, 0, 1)
	if err := sqliteJSONQuery(dbPath, mustEmbeddedSQL("stats_json.sql"), &rows); err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("unexpected stats row count: %d", len(rows))
	}
	raw := rows[0]
	result := statsJSONResult{
		Root:          raw.Root,
		SchemaVersion: raw.SchemaVersion,
		FileSource:    raw.FileSource,
		IndexedAt:     raw.IndexedAt,
		UpdatedAt:     raw.UpdatedAt,
		LastOperation: raw.LastOperation,
		VCSKind:       raw.VCSKind,
		VCSRevision:   raw.VCSRevision,
		VCSRef:        raw.VCSRef,
		VCSHead:       raw.VCSHead,
		VCSBranch:     raw.VCSBranch,
		VCSDirty:      integerBoolPointer(raw.VCSDirty),
		Files:         raw.Files,
		Symbols:       raw.Symbols,
		Lines:         raw.Lines,
		CodeLines:     raw.CodeLines,
		CommentLines:  raw.CommentLines,
		BlankLines:    raw.BlankLines,
		HashAlgorithm: raw.HashAlgorithm,
		FTS5:          integerBoolPointer(raw.FTS5),
	}
	return writeJSON(os.Stdout, result)
}

func integerBoolPointer(value *int) *bool {
	if value == nil || (*value != 0 && *value != 1) {
		return nil
	}
	return boolPointer(*value == 1)
}

func cmdSchema(args []string) error {
	fs := flag.NewFlagSet("schema", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(commandUsage("schema"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	dbPath := requiredDB(*db, *root)
	if format == outputFormatText {
		return runSQLitePrint(dbPath, mustEmbeddedSQL("schema_query.sql"))
	}
	rows, err := loadSchemaJSONRows(dbPath)
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, rows)
}

func loadSchemaJSONRows(db string) ([]schemaJSONRow, error) {
	notice, err := queryLockNotice(db)
	if err != nil {
		return nil, err
	}
	if notice != "" {
		fmt.Fprint(os.Stderr, notice)
	}
	out, err := sqliteQueryOutput(db, mustEmbeddedSQL("schema_query_json.sql"))
	if err != nil {
		return nil, err
	}
	rows := make([]schemaJSONRow, 0)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		columns := strings.Split(line, "\t")
		if len(columns) != 6 {
			return nil, fmt.Errorf("unexpected schema row from sqlite3: %q", line)
		}
		ordinal, err := strconv.Atoi(columns[1])
		if err != nil {
			return nil, fmt.Errorf("invalid schema ordinal %q: %w", columns[1], err)
		}
		row := schemaJSONRow{
			TableName:  columns[0],
			Ordinal:    ordinal,
			ColumnName: columns[2],
			Type:       columns[3],
			Nullable:   columns[4] == "1",
			Key:        stringPointer(columns[5]),
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func cmdMetrics(args []string) error {
	fs := flag.NewFlagSet("metrics", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	language := fs.String("language", "", "language filter")
	limit := fs.Int("limit", 100, "maximum rows")
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New(commandUsage("metrics"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	where := "1 = 1"
	if *language != "" {
		where += " and language = " + quote(*language)
	}
	var sql string
	dbPath := requiredDB(*db, *root)
	if fs.NArg() == 0 {
		sql = formatEmbeddedSQL("metrics_summary.sql", where, *limit)
		if format == outputFormatJSON {
			rows := make([]metricsSummaryJSONRow, 0)
			if err := sqliteJSONQuery(dbPath, sql, &rows); err != nil {
				return err
			}
			return writeJSON(os.Stdout, rows)
		}
	} else {
		query := fs.Arg(0)
		where += " and path like " + quote("%"+query+"%") + " collate nocase"
		sql = formatEmbeddedSQL("metrics_files.sql", where, *limit)
		if format == outputFormatJSON {
			rows := make([]metricsFileJSONRow, 0)
			if err := sqliteJSONQuery(dbPath, sql, &rows); err != nil {
				return err
			}
			return writeJSON(os.Stdout, rows)
		}
	}
	return runSQLitePrint(dbPath, sql)
}
