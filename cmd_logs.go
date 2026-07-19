package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	dbFlag := fs.String("db", "", "database path")
	limit := fs.Int("limit", 20, "maximum build runs")
	formatFlag := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *limit <= 0 {
		return errors.New(commandUsage("logs"))
	}
	format, err := parseOutputFormat(*formatFlag)
	if err != nil {
		return err
	}
	db, _, err := resolveDB(*dbFlag, *root)
	if err != nil {
		return err
	}
	if format == outputFormatJSON {
		runs, err := loadBuildRuns(db, *limit)
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, runs)
	}
	path := operationLogPath(db)
	if !fileExists(path) {
		fmt.Println("id\toperation\tstatus\troot\tdb\tstarted_at\tfinished_at\terror")
		return nil
	}
	return runSQLitePrint(path, formatEmbeddedSQL("build_runs_select.sql", *limit))
}
