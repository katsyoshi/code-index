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

type command struct {
	name    string
	usage   string
	summary string
}

var commands = []command{
	{name: "help", usage: "code-index help [COMMAND]", summary: "show command help"},
	{name: "version", usage: "code-index version", summary: "show build and schema information"},
	{name: "path", usage: "code-index path ROOT", summary: "print the default database path for a root"},
	{name: "init", usage: "code-index init [--db DB] ROOT", summary: "initialize an empty index database"},
	{name: "rebuild", usage: "code-index rebuild [--db DB] [--max-bytes N] ROOT", summary: "atomically rebuild the full index"},
	{name: "update", usage: "code-index update [--db DB] [--max-bytes N] ROOT", summary: "create or incrementally refresh the index"},
	{name: "defs", usage: "code-index defs [--root ROOT|--db DB] [--kind KIND] [--language LANG] QUERY", summary: "find symbol definitions"},
	{name: "files", usage: "code-index files [--root ROOT|--db DB] [--language LANG] QUERY", summary: "find indexed files"},
	{name: "sql", usage: "code-index sql [--root ROOT|--db DB] [SQL]", summary: "run read-only SQL"},
	{name: "show", usage: "code-index show [--root ROOT|--db DB] --line N [--context N] PATH", summary: "show indexed source around a line"},
	{name: "stats", usage: "code-index stats [--root ROOT|--db DB]", summary: "show index table counts"},
	{name: "metrics", usage: "code-index metrics [--root ROOT|--db DB] [--language LANG] [--limit N] [PATH_QUERY]", summary: "show indexed code metrics"},
	{name: "status", usage: "code-index status [--root ROOT|--db DB]", summary: "show index metadata, lock state, and freshness"},
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return errors.New("missing command")
	}
	if handler := commandHandler(args[0]); handler != nil {
		return handler(args[1:])
	}
	printUsage(os.Stderr)
	return fmt.Errorf("unknown command: %s", args[0])
}

func commandHandler(name string) func([]string) error {
	switch name {
	case "help":
		return cmdHelp
	case "version":
		return cmdVersion
	case "path":
		return cmdPath
	case "init":
		return cmdInit
	case "rebuild":
		return cmdRebuild
	case "update":
		return cmdUpdate
	case "defs":
		return cmdDefs
	case "files":
		return cmdFiles
	case "sql":
		return cmdSQL
	case "show":
		return cmdShow
	case "stats":
		return cmdStats
	case "metrics":
		return cmdMetrics
	case "status":
		return cmdStatus
	default:
		return nil
	}
}

func usage() {
	printUsage(os.Stderr)
}

func printUsage(w io.Writer) {
	names := make([]string, 0, len(commands))
	for _, cmd := range commands {
		names = append(names, cmd.name)
	}
	fmt.Fprintf(w, "usage: code-index <%s> [options]\n", strings.Join(names, "|"))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, cmd := range commands {
		fmt.Fprintf(w, "  %-8s %s\n", cmd.name, cmd.summary)
	}
}

func commandUsage(name string) string {
	for _, cmd := range commands {
		if cmd.name == name {
			return "usage: " + cmd.usage
		}
	}
	return "usage: code-index " + name
}

func cmdHelp(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}
	if len(args) != 1 {
		return errors.New(commandUsage("help"))
	}
	for _, cmd := range commands {
		if cmd.name == args[0] {
			fmt.Println("usage: " + cmd.usage)
			fmt.Println()
			fmt.Println(cmd.summary)
			return nil
		}
	}
	return fmt.Errorf("unknown command: %s", args[0])
}

func cmdVersion(args []string) error {
	if len(args) != 0 {
		return errors.New(commandUsage("version"))
	}
	info := currentBuildInfo()
	fmt.Println("key\tvalue")
	fmt.Printf("commit\t%s\n", info.commit)
	if info.modified != "" {
		fmt.Printf("modified\t%s\n", info.modified)
	}
	fmt.Printf("schema_version\t%s\n", schemaVersion)
	fmt.Printf("file_source\t%s\n", fileSource)
	return nil
}

func cmdPath(args []string) error {
	if len(args) != 1 {
		return errors.New(commandUsage("path"))
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
		return errors.New(commandUsage("init"))
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
		return errors.New(commandUsage("rebuild"))
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
		return errors.New(commandUsage("update"))
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
	lock, err := acquireIndexLock(db, "update", root)
	if err != nil {
		if isIndexLocked(err) {
			printLockSkipped(db)
			return nil
		}
		return err
	}
	defer lock.release()
	if !fileExists(db) {
		fts := hasFTS5()
		if err := createEmptyIndexDB(db, root, "update", fts); err != nil {
			return err
		}
	}
	existing, err := loadIndexedFileStates(db)
	if err != nil {
		return err
	}
	meta, err := loadMeta(db)
	if err != nil {
		return err
	}
	candidates, candidateOnly := updateCandidatePaths(root, existing, meta)
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
	var added, updated, deleted int
	var symbolCount int
	err = walkGitTrackedFileSet(root, ignored, *maxBytes, candidates, func(path string, info fs.FileInfo) error {
		index, err := scanFileIndex(root, path, info, *maxBytes)
		if err != nil {
			return nil
		}
		seen[index.path] = true
		state, existed := existing[index.path]
		if existed && state.contentHash == index.contentHash {
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
		return nil
	})
	if err != nil {
		return err
	}
	for rel := range existing {
		if seen[rel] {
			continue
		}
		if candidateOnly && !candidates[rel] {
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
	fmt.Printf("added_files: %d\n", added)
	fmt.Printf("updated_files: %d\n", updated)
	fmt.Printf("deleted_files: %d\n", deleted)
	fmt.Printf("symbols: %d\n", symbolCount)
	fmt.Printf("hash_algorithm: %s\n", contentHashAlgorithm)
	fmt.Printf("fts5: %s\n", yesNo(fts))
	return nil
}

func createEmptyIndexDB(db, root, operation string, fts bool) error {
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
	writeOperationMetaSQL(writer, root, operation, fts)
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

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	root := fs.String("root", "", "repository root for default database path")
	dbFlag := fs.String("db", "", "database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(commandUsage("status"))
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
		if err := printCurrentStatus(*root, meta); err != nil {
			return err
		}
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
		"vcs_dirty_hash",
		"vcs_revision",
		"vcs_ref",
		"fts5",
	} {
		if value := meta[key]; value != "" {
			fmt.Printf("%s\t%s\n", key, value)
		}
	}
}

func printCurrentStatus(root string, meta map[string]string) error {
	if root == "" {
		root = meta["root"]
	}
	if root == "" {
		return nil
	}
	status, ok := currentVCSStatus(root)
	if !ok {
		return nil
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
		return nil
	}
	indexedHead := meta["vcs_head"]
	if indexedHead == "" {
		indexedHead = meta["vcs_revision"]
	}
	if indexedHead == "" {
		fmt.Println("index_stale\tunknown")
		return nil
	}
	stale, known, err := currentIndexStale(root, meta, status, indexedHead)
	if err != nil {
		return err
	}
	if !known {
		fmt.Println("index_stale\tunknown")
		return nil
	}
	fmt.Printf("index_stale\t%s\n", yesNo(stale))
	return nil
}

func currentIndexStale(root string, meta map[string]string, status vcsStatus, indexedHead string) (bool, bool, error) {
	if status.revision != indexedHead {
		return true, true, nil
	}
	if status.dirty != boolText(true) {
		return false, true, nil
	}
	if meta["vcs_dirty"] != boolText(true) {
		return true, true, nil
	}
	currentHash, ok, err := currentDirtyHash(root)
	if err != nil {
		return false, false, err
	}
	if !ok || meta["vcs_dirty_hash"] == "" {
		return false, false, nil
	}
	return currentHash != meta["vcs_dirty_hash"], true, nil
}
