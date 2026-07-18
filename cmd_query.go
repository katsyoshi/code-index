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

func cmdDefs(args []string) error {
	fs := flag.NewFlagSet("defs", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	kind := fs.String("kind", "", "symbol kind filter")
	language := fs.String("language", "", "language filter")
	limit := fs.Int("limit", 50, "maximum rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New(commandUsage("defs"))
	}
	query := fs.Arg(0)
	where := "(name = " + quote(query) + " collate nocase or name like " + quote(query+"%") + " collate nocase or signature like " + quote("%"+query+"%") + " collate nocase or path like " + quote("%"+query+"%") + " collate nocase)"
	if *kind != "" {
		where += " and kind = " + quote(*kind)
	}
	if *language != "" {
		where += " and language = " + quote(*language)
	}
	sql := formatEmbeddedSQL("defs.sql", where, quote(query), quote(query+"%"), *limit)
	return runSQLitePrint(requiredDB(*db, *root), sql)
}

func cmdFiles(args []string) error {
	fs := flag.NewFlagSet("files", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	language := fs.String("language", "", "language filter")
	limit := fs.Int("limit", 100, "maximum rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New(commandUsage("files"))
	}
	query := fs.Arg(0)
	where := "path like " + quote("%"+query+"%") + " collate nocase"
	if *language != "" {
		where += " and language = " + quote(*language)
	}
	sql := formatEmbeddedSQL("files.sql", where, *limit)
	return runSQLitePrint(requiredDB(*db, *root), sql)
}

func cmdSQL(args []string) error {
	fs := flag.NewFlagSet("sql", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	if err := fs.Parse(args); err != nil {
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
	return runSQLitePrint(requiredDB(*db, *root), query)
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	line := fs.Int("line", 0, "1-based line number")
	context := fs.Int("context", 3, "context lines")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || *line <= 0 {
		return errors.New(commandUsage("show"))
	}
	path := strings.TrimPrefix(filepath.ToSlash(fs.Arg(0)), "/")
	start := *line - *context
	if start < 1 {
		start = 1
	}
	end := *line + *context
	sql := formatEmbeddedSQL("show.sql", quote(path), quote("%"+path), quote(path), start, end)
	return runSQLitePrint(requiredDB(*db, *root), sql)
}

func cmdStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	db := fs.String("db", "", "database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(commandUsage("stats"))
	}
	return runSQLitePrint(requiredDB(*db, *root), mustEmbeddedSQL("stats.sql"))
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New(commandUsage("metrics"))
	}
	where := "1 = 1"
	if *language != "" {
		where += " and language = " + quote(*language)
	}
	var sql string
	if fs.NArg() == 0 {
		sql = formatEmbeddedSQL("metrics_summary.sql", where, *limit)
	} else {
		query := fs.Arg(0)
		where += " and path like " + quote("%"+query+"%") + " collate nocase"
		sql = formatEmbeddedSQL("metrics_files.sql", where, *limit)
	}
	return runSQLitePrint(requiredDB(*db, *root), sql)
}
