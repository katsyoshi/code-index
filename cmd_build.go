package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

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
	if err := ensureIndexDoesNotExist(db); err != nil {
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
	if err := ensureIndexDoesNotExist(db); err != nil {
		return err
	}
	fts := hasFTS5()
	if err := createEmptyIndexDB(db, root, "init", fts); err != nil {
		return err
	}
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

func ensureIndexDoesNotExist(db string) error {
	if _, err := os.Stat(db); err == nil {
		return fmt.Errorf("index already exists: %s; run rebuild to replace it", db)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
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
	if !fileExists(db) {
		if _, locked, err := readActiveIndexLock(db); err != nil {
			return err
		} else if locked {
			printLockSkipped(db)
			return nil
		}
		return fmt.Errorf("index not found: %s; run init or rebuild first, or pass --db", db)
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
