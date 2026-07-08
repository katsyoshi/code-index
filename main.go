package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("missing command")
	}
	switch args[0] {
	case "path":
		return cmdPath(args[1:])
	case "init":
		return cmdInit(args[1:])
	case "rebuild":
		return cmdRebuild(args[1:])
	case "update":
		return cmdUpdate(args[1:])
	case "defs":
		return cmdDefs(args[1:])
	case "files":
		return cmdFiles(args[1:])
	case "sql":
		return cmdSQL(args[1:])
	case "show":
		return cmdShow(args[1:])
	case "stats":
		return cmdStats(args[1:])
	case "metrics":
		return cmdMetrics(args[1:])
	case "status":
		return cmdStatus(args[1:])
	default:
		usage()
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: code-index <path|init|rebuild|update|defs|files|sql|show|stats|metrics|status> [options]")
}

func cmdPath(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: code-index path ROOT")
	}
	root, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	fmt.Println(defaultDBPath(root))
	return nil
}

func cmdInit(args []string) error {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	dbPath := flags.String("db", "", "database path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: code-index init [--db DB] ROOT")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return errors.New("sqlite3 command not found")
	}
	root, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", root)
	}
	db := *dbPath
	if db == "" {
		db = defaultDBPath(root)
	}
	if _, err := os.Stat(db); err == nil {
		return fmt.Errorf("index already exists: %s; run rebuild to replace it", db)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		return err
	}
	lock, err := acquireIndexLock(db, "init", root)
	if err != nil {
		return err
	}
	defer lock.release()
	if _, err := os.Stat(db); err == nil {
		return fmt.Errorf("index already exists: %s; run rebuild to replace it", db)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmpDB, err := createTempDBPath(db)
	if err != nil {
		return err
	}
	installed := false
	defer func() {
		if !installed {
			_ = removeDBFiles(tmpDB)
		}
	}()
	fts := hasFTS5()
	writer, wait, err := sqliteWriter(tmpDB)
	if err != nil {
		return err
	}
	writerOK := false
	defer func() {
		if !writerOK {
			_ = writer.Close()
			_ = wait()
		}
	}()
	writeSchema(writer, fts)
	writeOperationMetaSQL(writer, root, "init", fts)
	writeSQL(writer, "commit;\n")
	if err := writer.Close(); err != nil {
		return err
	}
	if err := wait(); err != nil {
		return err
	}
	writerOK = true
	if err := installBuiltDB(tmpDB, db); err != nil {
		return err
	}
	installed = true
	fmt.Printf("db: %s\n", db)
	fmt.Printf("root: %s\n", root)
	fmt.Printf("files: 0\n")
	fmt.Printf("symbols: 0\n")
	fmt.Printf("lines: 0\n")
	fmt.Printf("code_lines: 0\n")
	fmt.Printf("comment_lines: 0\n")
	fmt.Printf("blank_lines: 0\n")
	fmt.Printf("hash_algorithm: %s\n", contentHashAlgorithm)
	fmt.Printf("fts5: %s\n", yesNo(fts))
	return nil
}

func cmdRebuild(args []string) error {
	return runRebuild(args)
}

func cmdUpdate(args []string) error {
	return runUpdate(args)
}

func runRebuild(args []string) error {
	flags := flag.NewFlagSet("rebuild", flag.ExitOnError)
	dbPath := flags.String("db", "", "database path")
	maxBytes := flags.Int64("max-bytes", 1_000_000, "skip files larger than this")
	var extraIgnored repeatedFlag
	flags.Var(&extraIgnored, "ignore-dir", "extra directory name to ignore")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: code-index rebuild [--db DB] [--max-bytes N] ROOT")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return errors.New("sqlite3 command not found")
	}
	root, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", root)
	}
	db := *dbPath
	if db == "" {
		db = defaultDBPath(root)
	}
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		return err
	}
	lock, err := acquireIndexLock(db, "rebuild", root)
	if err != nil {
		if isIndexLocked(err) {
			printLockSkipped(db)
			return nil
		}
		return err
	}
	defer lock.release()
	tmpDB, err := createTempDBPath(db)
	if err != nil {
		return err
	}
	installed := false
	defer func() {
		if !installed {
			_ = removeDBFiles(tmpDB)
		}
	}()
	fts := hasFTS5()
	writer, wait, err := sqliteWriter(tmpDB)
	if err != nil {
		return err
	}
	writerOK := false
	defer func() {
		if !writerOK {
			_ = writer.Close()
			_ = wait()
		}
	}()
	writeSchema(writer, fts)
	ignored := cloneIgnored(extraIgnored)
	var fileCount, symbolCount, lineCount int
	var codeLineCount, commentLineCount, blankLineCount int
	nextFileID := 1
	nextSymbolID := 1
	err = walkGitTrackedFiles(root, ignored, *maxBytes, func(path string, info fs.FileInfo) error {
		index, err := scanFileIndex(root, path, info, *maxBytes)
		if err != nil {
			return nil
		}
		writeFileIndexInsertSQL(writer, index, fts, nextFileID, &nextSymbolID)
		fileCount++
		symbolCount += len(index.symbols)
		lineCount += len(index.lines)
		codeLineCount += index.metrics.codeLines
		commentLineCount += index.metrics.commentLines
		blankLineCount += index.metrics.blankLines
		nextFileID++
		return nil
	})
	if err != nil {
		return err
	}
	writeOperationMetaSQL(writer, root, "rebuild", fts)
	writeSQL(writer, "commit;\n")
	if err := writer.Close(); err != nil {
		return err
	}
	if err := wait(); err != nil {
		return err
	}
	writerOK = true
	if err := installBuiltDB(tmpDB, db); err != nil {
		return err
	}
	installed = true
	fmt.Printf("db: %s\n", db)
	fmt.Printf("root: %s\n", root)
	fmt.Printf("files: %d\n", fileCount)
	fmt.Printf("symbols: %d\n", symbolCount)
	fmt.Printf("lines: %d\n", lineCount)
	fmt.Printf("code_lines: %d\n", codeLineCount)
	fmt.Printf("comment_lines: %d\n", commentLineCount)
	fmt.Printf("blank_lines: %d\n", blankLineCount)
	fmt.Printf("hash_algorithm: %s\n", contentHashAlgorithm)
	fmt.Printf("fts5: %s\n", yesNo(fts))
	return nil
}

func runUpdate(args []string) error {
	flags := flag.NewFlagSet("update", flag.ExitOnError)
	dbPath := flags.String("db", "", "database path")
	maxBytes := flags.Int64("max-bytes", 1_000_000, "skip files larger than this")
	var extraIgnored repeatedFlag
	flags.Var(&extraIgnored, "ignore-dir", "extra directory name to ignore")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: code-index update [--db DB] [--max-bytes N] ROOT")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return errors.New("sqlite3 command not found")
	}
	root, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", root)
	}
	db := *dbPath
	if db == "" {
		db = defaultDBPath(root)
	}
	if !fileExists(db) {
		if _, locked, err := readActiveIndexLock(db); err != nil {
			return err
		} else if locked {
			printLockSkipped(db)
			return nil
		}
		return fmt.Errorf("index not found: %s; run init or rebuild first, or pass --db", db)
	}
	lock, err := acquireIndexLock(db, "update", root)
	if err != nil {
		if isIndexLocked(err) {
			printLockSkipped(db)
			return nil
		}
		return err
	}
	defer lock.release()
	existing, err := loadIndexedFileStates(db)
	if err != nil {
		return err
	}
	fts, err := dbHasFTS5Tables(db)
	if err != nil {
		return err
	}
	writer, wait, err := sqliteWriter(db)
	if err != nil {
		return err
	}
	writerOK := false
	defer func() {
		if !writerOK {
			_ = writer.Close()
			_ = wait()
		}
	}()
	writeSQL(writer, ".bail on\n")
	writeSQL(writer, ".timeout 5000\n")
	writeSQL(writer, "begin immediate;\n")
	ignored := cloneIgnored(extraIgnored)
	seen := map[string]bool{}
	var added, updated, deleted, unchanged int
	var symbolCount, lineCount int
	var codeLineCount, commentLineCount, blankLineCount int
	err = walkGitTrackedFiles(root, ignored, *maxBytes, func(path string, info fs.FileInfo) error {
		index, err := scanFileIndex(root, path, info, *maxBytes)
		if err != nil {
			return nil
		}
		seen[index.path] = true
		state, existed := existing[index.path]
		if existed && state.contentHash == index.contentHash {
			unchanged++
			return nil
		}
		if existed {
			updated++
		} else {
			added++
		}
		writeFileIndexDeleteSQL(writer, index.path, fts)
		writeFileIndexInsertSQL(writer, index, fts, 0, nil)
		symbolCount += len(index.symbols)
		lineCount += len(index.lines)
		codeLineCount += index.metrics.codeLines
		commentLineCount += index.metrics.commentLines
		blankLineCount += index.metrics.blankLines
		return nil
	})
	if err != nil {
		return err
	}
	for rel := range existing {
		if seen[rel] {
			continue
		}
		writeFileIndexDeleteSQL(writer, rel, fts)
		deleted++
	}
	writeOperationMetaSQL(writer, root, "update", fts)
	writeSQL(writer, "commit;\n")
	if err := writer.Close(); err != nil {
		return err
	}
	if err := wait(); err != nil {
		return err
	}
	writerOK = true
	fmt.Printf("db: %s\n", db)
	fmt.Printf("root: %s\n", root)
	fmt.Printf("added: %d\n", added)
	fmt.Printf("updated: %d\n", updated)
	fmt.Printf("deleted: %d\n", deleted)
	fmt.Printf("unchanged: %d\n", unchanged)
	fmt.Printf("symbols: %d\n", symbolCount)
	fmt.Printf("lines: %d\n", lineCount)
	fmt.Printf("code_lines: %d\n", codeLineCount)
	fmt.Printf("comment_lines: %d\n", commentLineCount)
	fmt.Printf("blank_lines: %d\n", blankLineCount)
	fmt.Printf("hash_algorithm: %s\n", contentHashAlgorithm)
	fmt.Printf("fts5: %s\n", yesNo(fts))
	return nil
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
		return errors.New("usage: code-index defs [--root ROOT|--db DB] [--kind KIND] [--language LANG] QUERY")
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
		return errors.New("usage: code-index files [--root ROOT|--db DB] [--language LANG] QUERY")
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
		return errors.New("usage: code-index sql [--root ROOT|--db DB] [SQL]")
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
		return errors.New("usage: code-index show [--root ROOT|--db DB] --line N [--context N] PATH")
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
	return runSQLitePrint(requiredDB(*db, *root), mustEmbeddedSQL("stats.sql"))
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
		return errors.New("usage: code-index metrics [--root ROOT|--db DB] [--language LANG] [--limit N] [PATH_QUERY]")
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

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	dbFlag := fs.String("db", "", "database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: code-index status [--root ROOT|--db DB]")
	}
	db := requiredDB(*dbFlag, *root)
	dbExists := fileExists(db)
	lockInfo, locked, err := readIndexLock(db)
	if err != nil {
		return err
	}
	if !dbExists && !locked {
		return fmt.Errorf("index not found: %s; run init or rebuild first, or pass --db", db)
	}
	fmt.Println("key\tvalue")
	fmt.Printf("db\t%s\n", db)
	fmt.Printf("exists\t%s\n", yesNo(dbExists))
	fmt.Printf("locked\t%s\n", yesNo(locked))
	if dbExists {
		meta, err := loadMeta(db)
		if err != nil {
			return err
		}
		printMetaStatus(meta)
		printCurrentStatus(*root, meta)
	}
	if locked {
		fmt.Printf("lock\t%s\n", indexLockPath(db))
		fmt.Printf("lock_operation\t%s\n", lockInfo.operationName())
		fmt.Printf("lock_pid\t%s\n", lockInfo.pidText())
		fmt.Printf("lock_stale\t%s\n", yesNo(isStaleIndexLock(lockInfo)))
		if lockInfo.startedAt != "" {
			fmt.Printf("lock_started_at\t%s\n", lockInfo.startedAt)
		}
		if lockInfo.root != "" {
			fmt.Printf("lock_root\t%s\n", lockInfo.root)
		}
	}
	return nil
}

func printMetaStatus(meta map[string]string) {
	for _, key := range []string{
		"root",
		"schema_version",
		"file_source",
		"hash_algorithm",
		"indexed_at",
		"updated_at",
		"last_operation",
		"vcs_kind",
		"vcs_head",
		"vcs_branch",
		"vcs_dirty",
		"vcs_revision",
		"vcs_ref",
		"fts5",
	} {
		if value := meta[key]; value != "" {
			fmt.Printf("%s\t%s\n", key, value)
		}
	}
}

func printCurrentStatus(root string, meta map[string]string) {
	if root == "" {
		root = meta["root"]
	}
	if root == "" {
		return
	}
	status, ok := currentVCSStatus(root)
	if !ok {
		return
	}
	fmt.Printf("current_vcs_kind\t%s\n", status.kind)
	if status.revision != "" {
		fmt.Printf("current_vcs_head\t%s\n", status.revision)
	}
	if status.ref != "" {
		fmt.Printf("current_vcs_branch\t%s\n", status.ref)
	}
	if status.dirty != "" {
		fmt.Printf("current_vcs_dirty\t%s\n", yesNo(status.dirty == boolText(true)))
	}
	if status.revision == "" && status.dirty == "" {
		return
	}
	indexedHead := meta["vcs_head"]
	if indexedHead == "" {
		indexedHead = meta["vcs_revision"]
	}
	if indexedHead == "" {
		fmt.Println("index_stale\tunknown")
		return
	}
	stale := status.revision != indexedHead || status.dirty == boolText(true)
	fmt.Printf("index_stale\t%s\n", yesNo(stale))
}
